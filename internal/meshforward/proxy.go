package meshforward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"time"
)

// Identity must come from the checked, pinned inner TLS peer certificate and
// current destination service binding, never from a route header or relay.
type Identity struct {
	SourcePin, SourceSPKI, TargetPin, TargetSPKI, Account string
}

// Proxy owns final destination TCP streams. Grant must read the current local
// grant on every call; a missing grant denies by default. The caller owns the
// authenticated carrier and must join this Open before settling its lease.
type Proxy struct {
	Dial        func(context.Context, string, string) (net.Conn, error) // nil uses net.Dialer
	CheckCredit func(context.Context, Grant) error
	Credit      func(context.Context, Grant, int) error // required durable destination-local prepayment

	mu     sync.Mutex
	closed bool
	active map[net.Conn]context.CancelFunc
	grants map[string]*grantUsage
	wg     sync.WaitGroup
}

type grantUsage struct {
	active  int
	expires time.Time
	pacer   *pacer
}

var ErrClosed = errors.New("TCP forward proxy stopped")

func (p *Proxy) Stop() {
	p.mu.Lock()
	p.closed = true
	active := make(map[net.Conn]context.CancelFunc, len(p.active))
	for c, cancel := range p.active {
		active[c] = cancel
	}
	p.mu.Unlock()
	for c, cancel := range active {
		cancel()
		_ = c.Close()
	}
	p.wg.Wait()
}

func (p *Proxy) Active() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.active)
}

// Open returns only after both copy workers, the revocation watcher, and the
// final socket have joined. The caller's route and Manager holds remain live
// until it returns. Any transfer failure is an unknown application outcome.
func (p *Proxy) Open(ctx context.Context, q Request, who Identity, stream net.Conn, grantNow func(context.Context) (Grant, error), ready func() error) error {
	if p == nil || p.Credit == nil || p.CheckCredit == nil || grantNow == nil || ready == nil || stream == nil || ctx == nil {
		return errors.New("authenticated TCP carrier and current grant required")
	}
	grant, err := grantNow(ctx)
	if err != nil {
		return err
	}
	if err = grant.Authorize(time.Now(), who.SourcePin, who.SourceSPKI, who.TargetPin, who.TargetSPKI, who.Account, q); err != nil {
		return err
	}
	until := time.Now().Add(q.TTL)
	if grant.Expires.Before(until) {
		until = grant.Expires
	}
	life, cancel := context.WithDeadline(ctx, until)
	grantKey, err := grant.Digest()
	if err != nil {
		cancel()
		return err
	}
	p.mu.Lock()
	if p.grants == nil {
		p.grants = make(map[string]*grantUsage)
	}
	now := time.Now()
	for key, usage := range p.grants {
		if usage.active == 0 && !now.Before(usage.expires) {
			delete(p.grants, key)
		}
	}
	usage := p.grants[grantKey]
	if usage == nil && len(p.grants) >= 256 {
		p.mu.Unlock()
		cancel()
		return ErrClosed
	}
	if usage == nil {
		shared, e := newPacer(grant.BytesPerSecond)
		if e != nil {
			p.mu.Unlock()
			cancel()
			return e
		}
		usage = &grantUsage{expires: grant.Expires, pacer: shared}
		p.grants[grantKey] = usage
	}
	if p.closed || usage.active >= grant.MaxConnections {
		p.mu.Unlock()
		cancel()
		return ErrClosed
	}
	if p.active == nil {
		p.active = make(map[net.Conn]context.CancelFunc)
	}
	if _, exists := p.active[stream]; exists {
		p.mu.Unlock()
		cancel()
		return errors.New("duplicate owned TCP stream")
	}
	p.active[stream] = cancel
	usage.active++
	p.wg.Add(1)
	p.mu.Unlock()
	defer func() {
		cancel()
		_ = stream.Close()
		p.mu.Lock()
		delete(p.active, stream)
		usage.active--
		p.mu.Unlock()
		p.wg.Done()
	}()

	dial := p.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 5 * time.Second}).DialContext
	}
	service, err := dial(life, grant.Endpoint.Network, grant.Endpoint.Address)
	if err != nil {
		return err
	}
	defer service.Close()
	recheck := func() error {
		current, err := grantNow(life)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, grant) {
			return errors.New("TCP destination grant or endpoint changed")
		}
		return current.Authorize(time.Now(), who.SourcePin, who.SourceSPKI, who.TargetPin, who.TargetSPKI, who.Account, q)
	}
	if err = recheck(); err != nil {
		return err
	}
	if err = p.CheckCredit(life, grant); err != nil {
		return err
	}
	if err = ready(); err != nil {
		return err
	}
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-life.Done():
				_ = stream.Close()
				_ = service.Close()
				return
			case <-tick.C:
				if recheck() != nil {
					cancel()
					_ = stream.Close()
					_ = service.Close()
					return
				}
			}
		}
	}()
	defer func() { cancel(); _ = stream.Close(); _ = service.Close(); <-watchDone }()
	// Each direction has its own byte ceiling, and both share one rate budget.
	fromSource := &limitedConn{Conn: stream, remaining: grant.BytesPerConnection}
	fromService := &limitedConn{Conn: service, remaining: grant.BytesPerConnection}
	type result struct{ err error }
	finished := make(chan result, 2)
	copyOne := func(dst, src net.Conn) {
		_, copyErr := copyPaced(life, dst, src, usage.pacer, recheck, func(ctx context.Context, n int) error {
			return p.Credit(ctx, grant, n)
		})
		if copyErr == nil || errors.Is(copyErr, io.EOF) {
			if half, ok := dst.(interface{ CloseWrite() error }); ok {
				_ = half.CloseWrite()
			}
		} else {
			cancel()
			_ = dst.Close()
			_ = src.Close()
		}
		finished <- result{copyErr}
	}
	go copyOne(service, fromSource)
	go copyOne(stream, fromService)
	first, second := <-finished, <-finished
	if first.err != nil {
		return fmt.Errorf("TCP forward outcome unknown: %w", first.err)
	}
	if second.err != nil {
		return fmt.Errorf("TCP forward outcome unknown: %w", second.err)
	}
	return nil
}

// limitedConn constrains bytes read before forwarding. A subsequent read
// fails instead of presenting a clean EOF as proof of complete delivery.
type limitedConn struct {
	net.Conn
	remaining int64
}

func (c *limitedConn) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, errors.New("TCP forward byte ceiling reached")
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.Conn.Read(p)
	c.remaining -= int64(n)
	return n, err
}
