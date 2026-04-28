package tradingview

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
)

const (
	defaultSearchBase  = "https://symbol-search.tradingview.com"
	defaultScannerBase = "https://scanner.tradingview.com"
	// browserUA matches what the JS reference's client sends. The
	// symbol-search endpoint returns an HTML challenge page for obvious
	// bot User-Agents, so we default to a real browser string.
	browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

// HTTPOption configures a one-shot HTTP helper (GetUser, SearchSymbol, GetTA).
// Separate from Option because these helpers are useful without a connected
// Client (e.g. symbol lookup before opening the WebSocket).
type HTTPOption func(*httpConfig)

type httpConfig struct {
	client        *http.Client
	location      string
	userAgent     string
	searchBase    string
	scannerBase   string
	sessionID     string
	sessionIDSign string
}

func defaultHTTPConfig() *httpConfig {
	return &httpConfig{
		client:      &http.Client{},
		location:    "https://www.tradingview.com/",
		userAgent:   browserUA,
		searchBase:  defaultSearchBase,
		scannerBase: defaultScannerBase,
	}
}

// noRedirectClient returns a copy of the user's client with redirects
// disabled. GetUser walks the redirect chain manually because the home
// page only exposes the auth_token after the final hop.
func noRedirectClient(base *http.Client) *http.Client {
	if base == nil {
		base = &http.Client{}
	}
	c := *base
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// WithHTTPOptionClient overrides the underlying *http.Client.
func WithHTTPOptionClient(c *http.Client) HTTPOption {
	return func(h *httpConfig) { h.client = c }
}

// WithHTTPOptionLocation overrides the auth-exchange base URL used by
// GetUser (useful for regional mirrors).
func WithHTTPOptionLocation(u string) HTTPOption {
	return func(h *httpConfig) { h.location = u }
}

// WithHTTPOptionUserAgent overrides the User-Agent sent by these helpers.
func WithHTTPOptionUserAgent(ua string) HTTPOption {
	return func(h *httpConfig) { h.userAgent = ua }
}

// WithHTTPOptionAuth attaches sessionid / sessionid_sign cookies to
// outgoing helper requests so account entitlements apply (real-time
// quotes on paid US-equity feeds, etc.). signature may be empty for
// legacy free accounts. Has no effect on GetUser, which takes cookies
// positionally.
func WithHTTPOptionAuth(sessionID, signature string) HTTPOption {
	return func(h *httpConfig) {
		h.sessionID = sessionID
		h.sessionIDSign = signature
	}
}

// Unexported test hooks for host overrides.
func withSearchBase(u string) HTTPOption  { return func(h *httpConfig) { h.searchBase = u } }
func withScannerBase(u string) HTTPOption { return func(h *httpConfig) { h.scannerBase = u } }

// GetUser exchanges a TradingView sessionid cookie for a short-lived
// auth_token by scraping the home page HTML. signature may be empty for
// legacy accounts.
func GetUser(ctx context.Context, sessionID, signature string, opts ...HTTPOption) (User, error) {
	cfg := defaultHTTPConfig()
	for _, o := range opts {
		o(cfg)
	}
	return fetchUser(ctx, cfg, sessionID, signature)
}

func fetchUser(ctx context.Context, cfg *httpConfig, sessionID, signature string) (User, error) {
	if sessionID == "" {
		return User{}, fmt.Errorf("%w: empty sessionid", ErrAuth)
	}
	client := noRedirectClient(cfg.client)
	next := cfg.location
	const maxRedirects = 5
	var body string
	for range maxRedirects + 1 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return User{}, err
		}
		req.Header.Set("User-Agent", cfg.userAgent)
		req.Header.Set("Cookie", authCookieHeader(sessionID, signature))

		resp, err := client.Do(req)
		if err != nil {
			return User{}, err
		}
		buf, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		_ = resp.Body.Close()
		if err != nil {
			return User{}, err
		}
		body = string(buf)
		if containsAuthToken(body) {
			break
		}
		loc := resp.Header.Get("Location")
		if loc == "" || loc == next {
			return User{}, fmt.Errorf("%w: auth_token not found in response", ErrAuth)
		}
		resolved, err := url.Parse(next)
		if err != nil {
			return User{}, err
		}
		if parsed, err := resolved.Parse(loc); err == nil {
			next = parsed.String()
		} else {
			next = loc
		}
	}
	if !containsAuthToken(body) {
		return User{}, fmt.Errorf("%w: too many redirects or expired session", ErrAuth)
	}
	return User{
		ID:             parseInt(reID.FindStringSubmatch(body)),
		Username:       firstGroup(reUsername.FindStringSubmatch(body)),
		AuthToken:      firstGroup(reAuthToken.FindStringSubmatch(body)),
		SessionHash:    firstGroup(reSessionHash.FindStringSubmatch(body)),
		PrivateChannel: firstGroup(rePrivateChan.FindStringSubmatch(body)),
	}, nil
}

func authCookieHeader(sessionID, signature string) string {
	if sessionID == "" {
		return ""
	}
	if signature == "" {
		return "sessionid=" + sessionID
	}
	return "sessionid=" + sessionID + ";sessionid_sign=" + signature
}

// SearchSymbol queries TradingView's symbol search v3 endpoint.
// query may include an exchange prefix (e.g. "BINANCE:BTC"); otherwise the
// full catalog is searched.
func SearchSymbol(ctx context.Context, query string, opts ...HTTPOption) ([]SymbolSearchResult, error) {
	cfg := defaultHTTPConfig()
	for _, o := range opts {
		o(cfg)
	}

	u, err := url.Parse(cfg.searchBase + "/symbol_search/v3")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	text := strings.ToUpper(strings.ReplaceAll(query, " ", "+"))
	if parts := strings.SplitN(text, ":", 2); len(parts) == 2 {
		q.Set("exchange", parts[0])
		q.Set("text", parts[1])
	} else {
		q.Set("text", text)
	}
	q.Set("search_type", "")
	q.Set("start", "0")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", cfg.userAgent)
	req.Header.Set("Origin", "https://www.tradingview.com")
	req.Header.Set("Referer", "https://www.tradingview.com/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if cfg.sessionID != "" {
		req.Header.Set("Cookie", authCookieHeader(cfg.sessionID, cfg.sessionIDSign))
	}

	resp, err := cfg.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tradingview: search_symbol: %s (body: %s)", resp.Status, firstChars(body, 200))
	}
	// Content-Type on this endpoint is unreliable (TradingView sometimes
	// serves JSON as text/plain), so check the body directly. A leading '<'
	// almost always means we got an HTML challenge or redirect page back.
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "<") {
		return nil, fmt.Errorf("tradingview: search_symbol: non-JSON response (body: %s)", firstChars(body, 200))
	}
	var payload struct {
		Symbols []struct {
			Symbol      string `json:"symbol"`
			Description string `json:"description"`
			Type        string `json:"type"`
			Exchange    string `json:"exchange"`
			Prefix      string `json:"prefix"`
		} `json:"symbols"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("tradingview: decode search: %w (body: %s)", err, firstChars(body, 200))
	}
	out := make([]SymbolSearchResult, 0, len(payload.Symbols))
	for _, s := range payload.Symbols {
		exchange := firstWord(s.Exchange)
		id := exchange + ":" + s.Symbol
		if s.Prefix != "" {
			id = s.Prefix + ":" + s.Symbol
		}
		out = append(out, SymbolSearchResult{
			ID:           id,
			Exchange:     exchange,
			FullExchange: s.Exchange,
			Symbol:       s.Symbol,
			Description:  stripHTMLHighlight(s.Description),
			Type:         s.Type,
		})
	}
	return out, nil
}

func firstChars(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func firstWord(s string) string {
	if head, _, ok := strings.Cut(s, " "); ok {
		return head
	}
	return s
}

func stripHTMLHighlight(s string) string {
	// TradingView wraps query hits in <em> tags. Drop them.
	s = strings.ReplaceAll(s, "<em>", "")
	s = strings.ReplaceAll(s, "</em>", "")
	return s
}

// GetTA returns TradingView's technical-analysis recommendation scores for
// a fully-qualified symbol (e.g. "BINANCE:BTCUSDT"). Each timeframe has
// three scores: Other (oscillators), All (overall), MA (moving averages).
func GetTA(ctx context.Context, fullSymbol string, opts ...HTTPOption) (TAResult, error) {
	cfg := defaultHTTPConfig()
	for _, o := range opts {
		o(cfg)
	}

	indicators := []string{"Recommend.Other", "Recommend.All", "Recommend.MA"}
	timeframes := []Timeframe{TF1m, TF5m, TF15m, TF1h, TF4h, TF1D, TF1W, TF1M}

	type colSpec struct {
		Name      string
		Timeframe Timeframe
	}
	var cols []colSpec
	var names []string
	for _, tf := range timeframes {
		for _, name := range indicators {
			var col string
			if tf == TF1D {
				col = name
			} else {
				col = name + "|" + string(tf)
			}
			names = append(names, col)
			cols = append(cols, colSpec{Name: name, Timeframe: tf})
		}
	}

	body, err := json.Marshal(struct {
		Symbols struct {
			Tickers []string `json:"tickers"`
		} `json:"symbols"`
		Columns []string `json:"columns"`
	}{
		Symbols: struct {
			Tickers []string `json:"tickers"`
		}{Tickers: []string{fullSymbol}},
		Columns: names,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.scannerBase+"/global/scan", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", cfg.userAgent)
	if cfg.sessionID != "" {
		req.Header.Set("Cookie", authCookieHeader(cfg.sessionID, cfg.sessionIDSign))
	}

	resp, err := cfg.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("tradingview: scanner: %s", resp.Status)
	}
	var payload struct {
		Data []struct {
			D []float64 `json:"d"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("tradingview: decode scanner: %w", err)
	}
	if len(payload.Data) == 0 || len(payload.Data[0].D) != len(cols) {
		return nil, fmt.Errorf("tradingview: scanner: unexpected response shape")
	}

	out := make(TAResult, len(timeframes))
	vals := payload.Data[0].D
	for i, spec := range cols {
		// Round to half-integer steps as the JS reference does: *1000/500.
		score := math.Round(vals[i]*1000) / 500
		p := out[spec.Timeframe]
		switch spec.Name {
		case "Recommend.Other":
			p.Other = score
		case "Recommend.All":
			p.All = score
		case "Recommend.MA":
			p.MA = score
		}
		out[spec.Timeframe] = p
	}
	return out, nil
}

// Scan posts a multi-symbol, multi-column query to TradingView's
// scanner endpoint and decodes the response into a map keyed by
// (returned-ticker, column).
//
// columns are TradingView screener column ids reverse-engineered from
// the web client; common ones include "ATR", "RSI", "MACD.macd",
// "Recommend.All", "average_volume_30d_calc". Append a "|<timeframe>"
// suffix for non-default timeframes (e.g. "ATR|60" for the
// 60-minute ATR; the bare id is the daily value). Column names are not
// officially documented; verify against your account before relying.
//
// Pass WithHTTPOptionAuth to use entitled (real-time) data on paid
// markets. Without it the helper goes anonymous, which silently
// demotes paid users to delayed feeds for entitled exchanges.
//
// Symbols missing from the response are absent from the result;
// individual columns the server returns null for are absent from the
// per-symbol map (vs. silently zero).
func Scan(ctx context.Context, symbols, columns []string, opts ...HTTPOption) (map[string]map[string]float64, error) {
	cfg := defaultHTTPConfig()
	for _, o := range opts {
		o(cfg)
	}

	body, err := json.Marshal(struct {
		Symbols struct {
			Tickers []string `json:"tickers"`
		} `json:"symbols"`
		Columns []string `json:"columns"`
	}{
		Symbols: struct {
			Tickers []string `json:"tickers"`
		}{Tickers: symbols},
		Columns: columns,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.scannerBase+"/global/scan", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", cfg.userAgent)
	if cfg.sessionID != "" {
		req.Header.Set("Cookie", authCookieHeader(cfg.sessionID, cfg.sessionIDSign))
	}

	resp, err := cfg.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tradingview: scanner: %s", resp.Status)
	}

	var payload struct {
		Data []struct {
			S string     `json:"s"`
			D []*float64 `json:"d"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("tradingview: decode scanner: %w", err)
	}

	out := make(map[string]map[string]float64, len(payload.Data))
	for _, row := range payload.Data {
		if len(row.D) != len(columns) {
			return nil, fmt.Errorf("tradingview: scanner: column count mismatch for %q (got %d, want %d)", row.S, len(row.D), len(columns))
		}
		m := make(map[string]float64, len(columns))
		for i, col := range columns {
			if row.D[i] == nil {
				continue
			}
			m[col] = *row.D[i]
		}
		out[row.S] = m
	}
	return out, nil
}
