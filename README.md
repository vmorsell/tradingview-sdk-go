# tradingview-sdk-go

Unofficial Go SDK for [TradingView](https://www.tradingview.com)'s WebSocket
and HTTP APIs. Modeled on the Node.js
[TradingView-API](https://github.com/Mathieu2301/TradingView-API) reference,
rewritten for idiomatic Go — channels instead of callbacks, `context.Context`
for lifecycle, functional options for configuration.

> **Status: v0.1.** Ships the core only — streaming quotes, streaming charts,
> and three HTTP helpers. Indicators, replay mode, drawings, and
> email/password login are deferred; see [Scope](#scope) below.

## Install

```bash
go get github.com/vmorsell/tradingview-sdk-go@latest
```

Go 1.25 or later.

## Quickstart

### Stream last-price quotes

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
defer stop()

client, err := tradingview.Connect(ctx)
if err != nil { log.Fatal(err) }
defer client.Close()

qs, err := client.NewQuoteSession(quote.WithFields(quote.FieldPresetPrice))
if err != nil { log.Fatal(err) }
qs.AddSymbol("BINANCE:BTCUSDT", quote.SessionRegular)

for u := range qs.Updates() {
    if d, ok := u.(quote.QuoteData); ok {
        fmt.Println(d.Symbol, d.Fields[quote.FieldLastPrice])
    }
}
```

### Stream chart candles

```go
cs, _ := client.NewChartSession()
cs.SetMarket("BINANCE:BTCEUR",
    chart.WithTimeframe("D"),
    chart.WithRange(200),
)

for u := range cs.Updates() {
    switch v := u.(type) {
    case chart.SymbolResolved:
        log.Println("resolved:", v.Info.FullName)
    case chart.Candles:
        last := v.Changed[len(v.Changed)-1]
        log.Printf("%s close=%.2f", last.Time.Format("2006-01-02"), last.Close)
    case chart.ChartError:
        log.Println("error:", v.Kind, v.Err)
    }
}
```

### Search + TA

```go
results, _ := tradingview.SearchSymbol(ctx, "btc")
ta, _ := tradingview.GetTA(ctx, results[0].ID)
fmt.Printf("%s 1D All=%+.2f\n", results[0].ID, ta[tradingview.TF1D].All)
```

### Authenticated

```go
client, err := tradingview.Connect(ctx,
    tradingview.WithSessionCookies(os.Getenv("TV_SESSIONID"), os.Getenv("TV_SESSIONID_SIGN")),
)
```

## Examples

Three runnable programs under `examples/`:

| Command | Purpose |
|---|---|
| `go run ./examples/quote_stream BINANCE:BTCUSDT COINBASE:ETHEUR` | realtime last-price ticker |
| `go run ./examples/simple_chart BINANCE:BTCEUR D` | streaming OHLCV candles |
| `go run ./examples/symbol_search btc` | symbol lookup + TA snapshot |

## Design notes

- **Single connection, many sessions.** `Client` opens one WebSocket and
  multiplexes quote and chart sessions over it.
- **Channel-based events.** Each session exposes one `Updates() <-chan Update`
  channel with a sum-type payload (data, completed, error). No separate error
  channel to forget to drain.
- **Drop-oldest backpressure.** If a consumer falls behind, old data updates
  are evicted to keep the pipeline flowing. Errors and `SymbolResolved`
  events go through a priority path and are not dropped in the common case.
  `session.DroppedUpdates()` exposes the count.
- **Graceful shutdown.** `Client.Close()` flushes the send queue, sends a
  WebSocket close frame via the same goroutine that owns writes (respecting
  gorilla's single-writer invariant), then closes each session's channel.
  `Done()` fires when teardown completes; `Err()` reports the first fatal
  cause, if any.

## Scope

**v0.1 ships:**
- WebSocket client: connect, anonymous or sessionid auth, heartbeat, shutdown
- Frame codec with heartbeat + bare-number ping handling
- Quote session streaming
- Chart session streaming with OHLCV candles and timeframe switching
- HTTP helpers: `GetUser`, `SearchSymbol`, `GetTA`

**Deferred to v0.2+:**
- Pine Script and built-in indicators (`create_study`)
- Replay mode
- Custom chart types (HeikinAshi, Renko, etc.)
- Drawings and chart tokens
- Pine permission manager
- Email/password login (use the sessionid cookie flow instead — more reliable)

Contributions welcome for any of the deferred items.

## License

MIT. See [LICENSE](LICENSE).
