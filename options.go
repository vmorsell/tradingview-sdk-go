package tradingview

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/websocket"
)

// Option configures Connect.
type Option func(*config)

type config struct {
	server        Server
	sessionID     string
	sessionIDSign string
	location      string
	userAgent     string
	headers       http.Header
	logger        *slog.Logger
	dialer        *websocket.Dialer
	httpClient    *http.Client
	// urlOverride bypasses URL construction; test hook, not part of the
	// public API.
	urlOverride string
}

func defaultConfig() *config {
	return &config{
		server:    ServerData,
		location:  "https://www.tradingview.com/",
		userAgent: "Mozilla/5.0 (compatible; tradingview-sdk-go/0.1)",
		headers:   http.Header{},
		logger:    slog.New(slog.NewTextHandler(discardWriter{}, nil)),
		dialer:    websocket.DefaultDialer,
		httpClient: &http.Client{
			// TradingView auth page follows a chain of redirects we parse manually.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// WithSessionCookies authenticates using a user's sessionid / sessionid_sign
// cookies scraped from a browser session. Both values are required for
// paid accounts; signature may be empty for older free accounts.
func WithSessionCookies(sessionID, signature string) Option {
	return func(c *config) {
		c.sessionID = sessionID
		c.sessionIDSign = signature
	}
}

// WithServer selects which TradingView data server to connect to.
func WithServer(s Server) Option { return func(c *config) { c.server = s } }

// WithLocation overrides the auth-exchange base URL (e.g. a regional mirror).
func WithLocation(url string) Option { return func(c *config) { c.location = url } }

// WithUserAgent sets the User-Agent header on the initial WS handshake and
// on HTTP requests made during auth.
func WithUserAgent(ua string) Option { return func(c *config) { c.userAgent = ua } }

// WithHTTPHeader adds a header sent on the WebSocket handshake. Repeatable.
func WithHTTPHeader(key, value string) Option {
	return func(c *config) { c.headers.Add(key, value) }
}

// WithLogger installs a structured logger. Defaults to a no-op.
func WithLogger(l *slog.Logger) Option { return func(c *config) { c.logger = l } }

// WithDialer overrides the gorilla websocket dialer. Primarily a test hook
// to inject a custom NetDialContext pointing at an httptest server.
func WithDialer(d *websocket.Dialer) Option { return func(c *config) { c.dialer = d } }

// WithHTTPClient overrides the HTTP client used by the initial auth exchange.
func WithHTTPClient(h *http.Client) Option { return func(c *config) { c.httpClient = h } }

// withURL is an unexported test hook that bypasses URL construction.
func withURL(u string) Option { return func(c *config) { c.urlOverride = u } }

// discardWriter satisfies io.Writer for the default no-op slog handler.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
