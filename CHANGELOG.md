# Changelog

## [Unreleased]

### Added
- WebSocket client with anonymous + sessionid cookie auth, heartbeat
  handling, and graceful shutdown (`tradingview.Connect`).
- Frame codec for TradingView's `~m~<len>~m~` wire format, including
  heartbeat (`~h~N`) and bare-number ping forms (`internal/protocol`).
- Quote session: streaming per-symbol field deltas over a single WebSocket,
  with preset and custom field selection (`package quote`).
- Chart session: streaming OHLCV candles with in-place market/timeframe
  switching and historical fetch-more (`package chart`).
- HTTP helpers: `GetUser`, `SearchSymbol`, `GetTA` — usable without a live
  WebSocket.
- Drop-oldest backpressure with `DroppedUpdates()` observability; priority
  delivery for errors and symbol-resolved events.
- Examples: `quote_stream`, `simple_chart`, `symbol_search`.
- CI: `go vet`, `go test -race`, `staticcheck`, `go build`.

### Deferred to future releases
- Pine Script and built-in indicators (studies).
- Replay mode.
- Custom chart types (HeikinAshi, Renko, Kagi, PointAndFigure, Range, LineBreak).
- Drawings, chart tokens, Pine permission manager.
- Email/password login.
