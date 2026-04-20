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

// Bridge is the subset of the Client the chart session consumes.
type Bridge interface {
	Send(method string, params ...any) error
	Register(sessionID string, h func(env protocol.Envelope))
	Unregister(sessionID string)
}

// Session hosts one chart. A Client may open many; each shows one market
// at a time (SetMarket swaps the current market in place).
type Session struct {
	id     string
	bridge Bridge
	cfg    *config

	updates chan Update
	done    chan struct{}
	closeCh chan struct{}

	mu             sync.Mutex
	info           MarketInfo
	seriesCreated  bool
	currentSerIdx  int
	lastMarket     *marketConfig
	lastMarketSym  string
	lastTimeframe  string
	lastRange      int

	dropped   atomic.Uint64
	closeOnce sync.Once
}

// New creates a chart session on bridge. Typically called via Client.NewChartSession.
func New(bridge Bridge, opts ...Option) (*Session, error) {
	cfg := &config{bufferSize: 64, timezone: "Etc/UTC"}
	for _, o := range opts {
		o(cfg)
	}

	s := &Session{
		id:      protocol.GenSessionID("cs"),
		bridge:  bridge,
		cfg:     cfg,
		updates: make(chan Update, cfg.bufferSize),
		done:    make(chan struct{}),
		closeCh: make(chan struct{}),
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
	return s, nil
}

// SetMarket resolves a symbol and kicks off streaming its candles.
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

	s.mu.Lock()
	s.currentSerIdx++
	serIdx := s.currentSerIdx
	s.lastMarket = mc
	s.lastMarketSym = symbol
	s.lastTimeframe = mc.timeframe
	s.lastRange = mc.numCandles
	s.mu.Unlock()

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
	if err := s.bridge.Send("resolve_symbol", s.id, fmt.Sprintf("ser_%d", serIdx), symKey); err != nil {
		return err
	}
	return s.setSeriesLocked(mc.timeframe, mc.numCandles, mc.to, serIdx)
}

// SetSeries changes the timeframe or candle count without re-resolving the
// symbol. Must follow a SetMarket.
func (s *Session) SetSeries(timeframe string, opts ...MarketOption) error {
	mc := &marketConfig{timeframe: timeframe, numCandles: 100}
	for _, o := range opts {
		o(mc)
	}

	s.mu.Lock()
	serIdx := s.currentSerIdx
	s.lastTimeframe = mc.timeframe
	s.lastRange = mc.numCandles
	s.mu.Unlock()
	if serIdx == 0 {
		return errors.New("chart: SetMarket must be called before SetSeries")
	}
	return s.setSeriesLocked(mc.timeframe, mc.numCandles, mc.to, serIdx)
}

func (s *Session) setSeriesLocked(timeframe string, numCandles int, to int64, serIdx int) error {
	method := "create_series"
	s.mu.Lock()
	if s.seriesCreated {
		method = "modify_series"
	}
	s.seriesCreated = true
	s.mu.Unlock()

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

// RequestMore fetches n additional earlier candles for the current series.
func (s *Session) RequestMore(n int) error {
	if n <= 0 {
		return nil
	}
	return s.bridge.Send("request_more_data", s.id, "$prices", n)
}

// SetTimezone switches the chart timezone.
func (s *Session) SetTimezone(tz string) error {
	return s.bridge.Send("switch_timezone", s.id, tz)
}

// Info returns a snapshot of the most recently resolved market metadata.
func (s *Session) Info() MarketInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info
}

// Updates returns the event stream.
func (s *Session) Updates() <-chan Update { return s.updates }

// Done fires when the session has fully torn down.
func (s *Session) Done() <-chan struct{} { return s.done }

// DroppedUpdates counts data updates dropped under backpressure.
func (s *Session) DroppedUpdates() uint64 { return s.dropped.Load() }

// Close deletes the chart session and releases resources. Idempotent.
func (s *Session) Close() error {
	var firstErr error
	s.closeOnce.Do(func() {
		close(s.closeCh)
		if err := s.bridge.Send("chart_delete_session", s.id); err != nil {
			firstErr = err
		}
		s.bridge.Unregister(s.id)
		close(s.updates)
		close(s.done)
	})
	return firstErr
}

// handle dispatches one envelope.
func (s *Session) handle(env protocol.Envelope) {
	select {
	case <-s.closeCh:
		return
	default:
	}

	switch env.Method {
	case "symbol_resolved":
		s.handleSymbolResolved(env)
	case "timescale_update", "du":
		s.handleTimescale(env)
	case "symbol_error":
		s.priorityEmit(ChartError{Kind: "symbol_error", Err: serverErr(env)})
	case "series_error":
		s.priorityEmit(ChartError{Kind: "series_error", Err: serverErr(env)})
	case "critical_error":
		s.priorityEmit(ChartError{Kind: "critical_error", Err: serverErr(env)})
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
	s.priorityEmit(SymbolResolved{Info: info})
}

// handleTimescale decodes a timescale_update / du payload.
// Shape: [sessionID, {"$prices": {"s": [{"i": idx, "v": [t,o,h,l,c,vol]}, ...]}}].
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

func (s *Session) emit(u Update) {
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
