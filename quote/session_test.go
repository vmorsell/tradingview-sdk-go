package quote

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/vmorsell/tradingview-sdk-go/internal/protocol"
)

// fakeBridge captures sent frames and lets tests drive the registered handler.
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
	env := protocol.Envelope{Method: method, Params: raws}
	if b.handler == nil {
		t.Fatal("no handler registered")
	}
	b.handler(env)
}

func TestSessionCreateEmitsProtocolFrames(t *testing.T) {
	b := &fakeBridge{}
	s, err := New(b)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got := b.sentMethods()
	want := []string{"quote_create_session", "quote_set_fields"}
	if !slices.Equal(got, want) {
		t.Fatalf("sent methods: got %v want %v", got, want)
	}
}

func TestSessionAddSymbolIsIdempotent(t *testing.T) {
	b := &fakeBridge{}
	s, _ := New(b)
	defer s.Close()

	_ = s.AddSymbol("BINANCE:BTCUSDT", SessionRegular)
	_ = s.AddSymbol("BINANCE:BTCUSDT", SessionRegular)

	adds := 0
	for _, m := range b.sentMethods() {
		if m == "quote_add_symbols" {
			adds++
		}
	}
	if adds != 1 {
		t.Fatalf("want 1 add frame, got %d", adds)
	}
}

func TestSessionMergesFieldDeltas(t *testing.T) {
	b := &fakeBridge{}
	s, _ := New(b, WithFields(FieldPresetPrice))
	defer s.Close()
	_ = s.AddSymbol("FOO", SessionRegular)

	// First qsd sets lp.
	b.drive(t, "qsd", s.id, map[string]any{
		"n": symbolKey("FOO", SessionRegular),
		"s": "ok",
		"v": map[string]any{"lp": 100.0},
	})
	u := recv(t, s)
	qd, ok := u.(QuoteData)
	if !ok {
		t.Fatalf("want QuoteData, got %T", u)
	}
	if qd.Symbol != "FOO" || qd.Fields["lp"] != 100.0 {
		t.Fatalf("bad first emit: %+v", qd)
	}

	// Second qsd updates volume; lp should persist merged.
	b.drive(t, "qsd", s.id, map[string]any{
		"n": symbolKey("FOO", SessionRegular),
		"s": "ok",
		"v": map[string]any{"volume": 5.0},
	})
	u = recv(t, s)
	qd = u.(QuoteData)
	if qd.Fields["lp"] != 100.0 || qd.Fields["volume"] != 5.0 {
		t.Fatalf("merge failed: %+v", qd.Fields)
	}
}

func TestSessionEmitsCompletedAndError(t *testing.T) {
	b := &fakeBridge{}
	s, _ := New(b)
	defer s.Close()

	b.drive(t, "quote_completed", s.id, symbolKey("FOO", SessionRegular))
	u := recv(t, s)
	if _, ok := u.(QuoteCompleted); !ok {
		t.Fatalf("want QuoteCompleted, got %T", u)
	}

	b.drive(t, "qsd", s.id, map[string]any{
		"n": symbolKey("FOO", SessionRegular),
		"s": "error",
		"v": map[string]any{},
	})
	u = recv(t, s)
	if _, ok := u.(QuoteError); !ok {
		t.Fatalf("want QuoteError, got %T", u)
	}
}

func TestSessionDropOldestBackpressure(t *testing.T) {
	b := &fakeBridge{}
	// Tiny buffer so drop fires quickly.
	s, _ := New(b, WithBufferSize(2))
	defer s.Close()

	// Push 10 data updates without draining; expect drops.
	for i := range 10 {
		b.drive(t, "qsd", s.id, map[string]any{
			"n": symbolKey("FOO", SessionRegular),
			"s": "ok",
			"v": map[string]any{"lp": float64(i)},
		})
	}
	if s.DroppedUpdates() == 0 {
		t.Fatal("expected some drops with full buffer")
	}
	// Drain and confirm channel still delivers.
	drained := 0
	for drained < 2 {
		select {
		case <-s.Updates():
			drained++
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("drain stuck after %d", drained)
		}
	}
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	b := &fakeBridge{}
	s, _ := New(b)
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	// Updates channel should be closed.
	select {
	case _, ok := <-s.Updates():
		if ok {
			t.Fatal("want closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("Updates not closed after Close")
	}
}

func recv(t *testing.T, s *Session) Update {
	t.Helper()
	select {
	case u := <-s.Updates():
		return u
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for update")
		return nil
	}
}
