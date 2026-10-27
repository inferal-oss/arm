package manager

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// Channel.Close models an SSH request blocked behind a stalled carrier. The
// original transport must close first, even when cancellation races an error.
type transportReviewChannel struct {
	*sourceTestChannel
	carrierClosed chan struct{}
	closes        atomic.Int32
}

func (c *transportReviewChannel) Close() error {
	<-c.carrierClosed
	c.closes.Add(1)
	return c.sourceTestChannel.Close()
}

func TestSyncSourceOriginalTransportCloserRetainsExactOriginal(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	ch := &transportReviewChannel{sourceTestChannel: newSourceTestChannel(), carrierClosed: make(chan struct{})}
	transportErr := errors.New("original carrier close result")
	var closes, joins atomic.Int32
	var transportOnce sync.Once
	_, h, err := m.AdmitSyncSource(t.Context(), i, rows, root, parent, control, func(context.Context) (SyncSourceChannel, error) {
		requests := make(chan *ssh.Request)
		close(requests)
		return SyncSourceChannel{Channel: ch, Requests: requests, CloseTransport: func() error {
			closes.Add(1)
			transportOnce.Do(func() { close(ch.carrierClosed) })
			return transportErr
		}, Join: func() error { joins.Add(1); return nil }}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.OriginalTransportCloser(h.ctx, ch); err == nil {
		t.Fatal("unopened transport lent")
	}
	entered := make(chan struct{})
	var runnerErr error
	if err := h.Start(func(ctx context.Context, original ssh.Channel, _ <-chan *ssh.Request) error {
		closeOriginal, err := h.OriginalTransportCloser(ctx, original)
		if err != nil {
			runnerErr = err
			close(entered)
			return err
		}
		if _, err := h.OriginalTransportCloser(context.WithValue(ctx, struct{}{}, true), original); err == nil {
			runnerErr = errors.New("derived context adopted")
		}
		if _, err := h.OriginalTransportCloser(ctx, newSourceTestChannel()); err == nil {
			runnerErr = errors.New("unrelated channel adopted")
		}
		forged := &SyncSourceHold{manager: m, lease: h.lease, transport: h.transport}
		if _, err := forged.OriginalTransportCloser(ctx, original); err == nil {
			runnerErr = errors.New("forged holder adopted")
		}
		// Expired execution authority cannot prevent cleanup of the captured tuple.
		h.cancel()
		m.mu.Lock()
		m.closing = true
		m.mu.Unlock()
		again, err := h.OriginalTransportCloser(ctx, original)
		m.mu.Lock()
		m.closing = false
		m.mu.Unlock()
		if err != nil {
			runnerErr = err
		} else if !errors.Is(again(), transportErr) {
			runnerErr = errors.New("original first close result lost")
		}
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				if !errors.Is(closeOriginal(), transportErr) {
					t.Error("concurrent closer lost first result")
				}
			})
		}
		wg.Wait()
		close(entered)
		return errors.Join(runnerErr, transportErr)
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	select {
	case <-entered:
	case <-deadline.C:
		t.Fatal("original close blocked behind channel close")
	}
	h.JoinTransport()
	if runnerErr != nil {
		t.Fatal(runnerErr)
	}
	if closes.Load() != 1 || ch.closes.Load() != 1 || joins.Load() != 1 {
		t.Fatalf("original witnesses: transport=%d channel=%d join=%d", closes.Load(), ch.closes.Load(), joins.Load())
	}
	if !errors.Is(h.result, transportErr) {
		t.Fatal("first transport error not retained")
	}
}
