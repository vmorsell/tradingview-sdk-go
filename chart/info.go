package chart

// MarketInfo carries the fields from TradingView's symbol_resolved packet
// that this SDK surfaces. Add more on demand; the wire-side struct in
// session.go decodes everything the server sends.
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
