package manager

import (
	"context"
	"errors"
	"net"
	"os"
	"time"

	"getarm.org/internal/model"
)

type forwardUnixAcceptedClose struct {
	closing bool
	err     error
	done    chan struct{}
}

// AcceptOriginal is a private source-worker seam. It never publishes the
// listener or its pathname. The returned accepted FD must be tracked and
// joined by the caller; it grants no right to read application bytes until
// pinned destination admission and CheckBeforeApplicationBytes both succeed.
func (b *ForwardUnixBound) AcceptOriginal(ctx context.Context) (*net.UnixConn, error) {
	if b == nil || ctx == nil || ctx.Err() != nil {
		return nil, forwardUnixPairUnavailable()
	}
	b.acceptMu.Lock()
	defer b.acceptMu.Unlock()
	if err := b.CheckBeforeApplicationBytes(ctx); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := b.listener.SetDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			return nil, b.fault(err)
		}
		local, err := b.listener.AcceptUnix()
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) && b.admissionStopped.Load() {
				return nil, errors.Join(ctx.Err(), err)
			}
			return nil, b.fault(err)
		}
		if err := b.CheckBeforeApplicationBytes(ctx); err != nil {
			return nil, errors.Join(err, b.closeRejectedOriginal(local))
		}
		uid, err := forwardUnixPeerUID(local)
		if err != nil || uid != uint32(os.Geteuid()) {
			if closeErr := b.closeRejectedOriginal(local); closeErr != nil {
				return nil, errors.Join(err, closeErr)
			}
			continue
		}
		if b.beforeAcceptedRegisterTest != nil {
			b.beforeAcceptedRegisterTest()
		}
		if err := b.CheckBeforeApplicationBytes(ctx); err != nil {
			return nil, errors.Join(err, b.closeRejectedOriginal(local))
		}
		b.nativeMu.Lock()
		if b.pair.native != b || b.pair.data.Phase != "bound" || b.admissionStopped.Load() || b.pair.child.Context().Err() != nil || ctx.Err() != nil {
			b.nativeMu.Unlock()
			return nil, errors.Join(forwardUnixPairUnavailable(), b.closeRejectedOriginal(local))
		}
		b.acceptedMu.Lock()
		if b.accepted == nil {
			b.accepted = make(map[*net.UnixConn]*forwardUnixAcceptedClose)
		}
		b.accepted[local] = &forwardUnixAcceptedClose{done: make(chan struct{})}
		b.acceptedMu.Unlock()
		b.nativeMu.Unlock()
		return local, nil
	}
}

// SourceBinding returns only the frozen source route metadata. It never
// exposes the original socket pathname, listener, token or accepted bytes.
func (b *ForwardUnixBound) SourceBinding(ctx context.Context) (model.ForwardSourceBinding, error) {
	if err := b.CheckBeforeApplicationBytes(ctx); err != nil {
		return model.ForwardSourceBinding{}, err
	}
	b.nativeMu.Lock()
	defer b.nativeMu.Unlock()
	if b.pair.native != b || b.pair.data.Phase != "bound" {
		return model.ForwardSourceBinding{}, forwardUnixPairUnavailable()
	}
	return b.pair.data.SourceBinding, nil
}

func (b *ForwardUnixBound) SourceLeaseID() string {
	if b == nil || b.pair == nil {
		return ""
	}
	return b.pair.source
}

// MarkOriginalTransportCloseUncertain keeps both original source and storage
// obligations charged when the remote raw carrier cannot prove its close.
// The worker must call it before closing the join supplied to BindOriginal.
func (b *ForwardUnixBound) MarkOriginalTransportCloseUncertain() error {
	if b == nil || b.pair == nil || b.pair.child == nil {
		return forwardUnixPairUnavailable()
	}
	return b.pair.child.MarkCleanupUncertain("original forward relay transport close uncertain")
}

// StopOriginalAdmission interrupts the one original accept loop without
// settling either charge. The worker must join every accepted original before
// it closes the join supplied to BindOriginal.
func (b *ForwardUnixBound) StopOriginalAdmission() error {
	if b == nil || b.pair == nil {
		return forwardUnixPairUnavailable()
	}
	return b.closeOriginal()
}

func (b *ForwardUnixBound) closeRejectedOriginal(local *net.UnixConn) error {
	if local == nil {
		return nil
	}
	if err := local.Close(); err != nil {
		return b.fault(err)
	}
	return nil
}

// CloseAcceptedOriginal records an uncertain native close before a worker
// reports its join. A caller must invoke it exactly once per accepted FD.
func (b *ForwardUnixBound) CloseAcceptedOriginal(local *net.UnixConn) error {
	if b == nil || local == nil {
		return forwardUnixPairUnavailable()
	}
	b.acceptedMu.Lock()
	state := b.accepted[local]
	if state == nil {
		b.acceptedMu.Unlock()
		return forwardUnixPairUnavailable()
	}
	if state.closing {
		b.acceptedMu.Unlock()
		<-state.done
		return state.err
	}
	state.closing = true
	b.acceptedMu.Unlock()
	err := local.Close()
	b.acceptedMu.Lock()
	state.err = err
	if err == nil {
		delete(b.accepted, local)
	}
	close(state.done)
	b.acceptedMu.Unlock()
	if err != nil {
		_ = b.pair.child.MarkCleanupUncertain("original forward Unix accepted descriptor close uncertain")
	}
	return err
}
