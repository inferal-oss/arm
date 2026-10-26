package meshsshrelay

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestTransitPacerSharesBothDirectionsAndCancelsWait(t *testing.T) {
	p, err := NewPacer(1)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.wait(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err = p.wait(ctx, 1); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("shared transit rate did not block and cancel promptly", err)
	}
	if err = p.wait(t.Context(), 2); err == nil {
		t.Fatal("pacing request larger than reviewed burst was accepted")
	}
}

func TestPacedWriterCannotBypassSharedBidirectionalBudget(t *testing.T) {
	pacer, err := NewPacer(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	first := pacedWriter{ctx: t.Context(), dst: io.Discard, pacer: pacer}
	if n, err := first.Write(make([]byte, 64<<10)); err != nil || n != 64<<10 {
		t.Fatalf("first transit direction: %d bytes, %v", n, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	second := pacedWriter{ctx: ctx, dst: io.Discard, pacer: pacer}
	if n, err := second.Write(make([]byte, 32<<10)); n != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("opposite direction bypassed shared byte rate: %d bytes, %v", n, err)
	}
}

func TestOutgoingCarrierCloseWaitsForCurrentRevalidation(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := newBounded(left, cancel, nil)
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c.startWatch(ctx, func() error {
		close(entered)
		<-release
		close(exited)
		return errors.New("current relay grant revoked")
	})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("outgoing revalidation worker did not enter")
	}
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("carrier close returned before revalidation worker joined")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("revalidation worker did not exit")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("carrier close did not join revalidation worker")
	}
}
