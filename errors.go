package tradingview

import (
	"errors"
	"fmt"
)

// Sentinel errors.
var (
	ErrNotConnected  = errors.New("tradingview: not connected")
	ErrClosed        = errors.New("tradingview: client closed")
	ErrProtocol      = errors.New("tradingview: protocol error")
	ErrAuth          = errors.New("tradingview: authentication failed")
	ErrSessionClosed = errors.New("tradingview: session closed")
)

// ProtocolError wraps a lower-level parsing or dispatch failure with a short
// snippet of the raw frame for debugging.
type ProtocolError struct {
	Op      string
	Raw     string
	Wrapped error
}

func (e *ProtocolError) Error() string {
	if e.Raw == "" {
		return fmt.Sprintf("tradingview: protocol error during %s: %v", e.Op, e.Wrapped)
	}
	return fmt.Sprintf("tradingview: protocol error during %s: %v (raw=%q)", e.Op, e.Wrapped, e.Raw)
}

func (e *ProtocolError) Unwrap() error { return e.Wrapped }

// ServerError carries a server-reported error envelope (e.g. symbol_error,
// series_error, critical_error, protocol_error).
type ServerError struct {
	Kind string
	Args []any
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("tradingview: server %s: %v", e.Kind, e.Args)
}
