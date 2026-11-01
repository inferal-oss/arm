package server

import (
	"context"
	"net"
	"sync"
	"time"
)

type residentIngressConnectionKey struct{}

type residentIngressListener struct {
	mu                            sync.Mutex
	owner                         *residentIngressPending
	original                      net.Listener
	closeAttempted, closeReturned bool
	closeError                    error
}

type residentIngressConnection struct {
	owner                                              *residentIngressPending
	slot                                               int
	connection                                         net.Conn
	localAddress, remoteAddress                        net.Addr
	acceptAttempted, acceptReturned                    bool
	acceptError                                        error
	closeAttempted, closeReturned                      bool
	closeError                                         error
	handlerAttempted, handlerReturned, handlerAbnormal bool
	handlerCause                                       any
	settled                                            bool
	parkAttempted, parkReturned                        bool
}

func (l *residentIngressListener) validOriginal() bool {
	return l != nil && l.owner != nil && l.owner.original() && l.owner.bounded == l && l.original != nil && l.original == l.owner.listener
}

func (l *residentIngressListener) Accept() (net.Conn, error) {
	if !l.validOriginal() {
		return nil, errResidentServerOriginal
	}
	p := l.owner
	select {
	case <-p.ctx.Done():
		return nil, p.ctx.Err()
	case index := <-p.available:
		p.mu.Lock()
		if index < 0 || index >= len(p.slots) || p.slots[index] != nil || p.firstError != nil {
			p.mu.Unlock()
			return nil, errResidentServerOriginal
		}
		c := &residentIngressConnection{owner: p, slot: index, acceptAttempted: true}
		p.slots[index] = c // actual slot BEFORE original Accept, never accept-and-drop
		p.mu.Unlock()
		actual, err := l.original.Accept()
		p.mu.Lock()
		c.connection, c.acceptError, c.acceptReturned = actual, err, true
		if err != nil && p.firstError == nil {
			p.firstError = err
		}
		if actual == nil && p.firstError == nil {
			p.firstError = errResidentServerOriginal
		}
		p.mu.Unlock()
		if err != nil {
			return nil, err
		} // partial actual retained, no cleanup/retry credit
		if actual == nil {
			return nil, errResidentServerOriginal
		}
		c.localAddress, c.remoteAddress = actual.LocalAddr(), actual.RemoteAddr()
		return c, nil
	}
}

func (l *residentIngressListener) Addr() net.Addr {
	if !l.validOriginal() {
		return nil
	}
	return l.original.Addr()
}

func (l *residentIngressListener) Close() error {
	if !l.validOriginal() {
		return errResidentServerOriginal
	}
	l.mu.Lock()
	if l.closeAttempted {
		returned, err := l.closeReturned, l.closeError
		l.mu.Unlock()
		if !returned {
			return errResidentServerPending
		}
		return err // observe prior result only; never a second actual Close
	}
	l.closeAttempted = true
	l.mu.Unlock()
	err := l.original.Close()
	l.mu.Lock()
	l.closeError, l.closeReturned = err, true
	l.mu.Unlock()
	return err
}

func (c *residentIngressConnection) original() bool {
	if c == nil || c.owner == nil || c.slot < 0 || c.slot >= residentIngressConnectionLimit {
		return false
	}
	p := c.owner
	if !p.original() {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.self == p && p.slots[c.slot] == c && c.acceptAttempted && c.acceptReturned && c.acceptError == nil && c.connection != nil && !c.settled
}

func (c *residentIngressConnection) Read(b []byte) (int, error) {
	if !c.original() {
		return 0, errResidentServerOriginal
	}
	return c.connection.Read(b)
}
func (c *residentIngressConnection) Write(b []byte) (int, error) {
	if !c.original() {
		return 0, errResidentServerOriginal
	}
	return c.connection.Write(b)
}
func (c *residentIngressConnection) LocalAddr() net.Addr {
	if c == nil {
		return nil
	}
	return c.localAddress // immutable original DATA after Close
}
func (c *residentIngressConnection) RemoteAddr() net.Addr {
	if c == nil {
		return nil
	}
	return c.remoteAddress
}
func (c *residentIngressConnection) SetDeadline(t time.Time) error {
	if !c.original() {
		return errResidentServerOriginal
	}
	return c.connection.SetDeadline(t)
}
func (c *residentIngressConnection) SetReadDeadline(t time.Time) error {
	if !c.original() {
		return errResidentServerOriginal
	}
	return c.connection.SetReadDeadline(t)
}
func (c *residentIngressConnection) SetWriteDeadline(t time.Time) error {
	if !c.original() {
		return errResidentServerOriginal
	}
	return c.connection.SetWriteDeadline(t)
}

func (c *residentIngressConnection) Close() error {
	if c == nil || c.owner == nil || c.slot < 0 || c.slot >= residentIngressConnectionLimit {
		return errResidentServerOriginal
	}
	p := c.owner
	if !p.original() {
		return errResidentServerOriginal
	}
	p.mu.Lock()
	if c.closeAttempted {
		returned, err := c.closeReturned, c.closeError
		p.mu.Unlock()
		if !returned {
			return errResidentServerPending
		}
		return err
	}
	if p.slots[c.slot] != c || c.connection == nil || !c.acceptReturned || c.acceptError != nil || c.settled {
		p.mu.Unlock()
		return errResidentServerOriginal
	}
	c.closeAttempted = true
	p.mu.Unlock()
	err := c.connection.Close() // exact original reference; unreturned stays retained
	p.mu.Lock()
	c.closeError, c.closeReturned = err, true
	c.settleLocked()
	p.mu.Unlock()
	return err
}

// Caller holds owner.mu. This observes only actual reference/handler outcomes;
// it creates no native/runtime Closed or standing-resource reclamation credit.
func (c *residentIngressConnection) settleLocked() {
	p := c.owner
	if p.slots[c.slot] != c || c.settled || !c.closeAttempted || !c.closeReturned || c.closeError != nil || !c.handlerAttempted || !c.handlerReturned || c.handlerAbnormal {
		return
	}
	c.settled = true
	p.lastSettled[c.slot] = c
	p.slots[c.slot] = nil
	p.available <- c.slot
}

func (c *residentIngressConnection) beginHandler(ctx context.Context) error {
	if !c.original() || ctx == nil || ctx.Value(residentIngressConnectionKey{}) != c {
		return errResidentServerOriginal
	}
	p := c.owner
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.slots[c.slot] != c || c.handlerAttempted || c.closeAttempted {
		return errResidentServerOriginal
	}
	c.handlerAttempted = true
	return nil
}

// Directly deferred by the actual handler. Unknown/panic/Goexit holds this SAME
// original frame and slot; no detached cleanup, watcher or task reconstruction.
func (c *residentIngressConnection) finishHandler(normal *bool) {
	cause := recover()
	p := c.owner
	p.mu.Lock()
	c.handlerReturned, c.handlerAbnormal, c.handlerCause = *normal, !*normal, cause
	if *normal {
		c.settleLocked()
	}
	p.mu.Unlock()
	if *normal {
		return
	}
	for {
		p.mu.Lock()
		c.parkAttempted = true
		c.parkReturned = false
		p.mu.Unlock()
		time.Sleep(24 * time.Hour)
		p.mu.Lock()
		c.parkReturned = true
		p.mu.Unlock()
	}
}
