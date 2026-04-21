package quote

// Update is the common type emitted on Session.Updates. Every emission is
// exactly one of the concrete variants below; use a type switch at the
// call site.
type Update interface{ isQuoteUpdate() }

// QuoteData carries the accumulated field state for a symbol. The server
// sends deltas, but the session merges them so consumers see the full
// picture on every update.
type QuoteData struct {
	Symbol string
	Fields map[Field]any
}

func (QuoteData) isQuoteUpdate() {}

// QuoteCompleted is emitted once TradingView signals that the initial
// snapshot for a symbol has finished loading. It fires exactly once per
// (symbol, session) subscription.
type QuoteCompleted struct {
	Symbol string
}

func (QuoteCompleted) isQuoteUpdate() {}

// QuoteError reports a per-symbol failure such as an unknown ticker or a
// permission denial. Errors go through a priority path and are not dropped
// by the backpressure policy unless the session is closing.
type QuoteError struct {
	Symbol string
	Err    error
}

func (QuoteError) isQuoteUpdate() {}

// SessionKind picks the market session a quote subscription uses.
// SessionRegular is the common case; SessionExtended covers pre- and
// post-market on exchanges that offer it.
type SessionKind string

const (
	SessionRegular  SessionKind = "regular"
	SessionExtended SessionKind = "extended"
)
