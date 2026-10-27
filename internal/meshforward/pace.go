package meshforward

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"time"
)

type pacer struct {
	mu            sync.Mutex
	rate          int64
	burst, tokens float64
	at            time.Time
}

func newPacer(rate int64) (*pacer, error) {
	if rate < 64<<10 || rate > MaxRate {
		return nil, errors.New("invalid TCP forward rate")
	}
	burst := float64(min(rate, 64<<10))
	return &pacer{rate: rate, burst: burst, tokens: burst, at: time.Now()}, nil
}

func (p *pacer) spend(ctx context.Context, n int) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		now := time.Now()
		p.tokens = math.Min(p.burst, p.tokens+now.Sub(p.at).Seconds()*float64(p.rate))
		p.at = now
		if p.tokens >= float64(n) {
			p.tokens -= float64(n)
			p.mu.Unlock()
			return nil
		}
		wait := time.Duration(math.Ceil((float64(n) - p.tokens) / float64(p.rate) * float64(time.Second)))
		p.mu.Unlock()
		timer := time.NewTimer(max(time.Millisecond, wait))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type pacedWriter struct {
	ctx    context.Context
	dst    net.Conn
	pacer  *pacer
	check  func() error
	credit func(context.Context, int) error
}

func (w pacedWriter) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		n := min(len(data), 32<<10)
		if err := w.pacer.spend(w.ctx, n); err != nil {
			return total, err
		}
		if err := w.check(); err != nil {
			return total, err
		}
		if err := w.credit(w.ctx, n); err != nil {
			return total, err
		}
		// Revocation after prepayment burns credit. It never authorizes a write.
		if err := w.check(); err != nil {
			return total, err
		}
		if err := w.ctx.Err(); err != nil {
			return total, err
		}
		written, err := w.dst.Write(data[:n])
		total += written
		if err != nil {
			return total, err
		}
		if written != n {
			return total, io.ErrShortWrite
		}
		data = data[n:]
	}
	return total, nil
}

func copyPaced(ctx context.Context, dst, src net.Conn, p *pacer, check func() error, credit func(context.Context, int) error) (int64, error) {
	return io.CopyBuffer(pacedWriter{ctx: ctx, dst: dst, pacer: p, check: check, credit: credit}, struct{ io.Reader }{src}, make([]byte, 32<<10))
}
