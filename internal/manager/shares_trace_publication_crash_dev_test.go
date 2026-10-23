//go:build arm_devtrace

package manager

import (
	"context"
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

// The parent owns and reaps this process. Persisted fingerprints never confer
// process signalling authority and are never used to reconstruct runtime work.
func TestSharePublicationFormalCrashChild(t *testing.T) {
	home := os.Getenv("ARM_PUBLICATION_CHILD_HOME")
	if home == "" {
		return
	}
	if _, err := formalPath(home); err != nil {
		t.Fatal(err)
	}
	key, ready, target := os.Getenv("ARM_PUBLICATION_CHILD_KEY"), os.Getenv("ARM_PUBLICATION_CHILD_READY"), os.Getenv("ARM_PUBLICATION_CHILD_TARGET")
	m, err := Open(home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := m.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1 << 20}, TTL: 5 * time.Minute, Reason: "private formal publication crash source", Key: "formal-publication-source"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(storage.ManagedDir, "payload"), []byte("approved fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	o := &publicationFormalObserver{key: key, storage: storage.ID, events: make(chan publicationFormalState, 256), native: map[string]publicationNative{}}
	publicationFormalObservers.Store(home, o)
	m.mu.Lock()
	m.tracePublicationLocked("init", "")
	m.mu.Unlock()
	p, err := m.PrepareShare(context.Background(), shareRequest(storage, key))
	if err != nil {
		t.Fatal(err)
	}
	shareWait(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.shares.state.Publications[p.ID].State == "prepared" && m.shares.workers[p.ID] == nil
	})
	p, err = m.InspectShare(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target == "published_restart" {
		o.mu.Lock()
		o.gates = map[string]chan struct{}{"verification:supervisor_drop": make(chan struct{})}
		o.mu.Unlock()
	}
	var entered <-chan struct{}
	if target != "published_restart" && target != "retirement_decision_recovery" {
		entered, _, _ = blockShareNativeIO(t, m)
	}
	if _, err = m.PublishShare(context.Background(), publicationRequest(p)); err != nil {
		t.Fatal(err)
	}
	if entered != nil {
		<-entered
	} else {
		if target == "published_restart" {
			shareWait(t, func() bool {
				m.mu.Lock()
				defer m.mu.Unlock()
				return m.shares.state.Publications[p.ID].State == "published"
			})
		} else {
			p = waitPublicationDone(t, m, p.ID)
		}
		if target == "retirement_decision_recovery" {
			o.mu.Lock()
			o.loseNextAck = true
			o.mu.Unlock()
			if _, err = m.End(p.Lease, "released"); err == nil {
				t.Fatal("lost retirement ACK expected")
			}
		}
	}
	o.mu.Lock()
	rows := append([]publicationFormalState(nil), o.records...)
	err = o.err
	o.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = formalWriteJSON(ready, rows); err != nil {
		t.Fatal(err)
	}
	select {}
}
func publicationRunCrashWitness(t *testing.T, c shareFormalCase, expected []publicationFormalState) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "authority")
	ready := filepath.Join(root, "ready.json")
	key := "formal-publication-" + c.Target
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestSharePublicationFormalCrashChild$", "-test.timeout=60s")
	child.Env = append(os.Environ(), "ARM_PUBLICATION_CHILD_HOME="+home, "ARM_PUBLICATION_CHILD_KEY="+key, "ARM_PUBLICATION_CHILD_READY="+ready, "ARM_PUBLICATION_CHILD_TARGET="+c.Target)
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
	var prefix []publicationFormalState
	shareWait(t, func() bool { return formalReadJSON(ready, &prefix) == nil })
	if len(prefix) < 10 {
		t.Fatal("missing actual child publication prefix")
	}
	o := &publicationFormalObserver{key: key, records: append([]publicationFormalState(nil), prefix...), native: prefix[len(prefix)-1].Native, comparison: prefix[len(prefix)-1].Comparison, stopped: prefix[len(prefix)-1].Stop, endDecision: prefix[len(prefix)-1].EndDecision, events: make(chan publicationFormalState, 256), gates: map[string]chan struct{}{"verification:reconcile": make(chan struct{})}}
	if o.endDecision == "none" {
		o.endDecision = ""
	}
	for _, snapshot := range prefix {
		if snapshot.Phase == "prepared" && snapshot.Original {
			o.preparedRevision = snapshot.Revision
			break
		}
	}
	copiedNative := map[string]publicationNative{}
	for scope, n := range o.native {
		copiedNative[scope] = n
	}
	o.native = copiedNative
	publicationFormalObservers.Store(home, o)
	defer publicationFormalObservers.Delete(home)
	var m *Manager
	defer func() {
		if m != nil {
			_ = m.Close()
		}
	}()
	var actual []publicationFormalState
	for i, want := range expected {
		if i < len(prefix) {
			if !reflect.DeepEqual(want, prefix[i]) {
				t.Fatalf("child prefix%d differs:\nGo%+v\nTLC%+v", i, prefix[i], want)
			}
			actual = append(actual, prefix[i])
			continue
		}
		switch want.Action {
		case "crash":
			if err = child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err = child.Wait(); err == nil {
				t.Fatal("child was not abruptly killed")
			}
			db, e := sql.Open("sqlite", "file:"+filepath.Join(home, "state.db")+"?mode=ro")
			if e != nil {
				t.Fatal(e)
			}
			db.SetMaxOpenConns(1)
			offline := &Manager{home: home, db: db, shares: &shareBoundary{}}
			o.mu.Lock()
			o.offline = true
			n := o.native[want.Scope]
			if n.Status == "running" {
				n.Status = "lost"
				o.native[want.Scope] = n
			}
			o.mu.Unlock()
			offline.tracePublicationLocked("crash", "")
			_ = db.Close()
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
				t.Fatal("restart replayed native scope")
			}
		case "observed_absent":
			p := formalPublication(t, m, key)
			go func() {
				_, _ = m.ReconcileShare(t.Context(), p.ID, p.Revision, "actual owned child is absent; retire private verification")
			}()
		case "cancelled":
			close(o.gates["verification:reconcile"])
		default:
			t.Fatal("unsupported actual crash action", want.Action)
		}
		select {
		case got := <-o.events:
			actual = append(actual, got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("actual crash%d differs:\nGo%+v\nTLC%+v", i, got, want)
			}
		case <-time.After(8 * time.Second):
			o.mu.Lock()
			e := o.err
			o.mu.Unlock()
			t.Fatal("missing actual crash observation", want.Action, e)
		}
	}
	if c.Target == "retirement_decision_recovery" {
		p := formalPublication(t, m, key)
		if _, err = m.End(p.Lease, "cancelled"); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-o.events:
			if got.Action != "retired" || got.Outcome != "released" || got.EndDecision != "released" {
				t.Fatal("committed retirement decision overwritten", got)
			}
			actual = append(actual, got)
		case <-time.After(5 * time.Second):
			t.Fatal("missing retirement completion")
		}
		p = formalPublication(t, m, key)
		before := p
		if _, err = m.End(p.Lease, "expired"); err != nil {
			t.Fatal(err)
		}
		p = formalPublication(t, m, key)
		if !reflect.DeepEqual(p, before) {
			t.Fatal("terminal retry mutated receipt")
		}
		m.mu.Lock()
		m.tracePublicationLocked("retry", p.ID)
		m.mu.Unlock()
		actual = append(actual, <-o.events)
	}
	o.mu.Lock()
	e, n := o.err, len(o.records)
	o.mu.Unlock()
	if e != nil || n != len(actual) {
		t.Fatal("extra/missing crash observations", n, len(actual), e)
	}
	if err = formalWriteJSON(c.Output, actual); err != nil {
		t.Fatal(err)
	}
}
