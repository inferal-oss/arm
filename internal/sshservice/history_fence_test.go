package sshservice

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type packReadBarrierContext struct {
	context.Context
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func TestSourceHistoryContradictoryHotOrPendingPreverifyRefuses(t *testing.T) {
	for _, kind := range []string{"hot", "pending"} {
		t.Run(kind, func(t *testing.T) {
			home, b, items := settledSourceHistoryFixture(t, 1)
			if _, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "overlap")); e != nil {
				t.Fatal(e)
			}
			original, e := LookupSourceHistory(t.Context(), home, b, items[0].Request.Pin.Target, items[0].Request.Key)
			if e != nil {
				t.Fatal(e)
			}
			var raw []byte
			var name string
			if kind == "hot" {
				v := *original.Completion
				v.At = v.At.Add(time.Second)
				raw, e = json.Marshal(v)
				name = completionName(v.Intent)
			} else {
				v := items[0]
				v.Request.Reason = "Different valid unknown source intention"
				raw, e = json.Marshal(v)
				name = intentName(v)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(home+"/mesh/"+name, raw, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = ReadState(t.Context(), home, b); e != nil {
				t.Fatal("overlap fixture not individually valid", e)
			}
			if _, e = LookupSourceHistory(t.Context(), home, b, items[0].Request.Pin.Target, items[0].Request.Key); e == nil {
				t.Fatal("lookup hid contradictory saved evidence")
			}
			if e = SettleSource(t.Context(), home, b, items[0], original.Completion.Receipt); e == nil {
				t.Fatal("read-only settlement hid contradictory saved evidence")
			}
			retained, e := os.ReadFile(home + "/mesh/" + name)
			if e != nil || string(retained) != string(raw) {
				t.Fatal("conflicting evidence was changed", e)
			}
		})
	}
}

func (c *packReadBarrierContext) Err() error {
	pcs := make([]uintptr, 24)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	inside := false
	for {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, "/internal/historypack.") {
			inside = true
		}
		if !more {
			break
		}
	}
	if inside {
		c.once.Do(func() { close(c.entered); <-c.release })
	}
	return c.Context.Err()
}
func TestSourceHistoryFullReadDoesNotHoldAdmissionLocks(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	if _, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "offlock")); e != nil {
		t.Fatal(e)
	}
	saved, e := LookupSourceHistory(t.Context(), home, b, items[0].Request.Pin.Target, items[0].Request.Key)
	if e != nil {
		t.Fatal(e)
	}
	next := sourceFixture(t, home, b, items[0].Request.Pin, "unrelated-admission")
	ctx := &packReadBarrierContext{Context: t.Context(), entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- SettleSource(ctx, home, b, items[0], saved.Completion.Receipt) }()
	select {
	case <-ctx.entered:
	case <-time.After(5 * time.Second):
		close(ctx.release)
		t.Fatal("actual pack read not reached")
	}
	// One bounded full pack read is deliberately suspended. A different source
	// reservation must finish before releasing it: no lifecycle/SSH lock is held.
	admitted := make(chan error, 1)
	go func() { admitted <- ReserveSource(t.Context(), home, b, next) }()
	select {
	case e := <-admitted:
		if e != nil {
			close(ctx.release)
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		close(ctx.release)
		t.Fatal("pack verification held source admission locks")
	}
	close(ctx.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestSourceHistoryPreverifiedNativeVersionChangeRefused(t *testing.T) {
	home, b, items := settledSourceHistoryFixture(t, 1)
	op, e := ArchiveSourceHistory(t.Context(), home, b, sourceArchiveRequest(t, home, b, "version"))
	if e != nil {
		t.Fatal(e)
	}
	v, e := preverifySourceHistory(t.Context(), home, b, items[0].Request.Pin.Target, items[0].Request.Key)
	if e != nil {
		t.Fatal(e)
	}
	path := home + "/" + SourceHistoryName + "/packs/pack-" + op.PackID + "/records.bin"
	f, e := os.OpenFile(path, os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	// Same inode and size; a changed version requires a fresh full read.
	if _, e = f.WriteAt([]byte{'x'}, 0); e != nil {
		f.Close()
		t.Fatal(e)
	}
	f.Close()
	e = withStateMutation(t.Context(), home, b, true, func(s *State, _ *os.File) error { return recheckVerifiedSourceHistory(t.Context(), s, v) })
	if e == nil {
		t.Fatal("changed bytes/version accepted from old verification")
	}
}
