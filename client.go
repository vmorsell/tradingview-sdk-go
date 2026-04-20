package tradingview

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"

	"github.com/vmorsell/tradingview-sdk-go/internal/protocol"
	"github.com/vmorsell/tradingview-sdk-go/internal/wire"
)

// Client is a connected TradingView WebSocket. Create one with Connect.
// It multiplexes quote and chart sessions over a single connection.
//
// A Client is goroutine-safe. Close is idempotent.
type Client struct {
	cfg      *config
	pump     *wire.Pump
	registry *wire.Registry
	logger   *slog.Logger
}

// Connect dials TradingView's data WebSocket and completes the initial
// set_auth_token exchange. The returned Client is ready to host sessions.
//
// If ctx is cancelled, Connect returns ctx.Err(). If ctx is cancelled after
// Connect returns, the client is torn down.
func Connect(ctx context.Context, opts ...Option) (*Client, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		o(cfg)
	}

	// Resolve auth token before dialing. Anonymous users send a literal
	// "unauthorized_user_token"; authenticated users exchange their
	// session cookie for a short-lived auth_token via HTTP.
	authToken := "unauthorized_user_token"
	if cfg.sessionID != "" {
		user, err := getUser(ctx, cfg.httpClient, cfg.location, cfg.sessionID, cfg.sessionIDSign, cfg.userAgent)
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

	// Tie client lifecycle to context: if caller cancels ctx after Connect,
	// tear down. This is in addition to an explicit Close.
	go func() {
		select {
		case <-ctx.Done():
			_ = c.Close()
		case <-p.Done():
		}
	}()

	return c, nil
}

// Close initiates graceful shutdown and blocks until the pump goroutines exit.
// Idempotent.
func (c *Client) Close() error { return c.pump.Close() }

// Done closes when the underlying connection has fully torn down.
func (c *Client) Done() <-chan struct{} { return c.pump.Done() }

// Err returns the first fatal connection error observed, if any. Non-nil
// only after Done has fired.
func (c *Client) Err() error { return c.pump.Err() }

// Send frames and enqueues a method call. Used by sub-packages via the
// clientBridge interface; exported here so the sub-packages can satisfy
// their own local interface without importing internal/wire.
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

// Register installs a handler for a session id. Used by sub-packages.
func (c *Client) Register(sessionID string, h func(protocol.Envelope)) {
	c.registry.Register(sessionID, h)
}

// Unregister removes a session handler. Used by sub-packages.
func (c *Client) Unregister(sessionID string) { c.registry.Unregister(sessionID) }

// cloneHeader returns a shallow copy of h (so callers can't mutate after Connect).
func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		vv := make([]string, len(v))
		copy(vv, v)
		out[k] = vv
	}
	return out
}

// getUser is the HTTP-side of auth: sessionid cookie → auth_token via
// scraping the TradingView home page HTML. Full public API lives in http.go
// (M3); this is the internal shim so Connect works in M2.
func getUser(ctx context.Context, client *http.Client, location, session, signature, userAgent string) (User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return User{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Cookie", authCookie(session, signature))

	// Follow up to 5 redirects manually; TradingView bounces through a few.
	var body []byte
	const maxRedirects = 5
	for range maxRedirects + 1 {
		resp, err := client.Do(req)
		if err != nil {
			return User{}, err
		}
		body = nil
		if resp.Body != nil {
			defer resp.Body.Close()
			const cap = 2 << 20 // 2 MiB is plenty for the home page HTML
			buf := make([]byte, 0, 8192)
			chunk := make([]byte, 8192)
			for len(buf) < cap {
				n, err := resp.Body.Read(chunk)
				if n > 0 {
					buf = append(buf, chunk[:n]...)
				}
				if err != nil {
					break
				}
			}
			body = buf
		}
		if loc := resp.Header.Get("Location"); loc != "" && loc != req.URL.String() {
			next, err := req.URL.Parse(loc)
			if err != nil {
				return User{}, err
			}
			req, err = http.NewRequestWithContext(ctx, http.MethodGet, next.String(), nil)
			if err != nil {
				return User{}, err
			}
			req.Header.Set("User-Agent", userAgent)
			req.Header.Set("Cookie", authCookie(session, signature))
			continue
		}
		break
	}

	s := string(body)
	if !containsAuthToken(s) {
		return User{}, fmt.Errorf("auth token not found in response (wrong or expired session?)")
	}
	return User{
		ID:             parseInt(reID.FindStringSubmatch(s)),
		Username:       firstGroup(reUsername.FindStringSubmatch(s)),
		AuthToken:      firstGroup(reAuthToken.FindStringSubmatch(s)),
		SessionHash:    firstGroup(reSessionHash.FindStringSubmatch(s)),
		PrivateChannel: firstGroup(rePrivateChan.FindStringSubmatch(s)),
	}, nil
}

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

func authCookie(session, signature string) string {
	if session == "" {
		return ""
	}
	if signature == "" {
		return "sessionid=" + session
	}
	return "sessionid=" + session + ";sessionid_sign=" + signature
}
