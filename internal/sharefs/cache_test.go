package sharefs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifiedCacheHitStillChecksCurrentNativeAndScope(t *testing.T) {
	for _, mode := range []string{"mutated-file", "replaced-root", "provider-closed", "revoked", "closed"} {
		t.Run(mode, func(t *testing.T) {
			e, p, a, b := fixture(t)
			path := filepath.Join(a.Path, "payload")
			write(t, path, bytes.Repeat([]byte("approved"), 1000))
			m := built(t, e, p, a, b)
			var denied atomic.Bool
			o := opened(t, e, p, a, m, func(context.Context, Access) error {
				if denied.Load() {
					return os.ErrPermission
				}
				return nil
			})
			h := handle(t, o, "payload")
			for i := 0; i < 2; i++ {
				data, err := o.Read(context.Background(), h, 0, 13)
				if err != nil || string(data) != "approvedappro" {
					t.Fatalf("warm %q %v", data, err)
				}
			}
			e.cache.mu.Lock()
			if len(e.cache.entries) != 1 {
				t.Fatal("read did not reuse a verified chunk")
			}
			e.cache.mu.Unlock()
			switch mode {
			case "mutated-file":
				write(t, path, bytes.Repeat([]byte("SECRET!!"), 1000))
			case "replaced-root":
				if err := os.Rename(a.Path, a.Path+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(a.Path, 0700); err != nil {
					t.Fatal(err)
				}
				write(t, path, []byte("SECRET!!"))
			case "provider-closed":
				p.Close()
			case "revoked":
				denied.Store(true)
			case "closed":
				o.Close()
			}
			if data, err := o.Read(context.Background(), h, 0, 8); data != nil || err == nil {
				t.Fatalf("cached bytes escaped %q %v", data, err)
			}
		})
	}
}

func TestVerifiedCacheFinalGuardCannotReturnWarmBytes(t *testing.T) {
	for _, mode := range []string{"revoke", "mutate", "close"} {
		t.Run(mode, func(t *testing.T) {
			e, p, a, b := fixture(t)
			path := filepath.Join(a.Path, "payload")
			write(t, path, []byte("approved"))
			m := built(t, e, p, a, b)
			enabled := false
			reads := 0
			var o *Origin
			g := func(_ context.Context, x Access) error {
				if enabled && x.Operation == "read" {
					reads++
					if reads == 3 {
						switch mode {
						case "revoke":
							return os.ErrPermission
						case "mutate":
							write(t, path, []byte("SECRET!!"))
						case "close":
							o.Close()
						}
					}
				}
				return nil
			}
			o = opened(t, e, p, a, m, g)
			h := handle(t, o, "payload")
			if _, err := o.Read(context.Background(), h, 0, 8); err != nil {
				t.Fatal(err)
			}
			enabled = true
			if got, err := o.Read(context.Background(), h, 0, 8); got != nil || err == nil {
				t.Fatalf("final %s returned cached bytes %q %v", mode, got, err)
			}
		})
	}
}

func TestVerifiedCacheEvictionAndAuthorityBounds(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "payload"), bytes.Repeat([]byte("x"), 10*ChunkBytes))
	m := built(t, e, p, a, b)
	origins := make([]*Origin, 0, 5)
	for j := 0; j < 5; j++ {
		o := opened(t, e, p, a, m, permit)
		origins = append(origins, o)
		h := handle(t, o, "payload")
		for i := 0; i < 10; i++ {
			data, err := o.Read(context.Background(), h, int64(i*ChunkBytes), 5)
			if err != nil || string(data) != "xxxxx" {
				t.Fatal(err)
			}
		}
		e.cache.mu.Lock()
		if len(e.cache.entries) > 32 || e.cache.origins[o] > 8 {
			t.Fatal("cache byte/cardinality limit exceeded")
		}
		if j == 0 {
			for k := range e.cache.entries {
				if k.index < 2 {
					t.Fatal("old unpinned chunk not evicted")
				}
			}
		}
		e.cache.mu.Unlock()
	}
	for _, o := range origins {
		o.Close()
	}
	e.cache.mu.Lock()
	defer e.cache.mu.Unlock()
	if len(e.cache.entries) != 0 || len(e.cache.origins) != 0 {
		t.Fatal("Close retained session cache identity/bytes")
	}
}

// These tests exercise bookkeeping under deterministic stalled I/O. The cache
// does not grant native authority; actual origin/security tests above cover it.
func TestCacheCoalescesFillAndWaiterCancellation(t *testing.T) {
	e, p, a, b := fixture(t)
	m := built(t, e, p, a, b)
	o := opened(t, e, p, a, m, permit)
	k := chunkKey{origin: o, index: 1}
	entered, release := make(chan struct{}), make(chan struct{})
	var fills atomic.Int32
	fill := func() ([]byte, error) {
		if fills.Add(1) == 1 {
			close(entered)
		}
		<-release
		return []byte("approved"), nil
	}
	done := make(chan error, 8)
	go func() {
		data, unpin, err := e.cache.acquire(context.Background(), k, fill)
		if err == nil {
			if string(data) != "approved" {
				err = errors.New("bad fill")
			}
			unpin()
		}
		done <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		_, unpin, err := e.cache.acquire(ctx, k, fill)
		if unpin != nil {
			unpin()
		}
		cancelled <- err
	}()
	cancel()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter cancellation blocked by native fill")
	}
	for i := 0; i < 7; i++ {
		go func() {
			_, unpin, err := e.cache.acquire(context.Background(), k, fill)
			if unpin != nil {
				unpin()
			}
			done <- err
		}()
	}
	close(release)
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if fills.Load() != 1 {
		t.Fatalf("same chunk repeated fill %d", fills.Load())
	}
}
func TestCacheCloseKeepsStalledReservationUntilFillJoins(t *testing.T) {
	e, p, a, b := fixture(t)
	m := built(t, e, p, a, b)
	o := opened(t, e, p, a, m, permit)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, unpin, err := e.cache.acquire(context.Background(), chunkKey{origin: o}, func() ([]byte, error) { close(entered); <-release; return []byte("approved"), nil })
		if unpin != nil {
			unpin()
		}
		done <- err
	}()
	<-entered
	o.Close()
	e.cache.mu.Lock()
	if len(e.cache.entries) != 1 || e.cache.origins[o] != 1 {
		t.Fatal("native in-flight reservation freed before join")
	}
	e.cache.mu.Unlock()
	if _, _, err := e.cache.acquire(context.Background(), chunkKey{origin: o}, func() ([]byte, error) { panic("closed origin fill") }); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	e.cache.mu.Lock()
	defer e.cache.mu.Unlock()
	if len(e.cache.entries) != 0 || len(e.cache.origins) != 0 {
		t.Fatal("late fill resurrected closed session")
	}
}
func TestCachePinnedSlotsCannotBeEvictedOrOverallocated(t *testing.T) {
	e, p, a, b := fixture(t)
	m := built(t, e, p, a, b)
	o := opened(t, e, p, a, m, permit)
	var releases []func()
	for i := 0; i < 8; i++ {
		_, release, err := e.cache.acquire(context.Background(), chunkKey{origin: o, index: int64(i)}, func() ([]byte, error) { return []byte("approved"), nil })
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := e.cache.acquire(ctx, chunkKey{origin: o, index: 8}, func() ([]byte, error) { t.Error("allocated ninth pinned chunk"); return nil, nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	releases[0]()
	_, release, err := e.cache.acquire(context.Background(), chunkKey{origin: o, index: 8}, func() ([]byte, error) { return []byte("new chunk"), nil })
	if err != nil {
		t.Fatal(err)
	}
	release()
	for _, release := range releases {
		release()
	}
}
func TestCacheConcurrentReadCloseReopen(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "payload"), bytes.Repeat([]byte("x"), 2*ChunkBytes))
	m := built(t, e, p, a, b)
	for j := 0; j < 15; j++ {
		o := opened(t, e, p, a, m, permit)
		h := handle(t, o, "payload")
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Go(func() {
				for n := 0; n < 4; n++ {
					got, err := o.Read(context.Background(), h, int64(n*133), 1024)
					if err == nil && len(got) != 1024 {
						t.Error(fmt.Sprintf("short cached result %d", len(got)))
					}
				}
			})
		}
		o.Close()
		wg.Wait()
	}
	e.cache.mu.Lock()
	defer e.cache.mu.Unlock()
	if len(e.cache.entries) != 0 {
		t.Fatal("closed origins leaked cached bytes")
	}
}

func TestDeniedGuardPurgesReusableCacheBytes(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "payload"), []byte("approved"))
	m := built(t, e, p, a, b)
	denied := false
	o := opened(t, e, p, a, m, func(context.Context, Access) error {
		if denied {
			return os.ErrPermission
		}
		return nil
	})
	h := handle(t, o, "payload")
	if _, err := o.Read(context.Background(), h, 0, 8); err != nil {
		t.Fatal(err)
	}
	denied = true
	if _, err := o.Read(context.Background(), h, 0, 8); err == nil {
		t.Fatal("denied guard accepted")
	}
	e.cache.mu.Lock()
	defer e.cache.mu.Unlock()
	if len(e.cache.entries) != 0 {
		t.Fatal("guard denial retained reusable bytes")
	}
}
func TestCacheAuthorityPinnedBoundAcrossOrigins(t *testing.T) {
	e, p, a, b := fixture(t)
	m := built(t, e, p, a, b)
	var releases []func()
	for j := 0; j < 4; j++ {
		o := opened(t, e, p, a, m, permit)
		for i := 0; i < 8; i++ {
			_, release, err := e.cache.acquire(context.Background(), chunkKey{origin: o, index: int64(i)}, func() ([]byte, error) { return []byte("approved"), nil })
			if err != nil {
				t.Fatal(err)
			}
			releases = append(releases, release)
		}
	}
	other := opened(t, e, p, a, m, permit)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := e.cache.acquire(ctx, chunkKey{origin: other}, func() ([]byte, error) { t.Error("allocated beyond authority cache reservation"); return nil, nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	for _, release := range releases {
		release()
	}
}
