// Package tradingview is a Go SDK for TradingView's unofficial WebSocket and
// HTTP APIs.
//
// The SDK opens a single long-lived WebSocket connection and multiplexes
// quote and chart sessions over it. HTTP helpers cover session-cookie auth,
// symbol search, and technical-analysis lookups.
//
// # Quickstart
//
//	ctx := context.Background()
//	client, err := tradingview.Connect(ctx)
//	if err != nil { log.Fatal(err) }
//	defer client.Close()
//
//	qs, err := client.NewQuoteSession()
//	if err != nil { log.Fatal(err) }
//	qs.AddSymbol("BINANCE:BTCUSDT", quote.SessionRegular)
//
//	for u := range qs.Updates() {
//	    if d, ok := u.(quote.QuoteData); ok {
//	        log.Printf("%s: %v", d.Symbol, d.Fields["lp"])
//	    }
//	}
//
// See the examples/ directory for runnable programs.
//
// # Scope
//
// v0.1 ships the core: quote streaming, chart streaming with OHLCV candles,
// and three HTTP helpers (GetUser, SearchSymbol, GetTA). Indicators, replay
// mode, drawings, and email/password login are deferred.
package tradingview
