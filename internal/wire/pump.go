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

// Conn is the minimal websocket interface the pump depends on, satisfied by
// *websocket.Conn. Narrowed so tests can stub without pulling in httptest.
type Conn interface {
	ReadMessage() (messageType int, p []byte, err error)
	WriteMessage(messageType int, data []byte) error
	Close() error
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// UnsolicitedHandler is invoked for envelopes that have no matching session
// id — typically the server's connection hello, or data the server pushes
// before a session is created.
type UnsolicitedHandler func(env protocol.Envelope)

// Pump runs the reader + writer + dispatcher goroutines for one Conn.
// It is constructed once per Client lifetime.
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

// Options configure a new Pump.
type Options struct {
	Conn               Conn
	Logger             *slog.Logger
	Registry           *Registry
	Unsolicited        UnsolicitedHandler
	SendBuffer         int
	ShutdownDrainLimit time.Duration
}

// NewPump wires up a Pump. It does not start goroutines; call Start.
func NewPump(opts Options) *Pump {
	if opts.SendBuffer <= 0 {
		opts.SendBuffer = 256
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	p := &Pump{
		conn:    opts.Conn,
		logger:  opts.Logger,
		reg:     opts.Registry,
		onOther: opts.Unsolicited,
		sendCh:  make(chan []byte, opts.SendBuffer),
		quitCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	return p
}

// Start launches reader and writer goroutines.
func (p *Pump) Start() {
	p.wg.Add(2)
	go p.readLoop()
	go p.writeLoop()
	go func() {
		p.wg.Wait()
		close(p.doneCh)
	}()
}

// Send enqueues a framed payload for the writer goroutine.
// Returns an error only if the pump is shutting down.
func (p *Pump) Send(frame []byte) error {
	select {
	case <-p.quitCh:
		return errors.New("tradingview: pump closed")
	case p.sendCh <- frame:
		return nil
	}
}

// Close initiates graceful shutdown. Safe to call multiple times.
// Blocks until both goroutines exit.
//
// Only the writer goroutine touches the underlying conn; Close signals via
// quitCh so the writer sends the close frame and closes the conn itself.
// This keeps gorilla/websocket's single-writer invariant intact.
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

// Done fires when both goroutines have exited.
func (p *Pump) Done() <-chan struct{} { return p.doneCh }

// Err reports the first fatal error, available after Done fires.
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
	// CompareAndSwap would be right, but Pointer has no CAS — a first-writer
	// wins via a nil check is close enough (goroutines rarely race here).
	if p.errSlot.Load() == nil {
		p.errSlot.Store(&err)
	}
}

func (p *Pump) readLoop() {
	defer p.wg.Done()
	// Server-initiated close must also trigger writer shutdown.
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
		// Echo heartbeat via the writer. Non-blocking: if the send buffer is
		// full, drop — TradingView tolerates a missed beat and the next
		// heartbeat will recover.
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
	// Only the writer touches the conn; it also owns shutdown of the conn
	// so the reader unblocks from ReadMessage.
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
	// Short, bounded drain — don't block shutdown on a full buffer.
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
	// net.ErrClosed surfaces when the writer has already closed the conn;
	// not a real fault.
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	// gorilla wraps the close-sequence race as a plain io error string.
	if strings.Contains(err.Error(), "use of closed network connection") {
		return true
	}
	return false
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
