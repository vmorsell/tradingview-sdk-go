package quote

// Update is emitted on Session.Updates. Exactly one concrete variant is
// returned per emission; type-switch on it at the call site.
type Update interface{ isQuoteUpdate() }

// QuoteData is a per-symbol field delta merged with all fields previously
// received for that symbol. Fields contains only what changed since the last
// emission, but callers get the accumulated view.
type QuoteData struct {
	Symbol string
	Fields map[Field]any
}

func (QuoteData) isQuoteUpdate() {}

// QuoteCompleted fires once TradingView has delivered the initial snapshot
// for a symbol (the server emits quote_completed exactly once per symbol).
type QuoteCompleted struct {
	Symbol string
}

func (QuoteCompleted) isQuoteUpdate() {}

// QuoteError signals a per-symbol error (e.g. unknown symbol, permission
// denied). Delivered with priority — never dropped by the backpressure
// policy.
type QuoteError struct {
	Symbol string
	Err    error
}

func (QuoteError) isQuoteUpdate() {}

// SessionKind is the market session used when subscribing: regular trading
// hours ("regular") or extended hours ("extended").
type SessionKind string

const (
	SessionRegular  SessionKind = "regular"
	SessionExtended SessionKind = "extended"
)
