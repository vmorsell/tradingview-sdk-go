package tradingview

import "errors"

// Sentinel errors returned or wrapped by the SDK. Use errors.Is to match.
var (
	ErrClosed = errors.New("tradingview: client closed")
	ErrAuth   = errors.New("tradingview: authentication failed")
)
