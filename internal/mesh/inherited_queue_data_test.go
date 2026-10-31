package mesh

import (
	"context"
	"getarm.org/internal/residentrefs"
	"runtime"
	"testing"
	"time"
)

// Queue DATA only. No delegated/materialized custody, verified image/fence,
// actual native holder, grant, or authority is fabricated. A resumed waiter
// fails the original-state gate; only normal queue publication is exercised.
func TestInheritedQueueDataCancellationCapacityPublicationAndAbnormal(t *testing.T) {
	newData := func() (*InheritedImagePending, *inheritedImageState, *inheritedImageOperation) {
		p := &InheritedImagePending{}
		s := &inheritedImageState{self: p, rootContext: context.Background()}
		p.state = s
		op := &inheritedImageOperation{kind: "data-only", done: make(chan struct{})}
		s.active = op
		return p, s, op
	}
	waitCount := func(t *testing.T, s *inheritedImageState, n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			s.mu.Lock()
			count := len(s.waiters)
			s.mu.Unlock()
			if count == n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("queue count %d, expected %d", count, n)
			}
			runtime.Gosched()
		}
	}
	t.Run("cancel-one-and-publish-other", func(t *testing.T) {
		p, s, op := newData()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		one, cancelOne := context.WithCancel(ctx)
		defer cancelOne()
		first := make(chan error, 1)
		second := make(chan error, 1)
		go func() { ctx := one; _, err := p.begin(ctx, "proof"); first <- err }()
		go func() { _, err := p.begin(ctx, "proof"); second <- err }()
		waitCount(t, s, 2)
		cancelOne()
		select {
		case err := <-first:
			if err != context.Canceled {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("cancelled waiter did not return")
		}
		waitCount(t, s, 1)
		s.mu.Lock()
		unchanged := s.active == op && s.firstError == nil
		s.mu.Unlock()
		if !unchanged {
			t.Fatal("waiter cancellation changed original")
		}
		normal := true
		var resultErr error
		s.finish(op, &normal, &resultErr)
		select {
		case err := <-second:
			if err != errResidentInheritedImage {
				t.Fatal("inert resumed waiter", err)
			}
		case <-ctx.Done():
			t.Fatal("normal publication did not resume waiter")
		}
		waitCount(t, s, 0)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.active != nil || s.firstError != nil {
			t.Fatal("normal DATA publication poisoned owner")
		}
	})
	t.Run("capacity", func(t *testing.T) {
		p, s, op := newData()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		results := make(chan error, 64)
		for i := 0; i < 64; i++ {
			go func() { _, err := p.begin(ctx, "proof"); results <- err }()
		}
		waitCount(t, s, 64)
		if _, err := p.begin(ctx, "proof"); err != residentrefs.ErrBusy {
			t.Fatal(err)
		}
		s.mu.Lock()
		unchanged := s.active == op && s.firstError == nil && len(s.waiters) == 64
		s.mu.Unlock()
		if !unchanged {
			t.Fatal("capacity refusal changed original")
		}
		cancel()
		for i := 0; i < 64; i++ {
			select {
			case err := <-results:
				if err != context.Canceled {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("capacity waiter did not exit")
			}
		}
		waitCount(t, s, 0)
	})
	t.Run("abnormal-remains-pending", func(t *testing.T) {
		_, s, op := newData()
		normal := false
		var resultErr error
		s.finish(op, &normal, &resultErr)
		s.mu.Lock()
		retained := s.active == op && op.abnormal && s.firstError != nil
		s.mu.Unlock()
		if !retained {
			t.Fatal("abnormal original lost")
		}
		select {
		case <-op.done:
			t.Fatal("abnormal original published normal completion")
		default:
		}
	})
}
