package sharecodec

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/storage"
)

func TestBridgeCloseAndJoinWaitsForActualAbortCallback(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	defer server.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	var calls atomic.Int32
	b, err := NewBridge(ctx, server, func() { calls.Add(1); close(entered); var one [1]byte; _, _ = r.Read(one[:]); server.Close() })
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-entered
	done := make(chan struct{})
	go func() { b.CloseAndJoin(); close(done) }()
	select {
	case <-done:
		t.Fatal("returned before native abort callback joined")
	case <-time.After(25 * time.Millisecond):
	}
	w.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("joined callback left completion waiting")
	}
	b.CloseAndJoin()
	if calls.Load() != 1 {
		t.Fatal("abort replayed", calls.Load())
	}
}
func TestBridgeCloseAndJoinIsConcurrentAndRepeatable(t *testing.T) {
	for n := 0; n < 64; n++ {
		server, client := net.Pipe()
		b, err := NewBridge(context.Background(), server, func() { server.Close() })
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for k := 0; k < 3; k++ {
			wg.Add(1)
			go func() { defer wg.Done(); b.CloseAndJoin() }()
		}
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("concurrent stopped/joined watcher deadlocked", n)
		}
		b.CloseAndJoin()
		client.Close()
	}
}

func TestServeSetupFailureStillClosesAndDrainsOwnedOrigin(t *testing.T) {
	for _, name := range []string{"cancelled-context", "nil-context", "nil-stream", "nil-abort"} {
		t.Run(name, func(t *testing.T) {
			provider, err := storage.Open(filepath.Join(t.TempDir(), "managed"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer provider.Close()
			id := strings.Repeat("7", 32)
			identity, err := provider.Create(id)
			if err != nil {
				t.Fatal(err)
			}
			artifact := model.Artifact{Name: id, Path: provider.Path(id), Phase: "owned", Identity: &identity}
			engine, err := sharefs.New(sharefs.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			binding := sharefs.Binding{Authority: strings.Repeat("b", 32), Owner: uint32(os.Geteuid()), Storage: id, StorageRevision: 1, Share: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)}
			manifest, err := engine.Build(t.Context(), provider, artifact, binding)
			if err != nil {
				t.Fatal(err)
			}
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			entered := make(chan struct{})
			var once sync.Once
			origin, err := engine.Open(t.Context(), provider, artifact, manifest, sharefs.Scope{Source: strings.Repeat("e", 32), EnrollmentRevision: 1, Consumer: strings.Repeat("f", 32), Session: strings.Repeat("1", 32)}, func(_ context.Context, access sharefs.Access) error {
				if access.Operation == "stat" {
					once.Do(func() { close(entered); var b [1]byte; _, _ = reader.Read(b[:]) })
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer origin.Close()
			operation := make(chan error, 1)
			go func() { _, err := origin.Stat(context.Background(), ""); operation <- err }()
			<-entered
			stream, peer := net.Pipe()
			defer stream.Close()
			defer peer.Close()
			ctx := context.Background()
			abort := func() { stream.Close() }
			switch name {
			case "cancelled-context":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case "nil-context":
				ctx = nil
			case "nil-stream":
				stream = nil
			case "nil-abort":
				abort = nil
			}
			done := make(chan error, 1)
			go func() { done <- Serve(ctx, origin, stream, abort) }()
			until := time.Now().Add(time.Second)
			for {
				select {
				case err := <-done:
					t.Fatal("setup failure returned while owned native operation was still blocked", err)
				default:
				}
				wait, cancel := context.WithTimeout(t.Context(), time.Millisecond)
				err := origin.WaitDrained(wait)
				cancel()
				if errors.Is(err, context.DeadlineExceeded) {
					break // Close happened, but the real authority pipe remains blocked.
				}
				if time.Now().After(until) {
					t.Fatal("setup failure did not begin origin closure", err)
				}
				time.Sleep(time.Millisecond)
			}
			writer.Close()
			if err := <-operation; !errors.Is(err, sharefs.ErrClosed) {
				t.Fatal("operation escaped setup failure", err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("invalid setup succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("setup failure did not return after actual join")
			}
			if err := origin.WaitDrained(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
