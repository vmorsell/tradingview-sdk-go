// Package protocol implements TradingView's WebSocket wire format.
//
// Frames are length-prefixed: "~m~<len>~m~<payload>". A single WebSocket
// message often contains multiple concatenated frames. The payload is one of:
//
//   - a JSON envelope {"m":"method","p":[session_id, ...args]}
//   - a heartbeat "~h~<n>" the client must echo back verbatim
//   - a bare integer (alternate ping form)
//   - a plain JSON session-hello on first connect (no m/p envelope)
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// ErrInvalidFrame is returned when input cannot be framed.
var ErrInvalidFrame = errors.New("invalid frame")

// Envelope is a decoded frame.
//
// Exactly one shape is populated:
//   - Method != "" → RPC envelope; Params is the raw JSON of each element of "p".
//   - Ping != 0 → heartbeat or bare-number ping the caller should echo.
//   - Raw != nil → JSON payload without an {m,p} envelope (e.g. server hello).
type Envelope struct {
	Method string
	Params []json.RawMessage
	Ping   int
	Raw    json.RawMessage
}

// IsPing reports whether this envelope is a heartbeat the client must echo.
func (e Envelope) IsPing() bool { return e.Method == "" && e.Raw == nil && e.Ping != 0 }

// SessionID extracts the session id from the first RPC parameter, if present.
// TradingView addresses most session-scoped messages by putting the session
// id at params[0].
func (e Envelope) SessionID() string {
	if len(e.Params) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(e.Params[0], &s); err != nil {
		return ""
	}
	return s
}

// Decode splits one WebSocket text message into zero or more envelopes.
// A well-formed input contains only complete frames; a trailing partial frame
// is reported as ErrInvalidFrame.
func Decode(data []byte) ([]Envelope, error) {
	var out []Envelope
	for i := 0; i < len(data); {
		if !startsWithMarker(data[i:]) {
			return out, fmt.Errorf("%w: expected ~m~ at offset %d", ErrInvalidFrame, i)
		}
		i += 3
		lenStart := i
		for i < len(data) && data[i] != '~' {
			i++
		}
		if i == lenStart || i+3 > len(data) || !startsWithMarker(data[i:]) {
			return out, fmt.Errorf("%w: truncated length prefix", ErrInvalidFrame)
		}
		n, err := strconv.Atoi(string(data[lenStart:i]))
		if err != nil {
			return out, fmt.Errorf("%w: bad length %q: %v", ErrInvalidFrame, data[lenStart:i], err)
		}
		i += 3
		if i+n > len(data) {
			return out, fmt.Errorf("%w: short payload: want %d bytes, have %d", ErrInvalidFrame, n, len(data)-i)
		}
		env, err := decodePayload(data[i : i+n])
		if err != nil {
			return out, err
		}
		out = append(out, env)
		i += n
	}
	return out, nil
}

func startsWithMarker(b []byte) bool {
	return len(b) >= 3 && b[0] == '~' && b[1] == 'm' && b[2] == '~'
}

func decodePayload(payload []byte) (Envelope, error) {
	// Heartbeat form: ~h~N
	if len(payload) >= 3 && payload[0] == '~' && payload[1] == 'h' && payload[2] == '~' {
		n, err := strconv.Atoi(string(payload[3:]))
		if err != nil {
			return Envelope{}, fmt.Errorf("%w: bad heartbeat %q: %v", ErrInvalidFrame, payload, err)
		}
		return Envelope{Ping: n}, nil
	}
	// Bare-number ping
	if n, err := strconv.Atoi(string(payload)); err == nil {
		return Envelope{Ping: n}, nil
	}
	// JSON (either {m,p} or plain object)
	if len(payload) == 0 {
		return Envelope{Raw: json.RawMessage{}}, nil
	}
	if payload[0] != '{' {
		return Envelope{Raw: append(json.RawMessage(nil), payload...)}, nil
	}
	var env struct {
		Method string            `json:"m"`
		Params []json.RawMessage `json:"p"`
	}
	if err := json.Unmarshal(payload, &env); err != nil || env.Method == "" {
		return Envelope{Raw: append(json.RawMessage(nil), payload...)}, nil
	}
	return Envelope{Method: env.Method, Params: env.Params}, nil
}

// Encode frames an RPC envelope for transmission.
func Encode(method string, params ...any) ([]byte, error) {
	if params == nil {
		params = []any{}
	}
	env := struct {
		Method string `json:"m"`
		Params []any  `json:"p"`
	}{Method: method, Params: params}
	body, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("tradingview: encode envelope: %w", err)
	}
	return frame(body), nil
}

// EncodeHeartbeat frames a ~h~N reply for a server-sent ping.
func EncodeHeartbeat(n int) []byte {
	return frame([]byte("~h~" + strconv.Itoa(n)))
}

func frame(body []byte) []byte {
	prefix := "~m~" + strconv.Itoa(len(body)) + "~m~"
	out := make([]byte, 0, len(prefix)+len(body))
	out = append(out, prefix...)
	out = append(out, body...)
	return out
}
