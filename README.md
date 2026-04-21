# tradingview-sdk-go

[![CI](https://github.com/vmorsell/tradingview-sdk-go/actions/workflows/ci.yml/badge.svg)](https://github.com/vmorsell/tradingview-sdk-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/vmorsell/tradingview-sdk-go.svg)](https://pkg.go.dev/github.com/vmorsell/tradingview-sdk-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/vmorsell/tradingview-sdk-go)](https://goreportcard.com/report/github.com/vmorsell/tradingview-sdk-go)
[![Latest Release](https://img.shields.io/github/v/release/vmorsell/tradingview-sdk-go?include_prereleases&sort=semver)](https://github.com/vmorsell/tradingview-sdk-go/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/vmorsell/tradingview-sdk-go)](go.mod)
[![License](https://img.shields.io/github/license/vmorsell/tradingview-sdk-go)](LICENSE)

A Go client for [TradingView](https://www.tradingview.com)'s undocumented realtime feeds. It opens one WebSocket, multiplexes quote and chart sessions over it, and exposes updates as typed channels. There is no official public API; this library is reverse-engineered from TradingView's web client.

## Status

This is a `v0.x` release. The public Go API is allowed to change between minor versions, and TradingView's own endpoints have no stability guarantees at all. Pin exact versions if you depend on this in anything production-adjacent.

## What it does

Realtime quotes and OHLCV candles are the two streaming primitives. On the HTTP side there are three helpers you can use on their own, before or without opening a WebSocket.

Quotes: subscribe to one or more symbols, receive per-field deltas (last price, bid/ask, volume, fundamentals, and the other 40-odd fields the web client uses). The session merges deltas into an accumulated view so consumers always get the latest snapshot.

Charts: resolve a market, start streaming OHLCV at any TradingView timeframe, switch timeframes in place, request more history on demand. Candles decode into `time.Time` + `float64` fields with no wire-format leakage.

HTTP helpers: `GetUser` trades a `sessionid` cookie for an `auth_token` by scraping the home page. `SearchSymbol` hits the v3 symbol-search endpoint. `GetTA` pulls the three recommendation scores (oscillators, moving averages, overall) across eight timeframes from the scanner.

A few smaller things worth mentioning: updates flow through a single channel carrying a sum type, so there's no separate error channel to forget. When a consumer falls behind the oldest data updates are dropped, never errors or symbol-resolved events, and `DroppedUpdates()` exposes the counter. `Client.Close` honours gorilla/websocket's single-writer invariant during shutdown.

## Install

```bash
go get github.com/vmorsell/tradingview-sdk-go
```

Go 1.25 or later.

## Quick start

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/signal"

    "github.com/vmorsell/tradingview-sdk-go"
    "github.com/vmorsell/tradingview-sdk-go/quote"
)

func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    client, err := tradingview.Connect(ctx)
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    qs, err := client.NewQuoteSession(quote.WithFields(quote.FieldPresetPrice))
    if err != nil {
        log.Fatal(err)
    }
    if err := qs.AddSymbol("BINANCE:BTCUSDT", quote.SessionRegular); err != nil {
        log.Fatal(err)
    }

    for u := range qs.Updates() {
        if d, ok := u.(quote.QuoteData); ok {
            fmt.Printf("%s %v\n", d.Symbol, d.Fields[quote.FieldLastPrice])
        }
    }
}
```

## Streaming candles

```go
cs, err := client.NewChartSession()
if err != nil {
    log.Fatal(err)
}
if err := cs.SetMarket("BINANCE:BTCEUR",
    chart.WithTimeframe("D"),
    chart.WithRange(200),
); err != nil {
    log.Fatal(err)
}

for u := range cs.Updates() {
    switch v := u.(type) {
    case chart.SymbolResolved:
        log.Printf("resolved: %s (%s)", v.Info.FullName, v.Info.Description)
    case chart.Candles:
        c := v.Changed[len(v.Changed)-1]
        fmt.Printf("%s  O=%.2f  H=%.2f  L=%.2f  C=%.2f  V=%.2f\n",
            c.Time.Format("2006-01-02 15:04"),
            c.Open, c.High, c.Low, c.Close, c.Volume)
    case chart.ChartError:
        log.Printf("chart error (%s): %v", v.Kind, v.Err)
    }
}
```

`cs.SetSeries("15")` swaps to a 15-minute view without re-resolving the symbol. `cs.RequestMore(200)` walks 200 candles further back in history.

## Symbol search and technical analysis

These two are plain HTTP, no WebSocket required:

```go
results, err := tradingview.SearchSymbol(ctx, "btc")
if err != nil {
    log.Fatal(err)
}
for _, r := range results[:10] {
    fmt.Printf("%-28s %-10s %s\n", r.ID, r.Type, r.Description)
}

ta, err := tradingview.GetTA(ctx, results[0].ID)
if err != nil {
    log.Fatal(err)
}
d := ta[tradingview.TF1D]
fmt.Printf("%s 1D all=%+.2f ma=%+.2f other=%+.2f\n",
    results[0].ID, d.All, d.MA, d.Other)
```

## Authenticated use

Paid-plan features need a browser session cookie. Pull `sessionid` and `sessionid_sign` from your logged-in TradingView tab:

```go
client, err := tradingview.Connect(ctx,
    tradingview.WithSessionCookies(
        os.Getenv("TV_SESSIONID"),
        os.Getenv("TV_SESSIONID_SIGN"),
    ),
)
```

If all you need is the `auth_token`, `tradingview.GetUser` performs that exchange standalone.

## Configuration

```go
client, err := tradingview.Connect(ctx,
    tradingview.WithServer(tradingview.ServerProData),
    tradingview.WithUserAgent("MyApp/1.0"),
    tradingview.WithHTTPHeader("Accept-Language", "de-DE"),
    tradingview.WithLogger(slog.Default()),
)
```

All options compose. Defaults are anonymous auth against `data.tradingview.com`, a browser-like User-Agent (the TradingView WAF rejects obvious bots), and a no-op logger.

## Examples

Three runnable programs under [`examples/`](examples/):

| Command | What it does |
|---|---|
| `go run ./examples/quote_stream BINANCE:BTCUSDT COINBASE:ETHEUR` | live last-price ticker for one or more symbols |
| `go run ./examples/simple_chart BINANCE:BTCEUR D` | resolves a symbol and prints every OHLCV update |
| `go run ./examples/symbol_search btc` | searches by keyword, prints TA for the top hit |

## Scope and non-goals (for now)

Shipped in v0.1:

- WebSocket client with anonymous or sessionid auth, heartbeat handling, graceful shutdown
- Frame codec for the `~m~<len>~m~` wire format plus heartbeat and bare-number ping forms
- Quote session streaming
- Chart session streaming with timeframe switching
- HTTP helpers: `GetUser`, `SearchSymbol`, `GetTA`

Not implemented yet:

- Pine Script and built-in indicators (`create_study` and friends)
- Replay mode
- Custom chart types (HeikinAshi, Renko, Kagi, PointAndFigure, Range, LineBreak)
- Drawings, chart tokens, and the Pine permission manager
- Email/password login (the sessionid cookie flow is much more reliable and Cloudflare-tolerant)

Happy to accept PRs or issues for any of those.

## Credit

Modeled after the Node.js reference at [Mathieu2301/TradingView-API](https://github.com/Mathieu2301/TradingView-API); the wire format and HTTP endpoint details come from there. Unofficial TradingView clients exist in other languages too if you need them.

## Contributing

Run `make ci` before opening a PR. That covers `go test -race -shuffle=on`, `govulncheck`, `golangci-lint`, `gofmt`, and `go mod tidy -diff`.

## License

[MIT](LICENSE).

---

<details>
<summary><strong>Disclaimer</strong></summary>

This is an unofficial, reverse-engineered SDK. It is not affiliated with, endorsed by, or supported by TradingView, Inc. The endpoints it talks to are undocumented and can change or break without notice. Read [TradingView's Terms of Service](https://www.tradingview.com/policies/) before deploying anything that relies on this library. You are responsible for how you use it.

</details>
