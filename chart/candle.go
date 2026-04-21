// Package chart implements the TradingView chart session: streaming OHLCV
// candles for one market at a time, with timeframe switching and on-demand
// historical backfill.
package chart

import "time"

// Candle is one OHLCV bar. Time is the period-open time in UTC.
type Candle struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
}

// candleFromWire decodes a period vector from a TradingView timescale
// packet. The wire shape is [time, open, high, low, close, volume] with
// time as a Unix second.
func candleFromWire(v []float64) (Candle, bool) {
	if len(v) < 6 {
		return Candle{}, false
	}
	return Candle{
		Time:   time.Unix(int64(v[0]), 0).UTC(),
		Open:   v[1],
		High:   v[2],
		Low:    v[3],
		Close:  v[4],
		Volume: v[5],
	}, true
}
