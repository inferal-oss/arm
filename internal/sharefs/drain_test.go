package sharefs

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"
)

func assertOriginStillDraining(t *testing.T, o *Origin) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := o.WaitDrained(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unfinished work falsely drained", err)
	}
}
func TestOriginDrainIncludesOperationWaitingForAuthorityIO(t *testing.T) {
	e, p, a, b := fixture(t)
	m := built(t, e, p, a, b)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	entered := make(chan struct{})
	var once sync.Once
	guard := func(ctx context.Context, access Access) error {
		if access.Operation == "stat" {
			once.Do(func() { close(entered); var b [1]byte; _, _ = r.Read(b[:]) })
		}
		return nil
	}
	o := opened(t, e, p, a, m, guard)
	done := make(chan error, 1)
	go func() { _, err := o.Stat(context.Background(), ""); done <- err }()
	<-entered
	o.BeginClose()
	assertOriginStillDraining(t, o)
	// Native read in the authority hook has not returned merely because Close
	// invalidated the Origin or its engine session count fell.
	w.Close()
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if err := o.WaitDrained(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Stat(context.Background(), ""); !errors.Is(err, ErrClosed) {
		t.Fatal("new operation after close", err)
	}
}
func TestOriginDrainCountsOperationsBeforeGlobalGate(t *testing.T) {
	e, p, a, b := fixture(t)
	o := opened(t, e, p, a, built(t, e, p, a, b), permit)
	for n := 0; n < e.limits.Reads; n++ {
		e.gate <- struct{}{}
	}
	done := make(chan error, 1)
	go func() { _, err := o.Stat(context.Background(), ""); done <- err }()
	until := time.Now().Add(time.Second)
	for {
		o.mu.Lock()
		count := o.operations
		o.mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("operation did not register before gate")
		}
		time.Sleep(time.Millisecond)
	}
	o.Close()
	assertOriginStillDraining(t, o)
	<-e.gate
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	for len(e.gate) > 0 {
		<-e.gate
	}
	if err := o.WaitDrained(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestOriginDrainIndependentlyWaitsForNativeCacheFillAndPinnedBytes(t *testing.T) {
	e, p, a, b := fixture(t)
	o := opened(t, e, p, a, built(t, e, p, a, b), permit)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, release, err := e.cache.acquire(context.Background(), chunkKey{origin: o}, func() ([]byte, error) { close(entered); var b [1]byte; _, err := r.Read(b[:]); return b[:], err })
		if release != nil {
			release()
		}
		done <- err
	}()
	<-entered
	o.Close()
	assertOriginStillDraining(t, o)
	w.Close()
	<-done
	if err := o.WaitDrained(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A direct package-internal pinned-cache fixture proves WaitDrained checks
	// reservations rather than relying solely on the public operation counter.
	o2 := opened(t, e, p, a, built(t, e, p, a, b), permit)
	_, release, err := e.cache.acquire(context.Background(), chunkKey{origin: o2}, func() ([]byte, error) { return []byte("verified fixture"), nil })
	if err != nil {
		t.Fatal(err)
	}
	o2.Close()
	assertOriginStillDraining(t, o2)
	release()
	if err := o2.WaitDrained(context.Background()); err != nil {
		t.Fatal(err)
	}
}
