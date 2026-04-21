# tradingview-sdk-go

[![CI](https://github.com/vmorsell/tradingview-sdk-go/actions/workflows/ci.yml/badge.svg)](https://github.com/vmorsell/tradingview-sdk-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/vmorsell/tradingview-sdk-go.svg)](https://pkg.go.dev/github.com/vmorsell/tradingview-sdk-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/vmorsell/tradingview-sdk-go)](https://goreportcard.com/report/github.com/vmorsell/tradingview-sdk-go)
[![Latest Release](https://img.shields.io/github/v/release/vmorsell/tradingview-sdk-go?include_prereleases&sort=semver)](https://github.com/vmorsell/tradingview-sdk-go/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/vmorsell/tradingview-sdk-go)](go.mod)
[![License](https://img.shields.io/github/license/vmorsell/tradingview-sdk-go)](LICENSE)

Unofficial Go SDK for [TradingView](https://www.tradingview.com)'s realtime WebSocket and HTTP APIs. Streams quote-field deltas and OHLCV candles over a single multiplexed connection, handles session-cookie authentication, and ships stateless helpers for symbol search and technical-analysis snapshots. Reverse-engineered from the TradingView web client — there is no official public API.

## Status

**`v0.x` — API surface may change between minor versions.** The SDK is functional for streaming quotes and candles but the TradingView endpoints themselves are undocumented and can change without notice. Pin exact versions in production.

## Features

- **WebSocket client** — single connection multiplexes many sessions. Anonymous or `sessionid` cookie auth, heartbeat handling, graceful shutdown.
- **Quote streaming** — per-symbol field deltas (last price, bid/ask, volume, fundamentals, …) merged into accumulated state. Preset or custom field selection.
- **Chart streaming** — resolve a market, stream OHLCV candles for any TradingView timeframe, switch timeframes in place, fetch historical candles on demand.
- **HTTP helpers** — `GetUser` (sessionid → authToken), `SearchSymbol` (v3 endpoint), `GetTA` (Recommend.All / .MA / .Other scores across 8 timeframes). Usable without a live WebSocket.
- **Channel-based API** — `session.Updates() <-chan Update` with a sum-type payload (data / completed / error). No callbacks, no error channel to forget to drain.
- **Drop-oldest backpressure** — data updates evict oldest on slow consumers; errors and `SymbolResolved` are delivered with priority. Observable via `DroppedUpdates()`.
- **Graceful shutdown** — `Client.Close()` respects gorilla/websocket's single-writer invariant, flushes the send queue, and closes every session's channel. `Done()` / `Err()` for post-mortem.

## Install

```bash
go get github.com/vmorsell/tradingview-sdk-go
```

Go 1.25+.

## Quick start

Stream last-price quotes for one or more symbols:

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

`cs.SetSeries("15")` swaps to a 15-minute view without re-resolving the symbol. `cs.RequestMore(200)` fetches 200 earlier candles.

## Symbol search and technical analysis

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
fmt.Printf("%s 1D all=%+.2f ma=%+.2f other=%+.2f\n",
    results[0].ID,
    ta[tradingview.TF1D].All,
    ta[tradingview.TF1D].MA,
    ta[tradingview.TF1D].Other)
```

## Authenticated

Paid-plan features (premium indicators, private data) require a browser session cookie:

```go
client, err := tradingview.Connect(ctx,
    tradingview.WithSessionCookies(
        os.Getenv("TV_SESSIONID"),
        os.Getenv("TV_SESSIONID_SIGN"),
    ),
)
```

`GetUser` performs the same exchange standalone if you just want an `auth_token`.

## Configuration

```go
client, err := tradingview.Connect(ctx,
    tradingview.WithServer(tradingview.ServerProData),
    tradingview.WithUserAgent("MyApp/1.0"),
    tradingview.WithHTTPHeader("Accept-Language", "de-DE"),
    tradingview.WithLogger(slog.Default()),
)
```

All options compose. Defaults: anonymous auth, `data.tradingview.com`, Chrome-like User-Agent, a no-op logger.

## Examples

Runnable end-to-end programs live under [`examples/`](examples/):

| Command | Purpose |
|---|---|
| `go run ./examples/quote_stream BINANCE:BTCUSDT COINBASE:ETHEUR` | realtime last-price ticker |
| `go run ./examples/simple_chart BINANCE:BTCEUR D` | streaming OHLCV candles |
| `go run ./examples/symbol_search btc` | symbol lookup + TA snapshot |

## Scope

**v0.1 ships:**
- WebSocket client: connect, anonymous or sessionid auth, heartbeat, shutdown
- Frame codec with heartbeat + bare-number ping handling
- Quote session streaming
- Chart session streaming with OHLCV candles and timeframe switching
- HTTP helpers: `GetUser`, `SearchSymbol`, `GetTA`

**Not yet implemented:**
- Pine Script and built-in indicators (`create_study`)
- Replay mode
- Custom chart types (HeikinAshi, Renko, Kagi, PointAndFigure, Range, LineBreak)
- Drawings, chart tokens, Pine permission manager
- Email/password login (use the sessionid cookie flow — more reliable)

Contributions welcome for any of the above.

## Related projects

The Node.js reference this SDK was modeled on: [Mathieu2301/TradingView-API](https://github.com/Mathieu2301/TradingView-API). Unofficial TradingView clients in other languages exist — search GitHub for "tradingview" if you need Python, Rust, or other runtimes.

## Contributing

Issues and PRs welcome. Run `make ci` (tests with `-race` and `-shuffle=on`, `govulncheck`, `golangci-lint`, `gofmt`, `go mod tidy`) before submitting.

## License

[MIT](LICENSE).

---

<details>
<summary><strong>Disclaimer</strong></summary>

This is an unofficial, reverse-engineered SDK. It is **not affiliated with, endorsed by, or supported by TradingView, Inc.** The underlying endpoints are undocumented and may change or break without notice. This SDK is provided for educational and personal use; respect [TradingView's Terms of Service](https://www.tradingview.com/policies/) when deciding whether and how to deploy it. You are solely responsible for any use of this SDK.

</details>
