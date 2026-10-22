package server

import (
	"context"
	"crypto/tls"
	"sync"
)

// workspaceRouteConn gates the FIRST application write after pinned inner TLS.
// No caller retries a request once this write starts, even if the network later
// loses its receipt. TLS handshake records precede this wrapper and cannot be
// mistaken for application dispatch. The carrier's ongoing validator checks
// current stable membership/deny/pins, not the lifetime of a selected graph.
type workspaceRouteConn struct {
	*tls.Conn
	ctx         context.Context
	beforeWrite func(context.Context) error
	mu          sync.Mutex
	started     bool
	failure     error
}

func (c *workspaceRouteConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil {
		return 0, c.failure
	}
	if !c.started {
		if err := c.beforeWrite(c.ctx); err != nil {
			c.failure = err
			_ = c.Conn.Close()
			return 0, err
		}
		c.started = true
	}
	return c.Conn.Write(p)
}
