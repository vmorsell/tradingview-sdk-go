package chart

// Update is the common type emitted on Session.Updates. Use a type switch
// to dispatch on the concrete variants below.
type Update interface{ isChartUpdate() }

// SymbolResolved fires once TradingView accepts the symbol and returns
// its metadata. The same snapshot is available via Session.Info at any
// time after this event.
type SymbolResolved struct {
	Info MarketInfo
}

func (SymbolResolved) isChartUpdate() {}

// Candles is a batch of candles that changed in one timescale_update.
// Changed is sorted by Time ascending.
type Candles struct {
	Changed []Candle
}

func (Candles) isChartUpdate() {}

// ChartError reports a server-side error such as symbol_error,
// series_error, or critical_error. Like QuoteError in the quote package,
// it flows through the priority path and is not dropped under normal
// backpressure.
type ChartError struct {
	Kind string
	Err  error
}

func (ChartError) isChartUpdate() {}
