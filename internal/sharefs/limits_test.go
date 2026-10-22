package sharefs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestGlobalOperationGateAndSessionBound(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	m := built(t, e, p, a, b)
	l := DefaultLimits()
	l.Reads = 1
	l.Sessions = 1
	small, err := New(l)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	g := func(_ context.Context, x Access) error {
		if x.Operation == "stat" {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}
	o := opened(t, small, p, a, m, g)
	if _, err := small.Open(context.Background(), p, a, m, scope(), permit); !errors.Is(err, ErrLimit) {
		t.Fatal("session memory bound not enforced")
	}
	done := make(chan error, 1)
	go func() { _, err := o.Stat(context.Background(), "file"); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := o.List(ctx, "", 0, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("metadata bypassed global I/O gate")
	}
	// Close does not wait for guard/native I/O or retain the bookkeeping mutex.
	o.Close()
	close(release)
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("closed in-flight operation: %v", err)
	}
	other, err := small.Open(context.Background(), p, a, m, scope(), permit)
	if err != nil {
		t.Fatal("closed session did not release its slot", err)
	}
	other.Close()
}

func TestGuardCancellationBeforeReturningData(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	m := built(t, e, p, a, b)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	g := func(_ context.Context, x Access) error {
		if x.Operation == "read" {
			calls++
			if calls == 3 {
				cancel()
			}
		}
		return nil
	}
	o := opened(t, e, p, a, m, g)
	if got, err := o.Read(ctx, handle(t, o, "file"), 0, 8); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled bytes returned %q %v", got, err)
	}
}

func TestNativeMountIdentityMustMatchPublishedRoot(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	m := built(t, e, p, a, b)
	f, err := os.Open("/dev")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	mount, err := nativeMountIdentity(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if mount == m.Entries[0].Version.Mount {
		t.Skip("/dev shares the fixture mount: native distinct-mount observation unavailable")
	}
	m.Entries[1].Version.Mount = mount
	m.SHA256, _, _ = manifestHash(m)
	if _, err := e.Open(context.Background(), p, a, m, scope(), permit); err == nil {
		t.Fatal("actual foreign mount identity accepted")
	}
}

func TestFinalGuardCannotCloseAndReturnMetadata(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	m := built(t, e, p, a, b)
	var o *Origin
	calls := 0
	g := func(_ context.Context, x Access) error {
		if x.Operation == "stat" {
			calls++
			if calls == 3 {
				o.Close()
			}
		}
		return nil
	}
	o = opened(t, e, p, a, m, g)
	if got, err := o.Stat(context.Background(), "file"); !errors.Is(err, ErrClosed) || got.Path != "" {
		t.Fatalf("closed final guard returned metadata: %#v %v", got, err)
	}
}

func TestReadOnlyBuildRejectsSparseOver32GiBBeforeHashing(t *testing.T) {
	e, p, a, b := fixture(t)
	f, err := os.Create(filepath.Join(a.Path, "oversized"))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(int64(MaxChunks)*ChunkBytes + 1)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := e.Build(context.Background(), p, a, b); !errors.Is(err, ErrLimit) {
		t.Fatalf("oversized sparse tree accepted: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("refusal scanned oversized content")
	}
}

// This benchmark is a cache-warm local filesystem/hash baseline, NOT mounted or
// network performance acceptance. The public adapter gate must measure those.
func BenchmarkSequentialRead(b *testing.B) {
	e, p, a, binding := fixture(b)
	payload := bytes.Repeat([]byte("immutable-origin"), 1<<20)
	write(b, filepath.Join(a.Path, "file"), payload)
	m := built(b, e, p, a, binding)
	o := opened(b, e, p, a, m, permit)
	h := handle(b, o, "file")
	b.Run("native-warm", func(b *testing.B) {
		f, err := os.Open(filepath.Join(a.Path, "file"))
		if err != nil {
			b.Fatal(err)
		}
		defer f.Close()
		buf := make([]byte, ChunkBytes)
		b.SetBytes(ChunkBytes)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			off := int64(i%(len(payload)/ChunkBytes)) * ChunkBytes
			if _, err := f.ReadAt(buf, off); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("verified-warm", func(b *testing.B) {
		b.SetBytes(ChunkBytes)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			off := int64(i%(len(payload)/ChunkBytes)) * ChunkBytes
			got, err := o.Read(context.Background(), h, off, ChunkBytes)
			if err != nil || len(got) != ChunkBytes {
				b.Fatal(err)
			}
		}
	})
}
