package quote

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/vmorsell/tradingview-sdk-go/internal/protocol"
)

// Bridge is the subset of the TradingView Client the session consumes.
// Defined locally so quote never imports the root package — the Client
// satisfies this structurally.
type Bridge interface {
	Send(method string, params ...any) error
	Register(sessionID string, h func(env protocol.Envelope))
	Unregister(sessionID string)
}

// Option configures a Session.
type Option func(*config)

type config struct {
	fields       []Field
	customFields []Field
	bufferSize   int
}

// WithFields selects a preset bundle of fields.
func WithFields(p FieldPreset) Option {
	return func(c *config) { c.fields = fieldsForPreset(p) }
}

// WithCustomFields overrides the field set with a caller-provided list.
func WithCustomFields(fs ...Field) Option {
	return func(c *config) { c.customFields = append([]Field(nil), fs...) }
}

// WithBufferSize sets the Updates() channel capacity. Default 64.
// Use higher values for very chatty symbols or slow consumers.
func WithBufferSize(n int) Option {
	return func(c *config) { c.bufferSize = n }
}

// Session is one TradingView quote session hosting an arbitrary number of
// symbols. Create with Client.NewQuoteSession.
type Session struct {
	id     string
	bridge Bridge

	updates chan Update
	done    chan struct{}
	closeCh chan struct{}

	mu        sync.Mutex
	state     map[string]map[Field]any // accumulated last value per symbol
	subscribe map[string]struct{}      // symbolKey → subscribed flag

	dropped   atomic.Uint64
	closeOnce sync.Once
}

// New creates a new Session on bridge. Not usually called directly — use
// Client.NewQuoteSession in the root package.
func New(bridge Bridge, opts ...Option) (*Session, error) {
	cfg := &config{bufferSize: 64}
	for _, o := range opts {
		o(cfg)
	}
	fields := cfg.customFields
	if len(fields) == 0 {
		fields = cfg.fields
	}
	if len(fields) == 0 {
		fields = fieldsForPreset(FieldPresetAll)
	}

	s := &Session{
		id:        protocol.GenSessionID("qs"),
		bridge:    bridge,
		updates:   make(chan Update, cfg.bufferSize),
		done:      make(chan struct{}),
		closeCh:   make(chan struct{}),
		state:     map[string]map[Field]any{},
		subscribe: map[string]struct{}{},
	}
	bridge.Register(s.id, s.handle)

	if err := bridge.Send("quote_create_session", s.id); err != nil {
		bridge.Unregister(s.id)
		return nil, err
	}
	setFieldsArgs := make([]any, 0, len(fields)+1)
	setFieldsArgs = append(setFieldsArgs, s.id)
	for _, f := range fields {
		setFieldsArgs = append(setFieldsArgs, string(f))
	}
	if err := bridge.Send("quote_set_fields", setFieldsArgs...); err != nil {
		bridge.Unregister(s.id)
		return nil, err
	}
	return s, nil
}

// AddSymbol subscribes to a symbol on this session. Safe to call repeatedly
// with the same (symbol, kind); the server deduplicates.
func (s *Session) AddSymbol(symbol string, kind SessionKind) error {
	if kind == "" {
		kind = SessionRegular
	}
	key := symbolKey(symbol, kind)
	s.mu.Lock()
	_, ok := s.subscribe[key]
	if !ok {
		s.subscribe[key] = struct{}{}
	}
	s.mu.Unlock()
	if ok {
		return nil
	}
	return s.bridge.Send("quote_add_symbols", s.id, key)
}

// RemoveSymbol unsubscribes from a symbol.
func (s *Session) RemoveSymbol(symbol string, kind SessionKind) error {
	if kind == "" {
		kind = SessionRegular
	}
	key := symbolKey(symbol, kind)
	s.mu.Lock()
	delete(s.subscribe, key)
	delete(s.state, key)
	s.mu.Unlock()
	return s.bridge.Send("quote_remove_symbols", s.id, key)
}

// Updates returns the single event stream for this session. Closed when
// the session (or its client) shuts down.
func (s *Session) Updates() <-chan Update { return s.updates }

// Done fires when the session is fully closed.
func (s *Session) Done() <-chan struct{} { return s.done }

// DroppedUpdates reports how many non-priority updates have been dropped
// due to a slow consumer. Error and Completed variants are never dropped.
func (s *Session) DroppedUpdates() uint64 { return s.dropped.Load() }

// Close unsubscribes, tells TradingView to delete the session, and closes
// the updates channel. Idempotent.
func (s *Session) Close() error {
	var firstErr error
	s.closeOnce.Do(func() {
		close(s.closeCh)
		if err := s.bridge.Send("quote_delete_session", s.id); err != nil {
			firstErr = err
		}
		s.bridge.Unregister(s.id)
		close(s.updates)
		close(s.done)
	})
	return firstErr
}

// handle is invoked on the dispatcher goroutine.
func (s *Session) handle(env protocol.Envelope) {
	select {
	case <-s.closeCh:
		return
	default:
	}

	switch env.Method {
	case "qsd":
		s.handleQSD(env)
	case "quote_completed":
		s.handleCompleted(env)
	case "quote_error":
		s.handleError(env)
	}
}

// handleQSD handles a quote-session-data packet.
// params shape: [sessionID, {"n": symbolKey, "s": "ok|error", "v": {...}}].
func (s *Session) handleQSD(env protocol.Envelope) {
	if len(env.Params) < 2 {
		return
	}
	var payload struct {
		N string          `json:"n"`
		S string          `json:"s"`
		V json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal(env.Params[1], &payload); err != nil {
		return
	}
	symbol := parseSymbolFromKey(payload.N)

	if payload.S == "error" {
		s.priorityEmit(QuoteError{Symbol: symbol, Err: errors.New("quote error")})
		return
	}

	delta := map[Field]any{}
	if err := json.Unmarshal(payload.V, &delta); err != nil {
		return
	}

	s.mu.Lock()
	acc := s.state[payload.N]
	if acc == nil {
		acc = map[Field]any{}
		s.state[payload.N] = acc
	}
	maps.Copy(acc, delta)
	out := make(map[Field]any, len(acc))
	maps.Copy(out, acc)
	s.mu.Unlock()

	s.emit(QuoteData{Symbol: symbol, Fields: out})
}

func (s *Session) handleCompleted(env protocol.Envelope) {
	if len(env.Params) < 2 {
		return
	}
	var key string
	if err := json.Unmarshal(env.Params[1], &key); err != nil {
		return
	}
	s.priorityEmit(QuoteCompleted{Symbol: parseSymbolFromKey(key)})
}

func (s *Session) handleError(env protocol.Envelope) {
	// quote_error arrives with per-symbol context; forward as a priority
	// update so callers see it even under backpressure.
	sym := ""
	if len(env.Params) >= 2 {
		var key string
		if err := json.Unmarshal(env.Params[1], &key); err == nil {
			sym = parseSymbolFromKey(key)
		}
	}
	s.priorityEmit(QuoteError{Symbol: sym, Err: fmt.Errorf("%w: %v", ErrQuoteServer, env.Params)})
}

// emit pushes a data update with drop-oldest backpressure.
// Counts one drop per eviction so callers can observe pressure.
func (s *Session) emit(u Update) {
	select {
	case s.updates <- u:
		return
	default:
	}
	// Full — drop the oldest entry to make room, then send.
	select {
	case <-s.updates:
		s.dropped.Add(1)
	default:
	}
	select {
	case s.updates <- u:
	default:
		// Concurrent reader/writer race; the new update is lost too.
		s.dropped.Add(1)
	}
}

// priorityEmit delivers an error/completed update. If the channel is full
// it evicts an older data update to make room — priority emits are never
// themselves dropped unless Close has raced with us.
func (s *Session) priorityEmit(u Update) {
	select {
	case s.updates <- u:
		return
	default:
	}
	select {
	case <-s.updates:
		s.dropped.Add(1)
	default:
	}
	select {
	case s.updates <- u:
	default:
		s.dropped.Add(1)
	}
}

// ErrQuoteServer wraps a server-side quote error.
var ErrQuoteServer = errors.New("quote: server error")

// symbolKey is TradingView's wire-format symbol reference: `={"session":"regular","symbol":"BINANCE:BTCUSDT"}`.
func symbolKey(symbol string, kind SessionKind) string {
	b, _ := json.Marshal(struct {
		Session string `json:"session"`
		Symbol  string `json:"symbol"`
	}{Session: string(kind), Symbol: symbol})
	return "=" + string(b)
}

// parseSymbolFromKey pulls the original symbol out of a wire key. Falls
// back to the key itself if decoding fails.
func parseSymbolFromKey(key string) string {
	if len(key) > 0 && key[0] == '=' {
		var obj struct {
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal([]byte(key[1:]), &obj); err == nil {
			return obj.Symbol
		}
	}
	return key
}
