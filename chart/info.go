package chart

// MarketInfo is the subset of TradingView's symbol_resolved payload this
// SDK exposes. Extend on demand.
type MarketInfo struct {
	SeriesID       string
	Name           string
	FullName       string
	Description    string
	Exchange       string
	ListedExchange string
	Currency       string
	Type           string
	Timezone       string
	Session        string
	SubsessionID   string
	Pricescale     int
	Minmov         int
	HasIntraday    bool
}
