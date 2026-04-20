package tradingview

// Server selects which TradingView WebSocket data server to connect to.
// Most callers want ServerData. ServerProData is for paid accounts;
// ServerWidgetData is for widget-embed flows.
type Server string

const (
	ServerData       Server = "data"
	ServerProData    Server = "prodata"
	ServerWidgetData Server = "widgetdata"
)

// Timeframe is a chart period resolution. Numeric values are minutes;
// D, W, M are day/week/month.
type Timeframe string

const (
	TF1m  Timeframe = "1"
	TF3m  Timeframe = "3"
	TF5m  Timeframe = "5"
	TF15m Timeframe = "15"
	TF30m Timeframe = "30"
	TF45m Timeframe = "45"
	TF1h  Timeframe = "60"
	TF2h  Timeframe = "120"
	TF3h  Timeframe = "180"
	TF4h  Timeframe = "240"
	TF1D  Timeframe = "1D"
	TF1W  Timeframe = "1W"
	TF1M  Timeframe = "1M"
)

// User is the authenticated TradingView user identity derived from a session
// cookie. Only the handful of fields the SDK actually consumes are populated.
type User struct {
	ID             int64
	Username       string
	AuthToken      string
	SessionHash    string
	PrivateChannel string
}

// SymbolSearchResult is a single hit from SearchSymbol.
type SymbolSearchResult struct {
	ID           string // e.g. "COINBASE:BTCEUR"
	Exchange     string
	FullExchange string
	Symbol       string
	Description  string
	Type         string
}

// TAResult maps each timeframe to its technical-analysis scores.
type TAResult map[Timeframe]TAPeriod

// TAPeriod carries the three TradingView recommendation scores for a single
// timeframe. Each score is in roughly [-1, 1]: negative is bearish, positive
// bullish.
type TAPeriod struct {
	Other float64
	All   float64
	MA    float64
}
