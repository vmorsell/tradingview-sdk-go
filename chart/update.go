package chart

// Update is emitted on Session.Updates.
type Update interface{ isChartUpdate() }

// SymbolResolved fires once TradingView accepts the symbol and ships its
// metadata. Info is also accessible via Session.Info() at any time.
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

// ChartError is a server-reported error (symbol_error, series_error,
// critical_error). Delivered with priority — never dropped.
type ChartError struct {
	Kind string
	Err  error
}

func (ChartError) isChartUpdate() {}
