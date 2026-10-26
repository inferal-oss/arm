package meshsshrelay

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type boundedConn struct {
	abort func() error
	net.Conn
	readMu, writeMu       sync.Mutex
	readBytes, writeBytes int64
	max                   int64
	last                  atomic.Int64
	elapsed               func() time.Duration
	cancel                context.CancelFunc
	once                  sync.Once
}

func newBounded(c net.Conn, cancel context.CancelFunc, abort func() error) *boundedConn {
	// time.Since retains the epoch's monotonic component. Never serialize wall
	// time for inactivity: clock corrections must not extend a live stream.
	epoch := time.Now()
	b := &boundedConn{Conn: c, max: MaxStreamBytes, cancel: cancel, abort: abort, elapsed: func() time.Duration { return time.Since(epoch) }}
	return b
}
func (c *boundedConn) Close() error {
	var err error
	c.once.Do(func() {
		c.cancel()
		if c.abort != nil {
			_ = c.abort()
		}
		err = c.Conn.Close()
	})
	return err
}
func (c *boundedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if c.readBytes >= c.max {
		_ = c.Close()
		return 0, fmt.Errorf("SSH relay incoming byte limit reached")
	}
	if int64(len(p)) > c.max-c.readBytes {
		p = p[:c.max-c.readBytes]
	}
	n, err := c.Conn.Read(p)
	c.readBytes += int64(n)
	if n > 0 {
		c.last.Store(int64(c.elapsed()))
	}
	return n, err
}
func (c *boundedConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for len(p) > 0 {
		if c.writeBytes >= c.max {
			_ = c.Close()
			return total, fmt.Errorf("SSH relay outgoing byte limit reached")
		}
		nmax := min(int64(len(p)), int64(MaxPayloadBytes), c.max-c.writeBytes)
		n, err := c.Conn.Write(p[:nmax])
		total += n
		c.writeBytes += int64(n)
		p = p[n:]
		if n > 0 {
			c.last.Store(int64(c.elapsed()))
		}
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}
func (c *boundedConn) idleFor() time.Duration {
	return c.elapsed() - time.Duration(c.last.Load())
}
func (c *boundedConn) watch(ctx context.Context, check func() error) {
	tick := time.NewTicker(max(10*time.Millisecond, min(time.Second, MaxIdle/4)))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = c.Close()
			return
		case <-tick.C:
			if c.idleFor() >= MaxIdle || check != nil && check() != nil {
				_ = c.Close()
				return
			}
		}
	}
}

type socketAbort struct {
	mu     sync.Mutex
	conn   net.Conn
	closed bool
}

func (s *socketAbort) set(c net.Conn) {
	if t, ok := c.(*tls.Conn); ok {
		c = t.NetConn()
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = c.Close()
		return
	}
	s.conn = c
	s.mu.Unlock()
}
func (s *socketAbort) close() {
	s.mu.Lock()
	s.closed = true
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

type captureHijacker struct {
	http.ResponseWriter
	socket socketAbort
}

func (w *captureHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, b, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.socket.set(c)
	}
	return c, b, err
}
