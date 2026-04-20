// Package quote implements the TradingView quote session — realtime
// per-symbol field updates (last price, volume, bid/ask, fundamentals, …).
//
// One quote session can host many symbols; updates arrive as a single
// channel of sum-type events tagged by symbol.
package quote

// Field is a TradingView quote field name. Send only the fields you care
// about to reduce bandwidth; the default FieldPresetAll covers the common
// set the reference JS SDK uses.
type Field string

// FieldPreset selects a canned bundle of fields.
type FieldPreset int

const (
	// FieldPresetAll is the full "all" bundle (~45 fields).
	FieldPresetAll FieldPreset = iota
	// FieldPresetPrice subscribes only to last price ("lp").
	FieldPresetPrice
)

// Common fields. Values map directly to TradingView keys.
const (
	FieldLastPrice       Field = "lp"
	FieldLastPriceTime   Field = "lp_time"
	FieldVolume          Field = "volume"
	FieldBid             Field = "bid"
	FieldAsk             Field = "ask"
	FieldChange          Field = "ch"
	FieldChangePct       Field = "chp"
	FieldHigh            Field = "high_price"
	FieldLow             Field = "low_price"
	FieldOpen            Field = "open_price"
	FieldPrevClose       Field = "prev_close_price"
	FieldExchange        Field = "exchange"
	FieldDescription     Field = "description"
	FieldCurrencyCode    Field = "currency_code"
	FieldType            Field = "type"
	FieldUpdateMode      Field = "update_mode"
	FieldIsTradable      Field = "is_tradable"
	FieldMarketCapBasic  Field = "market_cap_basic"
	FieldPriceEarningsTT Field = "price_earnings_ttm"
)

func fieldsForPreset(p FieldPreset) []Field {
	switch p {
	case FieldPresetPrice:
		return []Field{FieldLastPrice}
	default:
		return allFields
	}
}

var allFields = []Field{
	"base-currency-logoid", "ch", "chp", "currency-logoid",
	"currency_code", "current_session", "description",
	"exchange", "format", "fractional", "is_tradable",
	"language", "local_description", "logoid", "lp",
	"lp_time", "minmov", "minmove2", "original_name",
	"pricescale", "pro_name", "short_name", "type",
	"update_mode", "volume", "ask", "bid", "fundamentals",
	"high_price", "low_price", "open_price", "prev_close_price",
	"rch", "rchp", "rtc", "rtc_time", "status", "industry",
	"basic_eps_net_income", "beta_1_year", "market_cap_basic",
	"earnings_per_share_basic_ttm", "price_earnings_ttm",
	"sector", "dividends_yield", "timezone", "country_code",
	"provider_id",
}
