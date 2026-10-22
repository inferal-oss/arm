//go:build arm_devtrace

package manager

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/yrashk/arm/internal/model"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// This test process is owned by its parent exec.Cmd, not by a recorded PID.
// It is intentionally killed while its real native pipe read is still blocked.
func TestShareFormalCrashChild(t *testing.T) {
	home := os.Getenv("ARM_FORMAL_CHILD_HOME")
	if home == "" {
		return
	}
	key := os.Getenv("ARM_FORMAL_CHILD_KEY")
	ready := os.Getenv("ARM_FORMAL_CHILD_READY")
	if _, err := formalPath(home); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1 << 20}, TTL: 5 * time.Minute, Reason: "private formal crash input", Key: "formal-source"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.ManagedDir, "payload"), []byte("synthetic formal input"), 0600); err != nil {
		t.Fatal(err)
	}
	entered, _, _ := blockShareNativeIO(t, m)
	o := &shareFormalObserver{key: key, storage: s.ID, native: "never", events: make(chan shareFormalState, 256)}
	shareFormalObservers.Store(home, o)
	m.mu.Lock()
	m.traceShareLocked("init", "")
	m.mu.Unlock()
	if _, err = m.PrepareShare(context.Background(), shareRequest(s, key)); err != nil {
		t.Fatal(err)
	}
	<-entered
	o.mu.Lock()
	rows := append([]shareFormalState(nil), o.records...)
	err = o.err
	o.mu.Unlock()
	if err != nil || len(rows) != 4 {
		t.Fatal("actual child prefix", rows, err)
	}
	if err = formalWriteJSON(ready, rows); err != nil {
		t.Fatal(err)
	}
	select {}
}
func formalRunCrashWitness(t *testing.T, c shareFormalCase, expected []shareFormalState) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "authority")
	ready := filepath.Join(root, "ready.json")
	key := "formal-" + c.Target
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestShareFormalCrashChild$", "-test.timeout=60s")
	child.Env = append(os.Environ(), "ARM_FORMAL_CHILD_HOME="+home, "ARM_FORMAL_CHILD_READY="+ready, "ARM_FORMAL_CHILD_KEY="+key)
	child.Stdout = io.Discard
	child.Stderr = os.Stderr
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	var prefix []shareFormalState
	shareWait(t, func() bool { return formalReadJSON(ready, &prefix) == nil })
	if len(prefix) != 4 {
		t.Fatal("child did not emit actual entry prefix")
	}
	o := &shareFormalObserver{key: key, records: append([]shareFormalState(nil), prefix...), native: prefix[3].Native, launches: prefix[3].Launches, joined: prefix[3].JoinKnown, events: make(chan shareFormalState, 256), gates: map[string]chan struct{}{"reconcile": make(chan struct{})}}
	shareFormalObservers.Store(home, o)
	defer shareFormalObservers.Delete(home)
	var m *Manager
	defer func() {
		if m != nil {
			_ = m.Close()
		}
	}()
	var observed []shareFormalState
	for i, want := range expected {
		if i < 4 {
			if !reflect.DeepEqual(prefix[i], want) {
				t.Fatalf("real child differs from TLC step%d: Go%+v TLC%+v", i, prefix[i], want)
			}
			observed = append(observed, prefix[i])
			continue
		}
		switch want.Action {
		case "crash":
			if err = child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err = child.Wait(); err == nil {
				t.Fatal("child did not abruptly die")
			}
			db, e := sql.Open("sqlite", "file:"+filepath.Join(home, "state.db")+"?mode=ro")
			if e != nil {
				t.Fatal(e)
			}
			db.SetMaxOpenConns(1)
			// Observe durable SQL again only after the exact owned process is gone.
			// No startup/recovery/cache population is performed for this snapshot.
			offline := &Manager{home: home, db: db, shares: &shareBoundary{}}
			o.mu.Lock()
			o.offline = true
			if o.native == "running" {
				o.native = "lost"
			}
			o.mu.Unlock()
			offline.traceShareLocked("crash", "")
			db.Close()
		case "restart", "restart_unknown", "restart_rollback_unknown":
			o.mu.Lock()
			o.offline = false
			o.recovering = true
			o.loseNextAck = want.Action == "restart_unknown"
			o.rollbackBeforeCommit = want.Action == "restart_rollback_unknown"
			o.mu.Unlock()
			m, err = Open(home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
			if err != nil {
				t.Fatal(err)
			}
			o.mu.Lock()
			o.recovering = false
			o.mu.Unlock()
			if len(m.shares.workers) != 0 {
				t.Fatal("restart replayed native worker")
			}
		case "observed_absent":
			p := formalPublication(t, m, key)
			go func() { _, _ = m.ReconcileShare(t.Context(), p.ID, p.Revision, "private exact child absence proof") }()
		case "cancelled":
			close(o.gates["reconcile"])
		default:
			t.Fatal("unsupported crash witness action", want.Action)
		}
		select {
		case got := <-o.events:
			observed = append(observed, got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("actual crash/recovery differs at%d:\nGo%+v\nTLC%+v", i, got, want)
			}
		case <-time.After(5 * time.Second):
			o.mu.Lock()
			e := o.err
			o.mu.Unlock()
			t.Fatal("no actual crash/recovery observation", want.Action, e)
		}
	}
	o.mu.Lock()
	err = o.err
	n := len(o.records)
	o.mu.Unlock()
	if err != nil || n != len(expected) {
		t.Fatal(fmt.Sprintf("extra/missing crash observations %d", n), err)
	}
	if err = formalWriteJSON(c.Output, observed); err != nil {
		t.Fatal(err)
	}
}
