package chart

// Option configures a chart Session at creation time.
type Option func(*config)

type config struct {
	timezone   string
	bufferSize int
}

// WithTimezone sets the chart timezone (e.g. "Etc/UTC", "America/New_York").
func WithTimezone(tz string) Option { return func(c *config) { c.timezone = tz } }

// WithBufferSize sets the Updates channel capacity. Default 64.
func WithBufferSize(n int) Option { return func(c *config) { c.bufferSize = n } }

// MarketOption configures one SetMarket or SetSeries call.
type MarketOption func(*marketConfig)

type marketConfig struct {
	timeframe  string
	numCandles int
	to         int64 // unix seconds; 0 means "now"
	adjustment Adjustment
	currency   string
	session    MarketSession
}

// WithTimeframe sets the candle resolution. Values follow TradingView's
// grammar: minute resolutions as plain numbers ("1", "5", "60", "240"),
// larger windows as suffixed strings ("D", "W", "M"). The root package
// exports Timeframe constants for the common ones.
func WithTimeframe(tf string) MarketOption { return func(m *marketConfig) { m.timeframe = tf } }

// WithRange sets how many candles to load initially. Default 100.
func WithRange(n int) MarketOption { return func(m *marketConfig) { m.numCandles = n } }

// WithTo anchors the candle window at a specific unix timestamp in
// seconds. Zero, the default, means "up to now".
func WithTo(tsSec int64) MarketOption { return func(m *marketConfig) { m.to = tsSec } }

// WithAdjustment picks how historical prices are adjusted for splits or
// dividends.
func WithAdjustment(a Adjustment) MarketOption { return func(m *marketConfig) { m.adjustment = a } }

// WithCurrency requests the chart be denominated in a specific currency
// code (e.g. "EUR", "JPY").
func WithCurrency(code string) MarketOption { return func(m *marketConfig) { m.currency = code } }

// WithMarketSession selects between regular and extended trading hours.
func WithMarketSession(s MarketSession) MarketOption {
	return func(m *marketConfig) { m.session = s }
}

// Adjustment selects how historical prices are adjusted.
type Adjustment string

const (
	AdjustSplits    Adjustment = "splits"
	AdjustDividends Adjustment = "dividends"
)

// MarketSession is the trading-hours filter the chart applies.
type MarketSession string

const (
	SessionRegular  MarketSession = "regular"
	SessionExtended MarketSession = "extended"
)
