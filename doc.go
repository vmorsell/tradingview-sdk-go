// Package tradingview is an unofficial Go client for TradingView's realtime
// WebSocket and HTTP APIs.
//
// A single Client holds one WebSocket connection and multiplexes quote and
// chart sessions over it. Streaming data arrives on per-session channels
// carrying a sum type (data, completed, error). HTTP helpers handle
// session-cookie auth, symbol search, and technical-analysis lookups.
//
// Basic usage:
//
//	ctx := context.Background()
//	client, err := tradingview.Connect(ctx)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	qs, _ := client.NewQuoteSession()
//	qs.AddSymbol("BINANCE:BTCUSDT", quote.SessionRegular)
//
//	for u := range qs.Updates() {
//	    if d, ok := u.(quote.QuoteData); ok {
//	        log.Printf("%s: %v", d.Symbol, d.Fields["lp"])
//	    }
//	}
//
// See the examples/ directory for runnable programs covering quote
// streaming, chart streaming, and symbol search.
//
// # Scope
//
// v0.1 implements the core streaming primitives (quotes, charts) and three
// HTTP helpers (GetUser, SearchSymbol, GetTA). Indicators, replay mode,
// custom chart types, drawings, and email/password login are out of scope
// for now.
package tradingview
