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

// ErrQuoteServer wraps a server-side quote error packet.
var ErrQuoteServer = errors.New("quote: server error")

// Bridge is the minimal subset of the TradingView Client that a quote
// session needs. It is defined locally so this package does not import
// the root package; the Client satisfies it structurally.
type Bridge interface {
	Send(method string, params ...any) error
	Register(sessionID string, h func(env protocol.Envelope))
	Unregister(sessionID string)
}

// Option configures a Session at construction time.
type Option func(*config)

type config struct {
	fields       []Field
	customFields []Field
	bufferSize   int
}

// WithFields selects a preset bundle of fields. See FieldPreset for the
// available presets.
func WithFields(p FieldPreset) Option {
	return func(c *config) { c.fields = fieldsForPreset(p) }
}

// WithCustomFields overrides the field set with a caller-provided list.
// Takes precedence over WithFields when both are supplied.
func WithCustomFields(fs ...Field) Option {
	return func(c *config) { c.customFields = append([]Field(nil), fs...) }
}

// WithBufferSize sets the capacity of the Updates channel (default 64).
// Raise this for chatty symbols or slow consumers; see the drop-oldest
// behaviour documented on Session.DroppedUpdates.
func WithBufferSize(n int) Option {
	return func(c *config) { c.bufferSize = n }
}

// Session is one TradingView quote session. It hosts any number of
// symbols and emits updates on a single channel.
type Session struct {
	id     string
	bridge Bridge

	updates chan Update
	done    chan struct{}
	closeCh chan struct{}

	mu        sync.Mutex
	state     map[string]map[Field]any // accumulated last value per symbol
	subscribe map[string]struct{}      // symbolKey -> subscribed flag

	dropped   atomic.Uint64
	closeOnce sync.Once
}

// New creates a new Session on bridge. In normal use callers go through
// Client.NewQuoteSession rather than calling this directly.
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

// AddSymbol subscribes to a symbol on this session. Repeated calls with
// the same (symbol, kind) are idempotent.
func (s *Session) AddSymbol(symbol string, kind SessionKind) error {
	if kind == "" {
		kind = SessionRegular
	}
	key := symbolKey(symbol, kind)
	s.mu.Lock()
	_, already := s.subscribe[key]
	if !already {
		s.subscribe[key] = struct{}{}
	}
	s.mu.Unlock()
	if already {
		return nil
	}
	return s.bridge.Send("quote_add_symbols", s.id, key)
}

// RemoveSymbol unsubscribes from a symbol. A no-op if the symbol was not
// previously subscribed.
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

// Updates returns the single event stream for this session. The channel
// is closed when the session (or its client) shuts down.
func (s *Session) Updates() <-chan Update { return s.updates }

// Done fires once the session is fully torn down.
func (s *Session) Done() <-chan struct{} { return s.done }

// DroppedUpdates reports how many data updates have been evicted under
// backpressure since the session was created. QuoteError and
// QuoteCompleted are delivered via a priority path and never count.
func (s *Session) DroppedUpdates() uint64 { return s.dropped.Load() }

// Close asks TradingView to release the session and closes the Updates
// channel. Idempotent; safe to call from any goroutine.
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

// handle runs on the dispatcher goroutine.
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

// handleQSD processes a quote-session-data packet.
//
// Wire shape: [sessionID, {"n": symbolKey, "s": "ok|error", "v": {...}}].
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
	// quote_error carries per-symbol context. Route it through the
	// priority path so slow consumers still see it.
	sym := ""
	if len(env.Params) >= 2 {
		var key string
		if err := json.Unmarshal(env.Params[1], &key); err == nil {
			sym = parseSymbolFromKey(key)
		}
	}
	s.priorityEmit(QuoteError{Symbol: sym, Err: fmt.Errorf("%w: %v", ErrQuoteServer, env.Params)})
}

// emit pushes a data update with drop-oldest backpressure. Each eviction
// increments the drop counter so callers can observe sustained pressure.
func (s *Session) emit(u Update) {
	select {
	case s.updates <- u:
		return
	default:
	}
	// Channel is full. Evict the oldest entry and try again.
	select {
	case <-s.updates:
		s.dropped.Add(1)
	default:
	}
	select {
	case s.updates <- u:
	default:
		// Lost a race with another writer or with Close.
		s.dropped.Add(1)
	}
}

// priorityEmit delivers an error or completed update. It still respects
// the channel's capacity but evicts a data update to make room rather
// than dropping the priority event itself.
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

// symbolKey builds TradingView's wire-format symbol reference, e.g.
// ={"session":"regular","symbol":"BINANCE:BTCUSDT"}.
func symbolKey(symbol string, kind SessionKind) string {
	b, _ := json.Marshal(struct {
		Session string `json:"session"`
		Symbol  string `json:"symbol"`
	}{Session: string(kind), Symbol: symbol})
	return "=" + string(b)
}

// parseSymbolFromKey extracts the symbol from a wire key, falling back to
// the raw key if it can't be decoded.
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
