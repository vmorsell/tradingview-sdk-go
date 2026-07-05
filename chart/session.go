package chart

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/vmorsell/tradingview-sdk-go/internal/protocol"
)

// Bridge is the subset of the Client a chart session consumes. Defined
// locally to avoid an import cycle with the root package.
type Bridge interface {
	Send(method string, params ...any) error
	Register(sessionID string, h func(env protocol.Envelope))
	Unregister(sessionID string)
	Done() <-chan struct{}
}

// Session hosts one chart. A Client may open many of them; each shows
// one market at a time (SetMarket swaps the current market in place).
type Session struct {
	id     string
	bridge Bridge

	updates chan Update
	done    chan struct{}

	mu            sync.Mutex
	closed        bool // guards updates against send-after-close
	info          MarketInfo
	seriesCreated bool
	currentSerIdx int

	dropped   atomic.Uint64
	closeOnce sync.Once
}

// New creates a chart session on bridge. Most callers go through
// Client.NewChartSession instead.
func New(bridge Bridge, opts ...Option) (*Session, error) {
	cfg := &config{bufferSize: 64, timezone: "Etc/UTC"}
	for _, o := range opts {
		o(cfg)
	}

	s := &Session{
		id:      protocol.GenSessionID("cs"),
		bridge:  bridge,
		updates: make(chan Update, cfg.bufferSize),
		done:    make(chan struct{}),
	}
	bridge.Register(s.id, s.handle)

	if err := bridge.Send("chart_create_session", s.id, ""); err != nil {
		bridge.Unregister(s.id)
		return nil, err
	}
	if cfg.timezone != "" {
		if err := bridge.Send("switch_timezone", s.id, cfg.timezone); err != nil {
			bridge.Unregister(s.id)
			return nil, err
		}
	}
	go s.watchBridge()
	return s, nil
}

// watchBridge tears the session down when the owning client dies, so
// Updates closes and Done fires even if nobody calls Close explicitly.
func (s *Session) watchBridge() {
	select {
	case <-s.bridge.Done():
		_ = s.shutdown(false)
	case <-s.done:
	}
}

// SetMarket resolves a symbol and begins streaming its candles.
// Subsequent calls swap the market in place.
func (s *Session) SetMarket(symbol string, opts ...MarketOption) error {
	mc := &marketConfig{
		timeframe:  "240",
		numCandles: 100,
		adjustment: AdjustSplits,
	}
	for _, o := range opts {
		o(mc)
	}

	symInit := map[string]any{
		"symbol":     symbol,
		"adjustment": string(mc.adjustment),
	}
	if mc.session != "" {
		symInit["session"] = string(mc.session)
	}
	if mc.currency != "" {
		symInit["currency-id"] = mc.currency
	}

	symKey := "="
	if b, err := json.Marshal(symInit); err == nil {
		symKey += string(b)
	}

	// Hold the lock across both sends so concurrent SetMarket calls
	// can't interleave resolve/create frames on the wire.
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentSerIdx++
	serIdx := s.currentSerIdx
	if err := s.bridge.Send("resolve_symbol", s.id, fmt.Sprintf("ser_%d", serIdx), symKey); err != nil {
		return err
	}
	return s.sendSeriesLocked(mc.timeframe, mc.numCandles, mc.to, serIdx)
}

// SetSeries changes the timeframe or candle count without re-resolving
// the symbol. It is invalid to call before SetMarket.
func (s *Session) SetSeries(timeframe string, opts ...MarketOption) error {
	mc := &marketConfig{timeframe: timeframe, numCandles: 100}
	for _, o := range opts {
		o(mc)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentSerIdx == 0 {
		return errors.New("chart: SetMarket must be called before SetSeries")
	}
	return s.sendSeriesLocked(mc.timeframe, mc.numCandles, mc.to, s.currentSerIdx)
}

// sendSeriesLocked sends a create_series or modify_series frame. The
// caller must hold s.mu.
func (s *Session) sendSeriesLocked(timeframe string, numCandles int, to int64, serIdx int) error {
	method := "create_series"
	if s.seriesCreated {
		method = "modify_series"
	}
	s.seriesCreated = true

	var rangeArg any = numCandles
	if to > 0 {
		rangeArg = []any{"bar_count", to, numCandles}
	}
	if method == "modify_series" {
		rangeArg = ""
	}

	return s.bridge.Send(method,
		s.id,
		"$prices",
		"s1",
		fmt.Sprintf("ser_%d", serIdx),
		timeframe,
		rangeArg,
	)
}

// RequestMore fetches n additional earlier candles for the current
// series. A no-op for n <= 0.
func (s *Session) RequestMore(n int) error {
	if n <= 0 {
		return nil
	}
	return s.bridge.Send("request_more_data", s.id, "$prices", n)
}

// SetTimezone switches the chart timezone. See
// https://www.tradingview.com/charting-library-docs/latest/ui_elements/timezones
// for the accepted values.
func (s *Session) SetTimezone(tz string) error {
	return s.bridge.Send("switch_timezone", s.id, tz)
}

// Info returns a snapshot of the most recently resolved market metadata.
// Safe to call concurrently.
func (s *Session) Info() MarketInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// Updates returns the event stream for this session. The channel is
// closed when the session is closed or the owning client shuts down
// (including on connection loss).
func (s *Session) Updates() <-chan Update { return s.updates }

// Done fires when the session has fully torn down.
func (s *Session) Done() <-chan struct{} { return s.done }

// DroppedUpdates reports how many pending updates have been evicted
// under backpressure since the session was created.
func (s *Session) DroppedUpdates() uint64 { return s.dropped.Load() }

// Close asks TradingView to release the session and closes the Updates
// channel. Idempotent.
func (s *Session) Close() error { return s.shutdown(true) }

// shutdown tears the session down exactly once. notifyServer is false
// when the client itself is dying and the connection is unusable.
func (s *Session) shutdown(notifyServer bool) error {
	var sendErr error
	s.closeOnce.Do(func() {
		if notifyServer {
			sendErr = s.bridge.Send("chart_delete_session", s.id)
		}
		s.bridge.Unregister(s.id)
		s.mu.Lock()
		s.closed = true
		close(s.updates)
		s.mu.Unlock()
		close(s.done)
	})
	return sendErr
}

// handle dispatches one envelope.
func (s *Session) handle(env protocol.Envelope) {
	switch env.Method {
	case "symbol_resolved":
		s.handleSymbolResolved(env)
	case "timescale_update", "du":
		s.handleTimescale(env)
	case "symbol_error":
		s.emit(ChartError{Kind: "symbol_error", Err: serverErr(env)})
	case "series_error":
		s.emit(ChartError{Kind: "series_error", Err: serverErr(env)})
	case "critical_error":
		s.emit(ChartError{Kind: "critical_error", Err: serverErr(env)})
	}
}

func (s *Session) handleSymbolResolved(env protocol.Envelope) {
	if len(env.Params) < 3 {
		return
	}
	var seriesID string
	_ = json.Unmarshal(env.Params[1], &seriesID)

	var raw struct {
		Name           string `json:"name"`
		FullName       string `json:"full_name"`
		Description    string `json:"description"`
		Exchange       string `json:"exchange"`
		ListedExchange string `json:"listed_exchange"`
		CurrencyID     string `json:"currency_id"`
		Type           string `json:"type"`
		Timezone       string `json:"timezone"`
		Session        string `json:"session"`
		SubsessionID   string `json:"subsession_id"`
		Pricescale     int    `json:"pricescale"`
		Minmov         int    `json:"minmov"`
		HasIntraday    bool   `json:"has_intraday"`
	}
	if err := json.Unmarshal(env.Params[2], &raw); err != nil {
		return
	}
	info := MarketInfo{
		SeriesID:       seriesID,
		Name:           raw.Name,
		FullName:       raw.FullName,
		Description:    raw.Description,
		Exchange:       raw.Exchange,
		ListedExchange: raw.ListedExchange,
		Currency:       raw.CurrencyID,
		Type:           raw.Type,
		Timezone:       raw.Timezone,
		Session:        raw.Session,
		SubsessionID:   raw.SubsessionID,
		Pricescale:     raw.Pricescale,
		Minmov:         raw.Minmov,
		HasIntraday:    raw.HasIntraday,
	}
	s.mu.Lock()
	s.info = info
	s.mu.Unlock()
	s.emit(SymbolResolved{Info: info})
}

// handleTimescale decodes a timescale_update or du payload.
//
// Wire shape: [sessionID, {"$prices": {"s": [{"i": idx, "v": [t,o,h,l,c,vol]}, ...]}}].
func (s *Session) handleTimescale(env protocol.Envelope) {
	if len(env.Params) < 2 {
		return
	}
	var payload map[string]struct {
		S []struct {
			I int       `json:"i"`
			V []float64 `json:"v"`
		} `json:"s"`
	}
	if err := json.Unmarshal(env.Params[1], &payload); err != nil {
		return
	}
	prices, ok := payload["$prices"]
	if !ok {
		return
	}
	changed := make([]Candle, 0, len(prices.S))
	for _, pt := range prices.S {
		if c, ok := candleFromWire(pt.V); ok {
			changed = append(changed, c)
		}
	}
	if len(changed) == 0 {
		return
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Time.Before(changed[j].Time) })
	s.emit(Candles{Changed: changed})
}

func serverErr(env protocol.Envelope) error {
	if len(env.Params) == 0 {
		return fmt.Errorf("server error")
	}
	parts := make([]string, 0, len(env.Params))
	for _, p := range env.Params {
		parts = append(parts, string(p))
	}
	return fmt.Errorf("%v", parts)
}

// emit delivers u with drop-oldest backpressure: when the buffer is
// full, the oldest pending update is evicted (counted in DroppedUpdates)
// to make room. No-op once the session has shut down.
func (s *Session) emit(u Update) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
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
		s.dropped.Add(1)
	}
}
