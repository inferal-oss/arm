package meshstream

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func streamJoinSignal(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal(message)
	}
}

func TestStreamCancellationAbortJoinsOriginalCallback(t *testing.T) {
	t.Run("prevented", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls atomic.Int32
		owner := newJoinedAbort(ctx, func() { calls.Add(1) })
		if !owner.stopAndJoin() {
			t.Fatal("live callback was not prevented")
		}
		cancel()
		if !owner.stopAndJoin() || calls.Load() != 0 {
			t.Fatal("prevented callback ran or lost its original receipt")
		}
	})
	t.Run("already-running", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		defer unblock()
		owner := newJoinedAbort(ctx, func() { close(entered); <-release })
		cancel()
		streamJoinSignal(t, entered, "original cancellation callback did not start")
		joined := make(chan bool, 1)
		go func() { joined <- owner.stopAndJoin() }()
		select {
		case <-joined:
			t.Fatal("Stop falsely joined an already-running socket abort")
		case <-time.After(20 * time.Millisecond):
		}
		unblock()
		select {
		case stopped := <-joined:
			if stopped {
				t.Fatal("already-running callback reported prevented")
			}
		case <-time.After(time.Second):
			t.Fatal("original socket abort did not join")
		}
	})
}

func TestStreamHandlerRetainsActualRevalidationWatchUntilJoined(t *testing.T) {
	limits := DefaultLimits()
	limits.Idle = 80 * time.Millisecond
	f := transportTest(t, limits, func(ctx context.Context, _ Scope, _ Control, _ net.Conn) { <-ctx.Done() })
	validate := f.handler.Revalidate
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	f.handler.Revalidate = func(ctx context.Context, scope Scope) error {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
		}
		return validate(ctx, scope)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := Dial(ctx, f.address, f.policy, f.q, limits)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	streamJoinSignal(t, entered, "actual mTLS stream watch did not start revalidation")
	f.handler.ClosePeer(f.q.Source)
	// ClosePeer returns without waiting for a managed job. The admitted handler
	// must nevertheless retain its own in-progress watch until it really returns.
	if f.handler.Active() != 1 {
		t.Fatal("actual running revalidation was dropped from admitted transport count")
	}
	unblock()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for f.handler.Active() != 0 {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("original revalidation watch did not join after its own release")
		}
	}
}
