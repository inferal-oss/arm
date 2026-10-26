package meshsshrelay

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"sync"
	"time"
)

// Pacer spends one reviewed transit byte-rate budget across both forwarding
// directions. Copies use fixed 32-KiB buffers, so a blocked writer never
// accumulates an unbounded queue behind the limiter.
type Pacer struct {
	mu     sync.Mutex
	rate   int64
	burst  float64
	tokens float64
	at     time.Time
}

func NewPacer(bytesPerSecond int64) (*Pacer, error) {
	if bytesPerSecond < 1 || bytesPerSecond > 1<<30 {
		return nil, errors.New("invalid bounded SSH relay byte rate")
	}
	burst := float64(min(bytesPerSecond, 64<<10))
	return &Pacer{rate: bytesPerSecond, burst: burst, tokens: burst, at: time.Now()}, nil
}

func (p *Pacer) wait(ctx context.Context, n int) error {
	if p == nil || n < 0 || float64(n) > p.burst {
		return errors.New("invalid bounded SSH relay pacing request")
	}
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
		remaining := float64(n) - p.tokens
		p.mu.Unlock()
		delay := time.Duration(math.Ceil(remaining / float64(p.rate) * float64(time.Second)))
		timer := time.NewTimer(max(time.Millisecond, delay))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type pacedWriter struct {
	ctx   context.Context
	dst   io.Writer
	pacer *Pacer
}

func (w pacedWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		chunk := min(len(p), 32<<10)
		if err := w.pacer.wait(w.ctx, chunk); err != nil {
			return total, err
		}
		n, err := w.dst.Write(p[:chunk])
		total += n
		if err != nil {
			return total, err
		}
		if n != chunk {
			return total, io.ErrShortWrite
		}
		p = p[chunk:]
	}
	return total, nil
}

// CopyPaced does not use an optional WriterTo fast path that could skip the
// shared pacer. Socket closure and ctx cancellation interrupt reads and waits.
func CopyPaced(ctx context.Context, dst, src net.Conn, pacer *Pacer) (int64, error) {
	return io.CopyBuffer(pacedWriter{ctx: ctx, dst: dst, pacer: pacer}, struct{ io.Reader }{src}, make([]byte, 32<<10))
}
