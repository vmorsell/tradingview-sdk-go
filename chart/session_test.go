package chart

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vmorsell/tradingview-sdk-go/internal/protocol"
)

type fakeBridge struct {
	mu      sync.Mutex
	sent    []sentFrame
	handler func(protocol.Envelope)
	id      string
}

type sentFrame struct {
	method string
	params []any
}

func (b *fakeBridge) Send(method string, params ...any) error {
	b.mu.Lock()
	b.sent = append(b.sent, sentFrame{method: method, params: params})
	b.mu.Unlock()
	return nil
}

func (b *fakeBridge) Register(id string, h func(protocol.Envelope)) {
	b.id = id
	b.handler = h
}
func (b *fakeBridge) Unregister(id string) {
	if b.id == id {
		b.handler = nil
	}
}

func (b *fakeBridge) sentMethods() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.sent))
	for i, f := range b.sent {
		out[i] = f.method
	}
	return out
}

func (b *fakeBridge) drive(t *testing.T, method string, params ...any) {
	t.Helper()
	raws := make([]json.RawMessage, len(params))
	for i, p := range params {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		raws[i] = raw
	}
	if b.handler == nil {
		t.Fatal("no handler")
	}
	b.handler(protocol.Envelope{Method: method, Params: raws})
}

func TestSessionCreateSendsFrames(t *testing.T) {
	b := &fakeBridge{}
	s, err := New(b)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := b.sentMethods()
	want := []string{"chart_create_session", "switch_timezone"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSetMarketThenSetSeriesModifies(t *testing.T) {
	b := &fakeBridge{}
	s, _ := New(b)
	defer s.Close()

	if err := s.SetMarket("BINANCE:BTCUSDT", WithTimeframe("D")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSeries("15"); err != nil {
		t.Fatal(err)
	}
	// Expect: create_session, switch_timezone, resolve_symbol, create_series, modify_series
	got := b.sentMethods()
	want := []string{
		"chart_create_session",
		"switch_timezone",
		"resolve_symbol",
		"create_series",
		"modify_series",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSymbolResolvedPopulatesInfo(t *testing.T) {
	b := &fakeBridge{}
	s, _ := New(b)
	defer s.Close()
	b.drive(t, "symbol_resolved", s.id, "ser_1", map[string]any{
		"name":         "BTCUSDT",
		"full_name":    "BINANCE:BTCUSDT",
		"description":  "Bitcoin / Tether",
		"exchange":     "BINANCE",
		"currency_id":  "USDT",
		"type":         "crypto",
		"pricescale":   100,
		"minmov":       1,
		"has_intraday": true,
	})
	u := recv(t, s)
	sr, ok := u.(SymbolResolved)
	if !ok {
		t.Fatalf("want SymbolResolved got %T", u)
	}
	if sr.Info.FullName != "BINANCE:BTCUSDT" {
		t.Errorf("FullName %q", sr.Info.FullName)
	}
	if s.Info().Pricescale != 100 {
		t.Errorf("Info().Pricescale = %d", s.Info().Pricescale)
	}
}

func TestTimescaleUpdateDecodesCandles(t *testing.T) {
	b := &fakeBridge{}
	s, _ := New(b)
	defer s.Close()
	b.drive(t, "timescale_update", s.id, map[string]any{
		"$prices": map[string]any{
			"s": []map[string]any{
				{"i": 0, "v": []float64{1700000000, 100, 110, 95, 105, 1000}},
				{"i": 1, "v": []float64{1700003600, 105, 108, 102, 107, 500}},
			},
		},
	})
	u := recv(t, s)
	c, ok := u.(Candles)
	if !ok {
		t.Fatalf("want Candles got %T", u)
	}
	if len(c.Changed) != 2 {
		t.Fatalf("want 2 candles, got %d", len(c.Changed))
	}
	if c.Changed[0].Close != 105 || c.Changed[1].Close != 107 {
		t.Errorf("closes: %+v", c.Changed)
	}
	if !c.Changed[0].Time.Before(c.Changed[1].Time) {
		t.Errorf("expected time-ascending order")
	}
	_ = time.Now
}

func TestChartErrorsAreEmitted(t *testing.T) {
	b := &fakeBridge{}
	s, _ := New(b)
	defer s.Close()
	b.drive(t, "symbol_error", s.id, "ser_1", "invalid")
	u := recv(t, s)
	e, ok := u.(ChartError)
	if !ok {
		t.Fatalf("want ChartError got %T", u)
	}
	if e.Kind != "symbol_error" {
		t.Errorf("Kind: %q", e.Kind)
	}
}

func TestCloseIdempotentAndClosesUpdates(t *testing.T) {
	b := &fakeBridge{}
	s, _ := New(b)
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	select {
	case _, ok := <-s.Updates():
		if ok {
			t.Fatal("want closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("Updates not closed")
	}
}

func recv(t *testing.T, s *Session) Update {
	t.Helper()
	select {
	case u := <-s.Updates():
		return u
	case <-time.After(time.Second):
		t.Fatal("timeout")
		return nil
	}
}
