package distribution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelPayloadsBoundAndOrderedResults(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan int, 4)
	release := make(chan struct{})
	var active, peak atomic.Int32
	type result struct {
		items []Artifact
		err   error
	}
	done := make(chan result, 1)
	go func() {
		items, err := parallelPayloads(ctx, 2, func(ctx context.Context, i int) (Artifact, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
			}
			started <- i
			select {
			case <-release:
			case <-ctx.Done():
				return Artifact{}, ctx.Err()
			}
			return Artifact{Name: fmt.Sprint(i)}, nil
		})
		done <- result{items, err}
	}()
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("two independent payloads did not start concurrently")
		}
	}
	if active.Load() != 2 {
		t.Fatal("worker budget not occupied", active.Load())
	}
	close(release)
	r := <-done
	if r.err != nil || len(r.items) != 4 || peak.Load() != 2 || active.Load() != 0 {
		t.Fatal(r, peak.Load(), active.Load())
	}
	for i, item := range r.items {
		if item.Name != fmt.Sprint(i) {
			t.Fatal("completion order changed manifest order", r.items)
		}
	}
}

func TestParallelPayloadFailureCancelsAndWaitsForSiblings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, cancelled := make(chan struct{}, 4), make(chan struct{}, 3)
	fail, finish := make(chan struct{}), make(chan struct{})
	want := errors.New("compiler failed")
	done := make(chan error, 1)
	go func() {
		_, err := parallelPayloads(ctx, 4, func(ctx context.Context, i int) (Artifact, error) {
			started <- struct{}{}
			if i == 0 {
				<-fail
				return Artifact{}, want
			}
			<-ctx.Done()
			cancelled <- struct{}{}
			<-finish
			return Artifact{}, ctx.Err()
		})
		done <- err
	}()
	for range 4 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("builds did not start")
		}
	}
	close(fail)
	for range 3 {
		select {
		case <-cancelled:
		case <-ctx.Done():
			t.Fatal("failed build did not cancel sibling")
		}
	}
	select {
	case err := <-done:
		t.Fatal("returned before started builds exited", err)
	default:
	}
	close(finish)
	if err := <-done; !errors.Is(err, want) {
		t.Fatal("original compiler failure was lost", err)
	}
}

func TestParallelPayloadsCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := parallelPayloads(ctx, 2, func(context.Context, int) (Artifact, error) {
		t.Error("cancelled build started")
		return Artifact{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPayloadBudgetAndEnvironment(t *testing.T) {
	available := runtime.GOMAXPROCS(0)
	for _, requested := range []int{0, 1, 2, 4} {
		workers, cpus, err := payloadBudget(requested)
		if err != nil || workers < 1 || workers > 4 || workers*cpus > available || cpus < 1 || requested > 0 && workers > requested {
			t.Fatal(requested, workers, cpus, available, err)
		}
	}
	t.Setenv("GOMAXPROCS", "999")
	t.Setenv("GOFLAGS", "-tags=armcarrier")
	env := buildEnvironment("linux", "arm64", 2)
	values := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if _, duplicate := values[key]; duplicate {
			t.Fatal("duplicate environment key", key)
		}
		values[key] = value
	}
	if values["GOMAXPROCS"] != "2" || values["GOFLAGS"] != "" || values["GOOS"] != "linux" || values["CGO_ENABLED"] != "0" {
		t.Fatal(values)
	}
	for _, jobs := range []int{-1, 5} {
		root := filepath.Join(t.TempDir(), "uncreated")
		options := Options{Root: root, Version: "test", Protocol: 1, Jobs: jobs}
		if _, _, err := Build(context.Background(), options); err == nil {
			t.Fatal("invalid parallelism accepted", jobs)
		}
		if _, _, err := BuildCarrier(context.Background(), options); err == nil {
			t.Fatal("invalid carrier parallelism accepted", jobs)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("invalid request wrote output")
		}
	}
}

func TestConcurrentBuildLogsSerializeWrites(t *testing.T) {
	var output bytes.Buffer
	writer := &lockedWriter{writer: &output}
	var group sync.WaitGroup
	for i := range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			for range 32 {
				fmt.Fprintf(writer, "payload-%d\n", i)
			}
		}()
	}
	group.Wait()
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 128 {
		t.Fatal("lost diagnostic writes", len(lines))
	}
	for _, line := range lines {
		if line != "payload-0" && line != "payload-1" && line != "payload-2" && line != "payload-3" {
			t.Fatal("interleaved diagnostic write", line)
		}
	}
}
