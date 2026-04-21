package tradingview

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"

	"github.com/vmorsell/tradingview-sdk-go/chart"
	"github.com/vmorsell/tradingview-sdk-go/internal/protocol"
	"github.com/vmorsell/tradingview-sdk-go/internal/wire"
	"github.com/vmorsell/tradingview-sdk-go/quote"
)

// Client is a live TradingView WebSocket connection. Obtain one with
// Connect. Multiple quote and chart sessions can be opened on a single
// Client; the connection itself is shared.
//
// Clients are safe for concurrent use. Close is idempotent.
type Client struct {
	cfg      *config
	pump     *wire.Pump
	registry *wire.Registry
	logger   *slog.Logger
}

// Connect dials TradingView's data WebSocket and sends the initial
// set_auth_token frame. The returned Client is ready to host sessions.
//
// If ctx is cancelled before the handshake finishes Connect returns
// ctx.Err. If ctx is cancelled after Connect returns, the Client is torn
// down in the background.
func Connect(ctx context.Context, opts ...Option) (*Client, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	// Resolve the auth token before dialing. Anonymous users send a
	// literal "unauthorized_user_token"; authenticated users trade
	// their session cookie for a short-lived auth_token via HTTP.
	authToken := "unauthorized_user_token"
	if cfg.sessionID != "" {
		user, err := GetUser(ctx, cfg.sessionID, cfg.sessionIDSign,
			WithHTTPOptionClient(cfg.httpClient),
			WithHTTPOptionLocation(cfg.location),
			WithHTTPOptionUserAgent(cfg.userAgent),
		)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrAuth, err)
		}
		authToken = user.AuthToken
	}

	dialer := cfg.dialer
	header := cloneHeader(cfg.headers)
	header.Set("User-Agent", cfg.userAgent)
	header.Set("Origin", "https://www.tradingview.com")

	wsURL := cfg.urlOverride
	if wsURL == "" {
		wsURL = (&url.URL{
			Scheme:   "wss",
			Host:     string(cfg.server) + ".tradingview.com",
			Path:     "/socket.io/websocket",
			RawQuery: "from=chart&type=chart",
		}).String()
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("tradingview: dial %s: %w", wsURL, err)
	}

	reg := wire.NewRegistry()
	p := wire.NewPump(wire.Options{
		Conn:        conn,
		Logger:      cfg.logger,
		Registry:    reg,
		Unsolicited: func(env protocol.Envelope) { cfg.logger.Debug("unsolicited", "method", env.Method) },
	})
	p.Start()

	c := &Client{
		cfg:      cfg,
		pump:     p,
		registry: reg,
		logger:   cfg.logger,
	}

	auth, err := protocol.Encode("set_auth_token", authToken)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("tradingview: encode auth: %w", err)
	}
	if err := p.Send(auth); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("tradingview: send auth: %w", err)
	}

	// Tear down if the caller cancels ctx after Connect returned.
	// Explicit Close still works the same way.
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-p.Done():
		}
	}()

	return c, nil
}

// Close starts graceful shutdown and blocks until the pump goroutines
// exit. Idempotent.
func (c *Client) Close() error { return c.pump.Close() }

// Done closes once the underlying connection has fully torn down.
func (c *Client) Done() <-chan struct{} { return c.pump.Done() }

// Err returns the first fatal connection error, if any. Populated only
// after Done has fired.
func (c *Client) Err() error { return c.pump.Err() }

// Send frames and enqueues a method call. Used by sub-packages via the
// structural Bridge interface they each declare.
func (c *Client) Send(method string, params ...any) error {
	frame, err := protocol.Encode(method, params...)
	if err != nil {
		return fmt.Errorf("tradingview: encode %s: %w", method, err)
	}
	if err := c.pump.Send(frame); err != nil {
		return fmt.Errorf("%w: %v", ErrClosed, err)
	}
	return nil
}

// Register installs a handler for a session id. Sub-packages use this
// to route envelopes back to their own Session types.
func (c *Client) Register(sessionID string, h func(protocol.Envelope)) {
	c.registry.Register(sessionID, h)
}

// Unregister removes a session handler.
func (c *Client) Unregister(sessionID string) { c.registry.Unregister(sessionID) }

// NewQuoteSession opens a quote session on this client.
func (c *Client) NewQuoteSession(opts ...quote.Option) (*quote.Session, error) {
	return quote.New(c, opts...)
}

// NewChartSession opens a chart session on this client.
func (c *Client) NewChartSession(opts ...chart.Option) (*chart.Session, error) {
	return chart.New(c, opts...)
}

// cloneHeader returns a deep copy of h so callers can't mutate the
// headers this Client will use on reconnect (though reconnection isn't
// yet implemented).
func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		vv := make([]string, len(v))
		copy(vv, v)
		out[k] = vv
	}
	return out
}

// Compiled regexes used by the HTTP-side auth scrape. Kept at package
// scope so they are compiled once on program start.
var (
	reID          = regexp.MustCompile(`"id":(\d{1,10})`)
	reUsername    = regexp.MustCompile(`"username":"(.*?)"`)
	reAuthToken   = regexp.MustCompile(`"auth_token":"(.*?)"`)
	reSessionHash = regexp.MustCompile(`"session_hash":"(.*?)"`)
	rePrivateChan = regexp.MustCompile(`"private_channel":"(.*?)"`)
)

func containsAuthToken(s string) bool { return reAuthToken.MatchString(s) }

func firstGroup(m []string) string {
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func parseInt(m []string) int64 {
	if len(m) < 2 {
		return 0
	}
	var n int64
	for _, r := range m[1] {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}
