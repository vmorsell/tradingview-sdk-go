package tradingview

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/vmorsell/tradingview-sdk-go/internal/protocol"
)

// fakeServer serves a single WebSocket connection backed by an
// httptest.Server + gorilla/websocket Upgrader. It captures every frame the
// client sends and lets the test push server-originated frames.
type fakeServer struct {
	srv     *httptest.Server
	connCh  chan *websocket.Conn
	sent    chan protocol.Envelope // frames received from client
	stopped chan struct{}
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{
		connCh:  make(chan *websocket.Conn, 1),
		sent:    make(chan protocol.Envelope, 64),
		stopped: make(chan struct{}),
	}
	up := websocket.Upgrader{
		CheckOrigin: func(*http.Request) bool { return true },
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		f.connCh <- c
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				close(f.stopped)
				return
			}
			envs, err := protocol.Decode(data)
			if err != nil {
				t.Errorf("server decode: %v", err)
				continue
			}
			for _, e := range envs {
				f.sent <- e
			}
		}
	}))
	t.Cleanup(func() { f.srv.Close() })
	return f
}

// dialer returns a websocket.Dialer that ignores the requested wss:// URL
// and instead connects to the fake httptest server.
func (f *fakeServer) dialer() *websocket.Dialer {
	wsURL := strings.Replace(f.srv.URL, "http://", "ws://", 1)
	parsed, _ := url.Parse(wsURL)
	return &websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{}
			return d.DialContext(ctx, "tcp", parsed.Host)
		},
		// Rewrite scheme so gorilla speaks ws:// to the test server but the
		// caller can still pass a wss://data.tradingview.com URL.
		TLSClientConfig: nil,
	}
}

// acceptConn blocks up to 2s for the server-side conn.
func (f *fakeServer) acceptConn(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case c := <-f.connCh:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("server never received connection")
		return nil
	}
}

// expectFrame reads one envelope from the client, asserting the method.
func (f *fakeServer) expectFrame(t *testing.T, method string) protocol.Envelope {
	t.Helper()
	select {
	case e := <-f.sent:
		if e.Method != method {
			t.Fatalf("want method %q, got %q", method, e.Method)
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %q", method)
		return protocol.Envelope{}
	}
}

func TestConnectAnonymousSendsAuth(t *testing.T) {
	// The WithDialer-only path still goes through gorilla's handshake logic,
	// but we override NetDialContext so it hits our httptest server. We also
	// override the Host rewriting by passing a custom dialer that resolves
	// data.tradingview.com to 127.0.0.1.
	f := newFakeServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	client, err := Connect(ctx,
		WithDialer(f.dialer()),
		withURL(strings.Replace(f.srv.URL, "http://", "ws://", 1)),
	)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	_ = f.acceptConn(t)
	env := f.expectFrame(t, "set_auth_token")
	if env.SessionID() != "unauthorized_user_token" {
		t.Fatalf("want unauthorized_user_token, got %q", env.SessionID())
	}
}

func TestHeartbeatEcho(t *testing.T) {
	f := newFakeServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	client, err := Connect(ctx,
		WithDialer(f.dialer()),
		withURL(strings.Replace(f.srv.URL, "http://", "ws://", 1)),
	)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer client.Close()

	c := f.acceptConn(t)
	// Swallow the auth frame the client sends first.
	f.expectFrame(t, "set_auth_token")

	// Server sends ~h~42; client must echo it back verbatim.
	// Length is 5 bytes ("~h~42"), not 4.
	if err := c.WriteMessage(websocket.TextMessage, []byte("~m~5~m~~h~42")); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-f.sent:
			if e.IsPing() && e.Ping == 42 {
				return
			}
			// Unexpected frame; keep waiting.
		case <-deadline:
			t.Fatal("heartbeat not echoed")
		}
	}
}

func TestCloseIsIdempotentAndDoneFires(t *testing.T) {
	f := newFakeServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	client, err := Connect(ctx,
		WithDialer(f.dialer()),
		withURL(strings.Replace(f.srv.URL, "http://", "ws://", 1)),
	)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	_ = f.acceptConn(t)
	f.expectFrame(t, "set_auth_token")

	var wg sync.WaitGroup
	wg.Go(func() {
		<-client.Done()
	})

	if err := client.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	wg.Wait()
}
