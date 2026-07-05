package wire

import (
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// silentConn is a Conn whose peer never sends anything. ReadMessage
// honours the read deadline like a real conn: it blocks until the
// deadline passes, then returns os.ErrDeadlineExceeded.
type silentConn struct {
	mu       sync.Mutex
	deadline time.Time
	closed   chan struct{}
	once     sync.Once
}

func newSilentConn() *silentConn {
	return &silentConn{closed: make(chan struct{})}
}

func (c *silentConn) ReadMessage() (int, []byte, error) {
	c.mu.Lock()
	d := c.deadline
	c.mu.Unlock()
	if d.IsZero() {
		<-c.closed
		return 0, nil, net.ErrClosed
	}
	timer := time.NewTimer(time.Until(d))
	defer timer.Stop()
	select {
	case <-timer.C:
		return 0, nil, os.ErrDeadlineExceeded
	case <-c.closed:
		return 0, nil, net.ErrClosed
	}
}

func (c *silentConn) WriteMessage(int, []byte) error { return nil }

func (c *silentConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *silentConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return nil
}

func (c *silentConn) SetWriteDeadline(time.Time) error { return nil }

func TestPumpTearsDownOnReadTimeout(t *testing.T) {
	p := NewPump(Options{
		Conn:        newSilentConn(),
		Registry:    NewRegistry(),
		ReadTimeout: 50 * time.Millisecond,
	})
	p.Start()

	select {
	case <-p.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("pump did not tear down on read timeout")
	}
	if p.Err() == nil {
		t.Fatal("want a fatal error after read timeout")
	}
}
