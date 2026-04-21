package wire

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/vmorsell/tradingview-sdk-go/internal/protocol"
)

// Conn is the minimal WebSocket surface the pump uses. *websocket.Conn
// satisfies it; keeping it narrow makes the pump testable without
// pulling in httptest.
type Conn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// UnsolicitedHandler is called for envelopes with no matching session id.
// In practice this covers the server's connection hello and any stray
// packets that arrive before a session is registered.
type UnsolicitedHandler func(env protocol.Envelope)

// Pump runs the reader, writer, and dispatcher goroutines for one Conn.
// Construct one per Client lifetime.
type Pump struct {
	conn    Conn
	logger  *slog.Logger
	reg     *Registry
	onOther UnsolicitedHandler

	sendCh  chan []byte
	quitCh  chan struct{}
	doneCh  chan struct{}
	errSlot atomic.Pointer[error]

	closeOnce sync.Once
	wg        sync.WaitGroup
}

// Options configures a new Pump.
type Options struct {
	Conn        Conn
	Logger      *slog.Logger
	Registry    *Registry
	Unsolicited UnsolicitedHandler
	SendBuffer  int
}

// NewPump wires up a Pump. It does not start any goroutines; call Start
// once the returned Pump is stored on the owning Client.
func NewPump(opts Options) *Pump {
	if opts.SendBuffer <= 0 {
		opts.SendBuffer = 256
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Pump{
		conn:    opts.Conn,
		logger:  opts.Logger,
		reg:     opts.Registry,
		onOther: opts.Unsolicited,
		sendCh:  make(chan []byte, opts.SendBuffer),
		quitCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start launches the reader and writer goroutines.
func (p *Pump) Start() {
	p.wg.Add(2)
	go p.readLoop()
	go p.writeLoop()
	go func() {
		p.wg.Wait()
		close(p.doneCh)
	}()
}

// Send enqueues a framed payload for the writer. Returns an error only
// when the pump is shutting down.
func (p *Pump) Send(frame []byte) error {
	select {
	case <-p.quitCh:
		return errors.New("tradingview: pump closed")
	case p.sendCh <- frame:
		return nil
	}
}

// Close starts graceful shutdown and blocks until both goroutines exit.
// Safe to call multiple times.
//
// Only the writer goroutine touches the underlying conn. Close signals
// via quitCh so the writer itself sends the WebSocket close frame and
// closes the conn, which keeps gorilla/websocket's single-writer
// invariant intact.
func (p *Pump) Close() error {
	p.initiateClose()
	<-p.doneCh
	if e := p.errSlot.Load(); e != nil {
		return *e
	}
	return nil
}

func (p *Pump) initiateClose() {
	p.closeOnce.Do(func() { close(p.quitCh) })
}

// Done closes when both goroutines have exited.
func (p *Pump) Done() <-chan struct{} { return p.doneCh }

// Err returns the first fatal error observed. Populated only after Done
// has fired.
func (p *Pump) Err() error {
	if e := p.errSlot.Load(); e != nil {
		return *e
	}
	return nil
}

func (p *Pump) recordErr(err error) {
	if err == nil {
		return
	}
	// atomic.Pointer has no CAS; a nil-check first-writer-wins is close
	// enough because reader and writer rarely race on this slot.
	if p.errSlot.Load() == nil {
		p.errSlot.Store(&err)
	}
}

func (p *Pump) readLoop() {
	defer p.wg.Done()
	// A server-initiated close must also trigger the writer to exit.
	defer p.initiateClose()
	for {
		_, data, err := p.conn.ReadMessage()
		if err != nil {
			if !isExpectedCloseErr(err) {
				p.recordErr(fmt.Errorf("tradingview: read: %w", err))
			}
			return
		}
		envs, err := protocol.Decode(data)
		if err != nil {
			p.logger.Warn("decode frame", "err", err, "raw", truncate(data, 80))
			continue
		}
		for _, env := range envs {
			p.dispatch(env)
		}
	}
}

func (p *Pump) dispatch(env protocol.Envelope) {
	if env.IsPing() {
		// Echo heartbeat via the writer. Non-blocking on purpose: if the
		// send buffer is full we skip this beat since TradingView will
		// retry with the next heartbeat anyway.
		select {
		case p.sendCh <- protocol.EncodeHeartbeat(env.Ping):
		default:
			p.logger.Warn("heartbeat send buffer full; dropping")
		}
		return
	}
	if sid := env.SessionID(); sid != "" {
		if h, ok := p.reg.Lookup(sid); ok {
			h(env)
			return
		}
	}
	if p.onOther != nil {
		p.onOther(env)
	}
}

func (p *Pump) writeLoop() {
	defer p.wg.Done()
	// The writer owns the conn, including closing it, so the reader
	// unblocks from ReadMessage once we're done.
	defer func() { _ = p.conn.Close() }()
	for {
		select {
		case <-p.quitCh:
			_ = p.conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			)
			p.drainSend()
			return
		case frame := <-p.sendCh:
			if err := p.conn.WriteMessage(websocket.TextMessage, frame); err != nil {
				if !isExpectedCloseErr(err) {
					p.recordErr(fmt.Errorf("tradingview: write: %w", err))
				}
				return
			}
		}
	}
}

func (p *Pump) drainSend() {
	// Best-effort: flush anything already queued, bounded by a short
	// timer so shutdown can't wedge on a full buffer.
	deadline := time.NewTimer(100 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case frame := <-p.sendCh:
			_ = p.conn.WriteMessage(websocket.TextMessage, frame)
		case <-deadline.C:
			return
		default:
			return
		}
	}
}

func isExpectedCloseErr(err error) bool {
	if err == nil {
		return true
	}
	if websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived,
		websocket.CloseAbnormalClosure) {
		return true
	}
	// net.ErrClosed surfaces when the writer has already closed the conn.
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	// gorilla surfaces the close-sequence race as a plain io error.
	if strings.Contains(err.Error(), "use of closed network connection") {
		return true
	}
	return false
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
