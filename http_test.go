package tradingview

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func TestGetUserParsesHTML(t *testing.T) {
	body := fixture(t, "get_user.html")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); !strings.Contains(got, "sessionid=cookie-value") {
			t.Errorf("missing sessionid cookie: %q", got)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	user, err := GetUser(t.Context(), "cookie-value", "sig",
		WithHTTPOptionLocation(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if user.AuthToken != "FAKE_AUTH_TOKEN_12345" {
		t.Errorf("AuthToken: %q", user.AuthToken)
	}
	if user.Username != "testuser" {
		t.Errorf("Username: %q", user.Username)
	}
	if user.ID != 42 {
		t.Errorf("ID: %d", user.ID)
	}
	if user.SessionHash != "abc123hash" {
		t.Errorf("SessionHash: %q", user.SessionHash)
	}
	if user.PrivateChannel != "private_xyz" {
		t.Errorf("PrivateChannel: %q", user.PrivateChannel)
	}
}

func TestGetUserRejectsExpiredSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Logged-out response: no auth_token anywhere in the HTML.
		_, _ = w.Write([]byte(`<html><body>Please log in</body></html>`))
	}))
	t.Cleanup(srv.Close)

	_, err := GetUser(t.Context(), "expired", "",
		WithHTTPOptionLocation(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err == nil {
		t.Fatal("want error on expired session")
	}
}

func TestSearchSymbolDecodesFixture(t *testing.T) {
	body := fixture(t, "symbol_search_v3.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/symbol_search/v3" {
			t.Errorf("path: %q", r.URL.Path)
		}
		if r.URL.Query().Get("text") == "" {
			t.Error("missing text param")
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	results, err := SearchSymbol(t.Context(), "btc",
		withSearchBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	if results[0].ID != "BINANCE:BTCUSDT" {
		t.Errorf("results[0].ID: %q", results[0].ID)
	}
	if strings.Contains(results[1].Description, "<em>") {
		t.Errorf("description still has em tags: %q", results[1].Description)
	}
	// Prefix overrides exchange in the ID.
	if results[2].ID != "CME_MINI:BTC1!" {
		t.Errorf("results[2].ID: %q", results[2].ID)
	}
}

func TestSearchSymbolAttachesCookieWhenAuthSet(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{"symbols":[]}`))
	}))
	t.Cleanup(srv.Close)

	_, err := SearchSymbol(t.Context(), "btc",
		withSearchBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
		WithHTTPOptionAuth("sid-value", "sig-value"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sessionid=sid-value;sessionid_sign=sig-value" {
		t.Errorf("Cookie: %q", got)
	}
}

func TestSearchSymbolNoCookieByDefault(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{"symbols":[]}`))
	}))
	t.Cleanup(srv.Close)

	_, err := SearchSymbol(t.Context(), "btc",
		withSearchBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("Cookie should be empty without WithHTTPOptionAuth, got %q", got)
	}
}

func TestSearchSymbolWithExchangePrefix(t *testing.T) {
	var captured struct {
		Exchange string
		Text     string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Exchange = r.URL.Query().Get("exchange")
		captured.Text = r.URL.Query().Get("text")
		_, _ = w.Write([]byte(`{"symbols":[]}`))
	}))
	t.Cleanup(srv.Close)

	_, err := SearchSymbol(t.Context(), "binance:btc",
		withSearchBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if captured.Exchange != "BINANCE" {
		t.Errorf("exchange: %q", captured.Exchange)
	}
	if captured.Text != "BTC" {
		t.Errorf("text: %q", captured.Text)
	}
}

func TestGetTAShapesResponse(t *testing.T) {
	body := fixture(t, "scanner_ta.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/scan" {
			t.Errorf("path: %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: %q", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Symbols struct {
				Tickers []string `json:"tickers"`
			} `json:"symbols"`
			Columns []string `json:"columns"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		if len(req.Symbols.Tickers) != 1 || req.Symbols.Tickers[0] != "BINANCE:BTCUSDT" {
			t.Errorf("tickers: %v", req.Symbols.Tickers)
		}
		if len(req.Columns) != 24 { // 8 timeframes × 3 indicators
			t.Errorf("columns: %d", len(req.Columns))
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	result, err := GetTA(t.Context(), "BINANCE:BTCUSDT",
		withScannerBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 8 {
		t.Fatalf("want 8 timeframes, got %d", len(result))
	}
	// 1m bucket: first three values in the fixture (0.5, 0.4, 0.45) →
	// round(x*1000)/500 gives 1.0, 0.8, 0.9.
	m1 := result[TF1m]
	if m1.Other != 1.0 {
		t.Errorf("TF1m.Other: %v", m1.Other)
	}
	if m1.All != 0.8 {
		t.Errorf("TF1m.All: %v", m1.All)
	}
	if m1.MA != 0.9 {
		t.Errorf("TF1m.MA: %v", m1.MA)
	}
}

func TestGetTAAttachesCookieWhenAuthSet(t *testing.T) {
	body := fixture(t, "scanner_ta.json")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	_, err := GetTA(t.Context(), "BINANCE:BTCUSDT",
		withScannerBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
		WithHTTPOptionAuth("sid-value", ""),
	)
	if err != nil {
		t.Fatal(err)
	}
	// Empty signature path: just sessionid, no semicolon-suffixed pair.
	if got != "sessionid=sid-value" {
		t.Errorf("Cookie: %q", got)
	}
}

func TestGetTANoCookieByDefault(t *testing.T) {
	body := fixture(t, "scanner_ta.json")
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	_, err := GetTA(t.Context(), "BINANCE:BTCUSDT",
		withScannerBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("Cookie should be empty without WithHTTPOptionAuth, got %q", got)
	}
}

func TestScanDecodesMultiSymbol(t *testing.T) {
	var captured struct {
		Tickers []string
		Columns []string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/global/scan" {
			t.Errorf("path: %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: %q", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			Symbols struct {
				Tickers []string `json:"tickers"`
			} `json:"symbols"`
			Columns []string `json:"columns"`
		}
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		captured.Tickers = req.Symbols.Tickers
		captured.Columns = req.Columns
		_, _ = w.Write([]byte(`{"data":[
			{"s":"BINANCE:BTCUSDT","d":[1234.5, 65.7]},
			{"s":"NASDAQ:AAPL","d":[2.1, 55.0]}
		]}`))
	}))
	t.Cleanup(srv.Close)

	got, err := Scan(t.Context(),
		[]string{"BINANCE:BTCUSDT", "NASDAQ:AAPL"},
		[]string{"ATR", "RSI"},
		withScannerBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(captured.Tickers) != 2 || captured.Tickers[1] != "NASDAQ:AAPL" {
		t.Errorf("request tickers: %v", captured.Tickers)
	}
	if len(captured.Columns) != 2 || captured.Columns[0] != "ATR" {
		t.Errorf("request columns: %v", captured.Columns)
	}
	if v := got["BINANCE:BTCUSDT"]["ATR"]; v != 1234.5 {
		t.Errorf("BTC ATR: %v", v)
	}
	if v := got["NASDAQ:AAPL"]["RSI"]; v != 55.0 {
		t.Errorf("AAPL RSI: %v", v)
	}
}

func TestScanOmitsNullColumns(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"s":"NASDAQ:FOO","d":[null, 42.0]}]}`))
	}))
	t.Cleanup(srv.Close)

	got, err := Scan(t.Context(),
		[]string{"NASDAQ:FOO"},
		[]string{"ATR", "RSI"},
		withScannerBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err != nil {
		t.Fatal(err)
	}
	row, ok := got["NASDAQ:FOO"]
	if !ok {
		t.Fatal("symbol missing from result")
	}
	if _, present := row["ATR"]; present {
		t.Error("ATR should be absent (server returned null)")
	}
	if v := row["RSI"]; v != 42.0 {
		t.Errorf("RSI: %v", v)
	}
}

func TestScanRejectsColumnCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Request asked for 2 columns; server returns 3 values.
		_, _ = w.Write([]byte(`{"data":[{"s":"X:Y","d":[1.0, 2.0, 3.0]}]}`))
	}))
	t.Cleanup(srv.Close)

	_, err := Scan(t.Context(),
		[]string{"X:Y"},
		[]string{"ATR", "RSI"},
		withScannerBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err == nil {
		t.Fatal("want error on column count mismatch")
	}
	if !strings.Contains(err.Error(), "column count mismatch") {
		t.Errorf("error message: %v", err)
	}
}

func TestScanIncludesBodyOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unknown column ATR_BOGUS"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := Scan(t.Context(),
		[]string{"X:Y"},
		[]string{"ATR_BOGUS"},
		withScannerBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
	)
	if err == nil {
		t.Fatal("want error on 4xx")
	}
	if !strings.Contains(err.Error(), "unknown column ATR_BOGUS") {
		t.Errorf("error should include response body, got: %v", err)
	}
}

func TestScanAttachesCookieWhenAuthSet(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Cookie")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	t.Cleanup(srv.Close)

	_, err := Scan(t.Context(),
		[]string{"X:Y"},
		[]string{"ATR"},
		withScannerBase(srv.URL),
		WithHTTPOptionClient(srv.Client()),
		WithHTTPOptionAuth("sid-value", "sig-value"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sessionid=sid-value;sessionid_sign=sig-value" {
		t.Errorf("Cookie: %q", got)
	}
}
