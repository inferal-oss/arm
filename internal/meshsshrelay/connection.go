package meshsshrelay

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type boundedConn struct {
	abort  func() error
	native *socketAbort
	net.Conn
	readMu, writeMu       sync.Mutex
	readBytes, writeBytes int64
	max                   int64
	last                  atomic.Int64
	elapsed               func() time.Duration
	cancel                context.CancelFunc
	once                  sync.Once
	closeErr              error
	watchDone             chan struct{}
}

func newBounded(c net.Conn, cancel context.CancelFunc, abort func() error) *boundedConn {
	// time.Since retains the epoch's monotonic component. Never serialize wall
	// time for inactivity: clock corrections must not extend a live stream.
	epoch := time.Now()
	b := &boundedConn{Conn: c, max: MaxStreamBytes, cancel: cancel, abort: abort, elapsed: func() time.Duration { return time.Since(epoch) }}
	return b
}
func (c *boundedConn) Close() error {
	err := c.shutdown()
	if c.watchDone != nil {
		<-c.watchDone
	}
	return err
}
func (c *boundedConn) shutdown() error {
	c.once.Do(func() {
		c.cancel()
		if c.abort != nil {
			_ = c.abort()
		}
		c.closeErr = c.Conn.Close()
	})
	return c.closeErr
}

// NativeCloseProof returns the cached first raw socket close result.
func (c *boundedConn) NativeCloseProof() error {
	if c.native == nil {
		return fmt.Errorf("original relay socket close proof unavailable")
	}
	return c.native.closeOriginal()
}
func (c *boundedConn) startWatch(ctx context.Context, check func() error) {
	c.watchDone = make(chan struct{})
	go func() {
		defer close(c.watchDone)
		c.watch(ctx, check)
	}()
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
			_ = c.shutdown()
			return
		case <-tick.C:
			if c.idleFor() >= MaxIdle || check != nil && check() != nil {
				_ = c.shutdown()
				return
			}
		}
	}
}

type socketAbort struct {
	mu         sync.Mutex
	conn       net.Conn
	closed     bool
	proofKnown bool
	proofErr   error
}

// firstCloseConn is handed to the outgoing TLS/HTTP stack. Its implicit
// close paths and explicit abort therefore share one first native result.
type firstCloseConn struct {
	net.Conn
	once sync.Once
	err  error
}

func (c *firstCloseConn) Close() error {
	c.once.Do(func() { c.err = c.Conn.Close() })
	return c.err
}

func (s *socketAbort) set(c net.Conn) net.Conn {
	if t, ok := c.(*tls.Conn); ok {
		c = t.NetConn()
	}
	owned := &firstCloseConn{Conn: c}
	s.mu.Lock()
	if s.closed {
		err := owned.Close()
		if s.proofKnown {
			s.proofErr = errors.Join(s.proofErr, err)
		} else {
			s.proofErr = err
			s.proofKnown = true
		}
		s.mu.Unlock()
		return owned
	}
	s.conn = owned
	s.mu.Unlock()
	return owned
}
func (s *socketAbort) close() {
	_ = s.closeOriginal()
}

func (s *socketAbort) closeOriginal() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if !s.proofKnown && s.conn != nil {
		s.proofErr = s.conn.Close()
		s.proofKnown = true
	}
	if !s.proofKnown {
		return fmt.Errorf("original relay socket close proof unavailable")
	}
	return s.proofErr
}

func (s *socketAbort) closeFailure() error {
	s.mu.Lock()
	hasOriginal := s.conn != nil || s.proofKnown
	s.mu.Unlock()
	if !hasOriginal {
		return nil
	}
	if err := s.closeOriginal(); err != nil {
		return &NativeCloseUncertain{Cause: err}
	}
	return nil
}

type captureHijacker struct {
	http.ResponseWriter
	socket socketAbort
}

func (w *captureHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, b, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		// The server's TLS owner already exists before Hijack; keep returning
		// that stream. The outgoing DialTLSContext path installs our wrapper
		// before TLS is constructed and is the original-close proof source.
		w.socket.set(c)
	}
	return c, b, err
}
