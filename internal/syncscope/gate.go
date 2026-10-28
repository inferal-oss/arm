package syncscope

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
)

// Gate excludes one original-scope native writer or verifier at a time.
// It supplies ordering, not durable ownership or quota evidence.
type Gate struct {
	slot  chan struct{}
	epoch atomic.Uint64
}

var ErrBusy = errors.New("sync scope gate unavailable")

func NewGate() *Gate {
	g := &Gate{slot: make(chan struct{}, 1)}
	g.slot <- struct{}{}
	return g
}

func (g *Gate) BeginWrite(ctx context.Context) (*Writer, error) {
	if g == nil || ctx == nil {
		return nil, ErrBusy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.slot:
	}
	if err := ctx.Err(); err != nil {
		g.slot <- struct{}{}
		return nil, err
	}
	prior := g.epoch.Load()
	if prior == math.MaxUint64 {
		g.slot <- struct{}{}
		return nil, ErrBusy
	}
	g.epoch.Store(prior + 1)
	return &Writer{gate: g, priorEpoch: prior}, nil
}

func (g *Gate) TryVerify(ctx context.Context) (*Verifier, error) {
	if g == nil || ctx == nil {
		return nil, ErrBusy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-g.slot:
	default:
		return nil, ErrBusy
	}
	if err := ctx.Err(); err != nil {
		g.slot <- struct{}{}
		return nil, err
	}
	return &Verifier{gate: g, epoch: g.epoch.Load()}, nil
}

type Writer struct {
	gate       *Gate
	priorEpoch uint64
	released   atomic.Bool
}

func (w *Writer) Release() {
	if w != nil && w.gate != nil && !w.released.Swap(true) {
		w.gate.slot <- struct{}{}
	}
}

// Follows detects an intervening Manager writer after native verification.
func (w *Writer) Follows(v *Verifier) bool {
	return w != nil && v != nil && w.gate != nil && w.gate == v.gate &&
		!w.released.Load() && v.released.Load() && w.priorEpoch == v.epoch
}

type Verifier struct {
	gate     *Gate
	epoch    uint64
	released atomic.Bool
}

func (v *Verifier) Release() {
	if v != nil && v.gate != nil && !v.released.Swap(true) {
		v.gate.slot <- struct{}{}
	}
}
