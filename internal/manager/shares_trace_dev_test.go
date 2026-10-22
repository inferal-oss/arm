//go:build arm_devtrace

package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/sharelease"
	"golang.org/x/sys/unix"
)

type shareFormalCase struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	Output string `json:"output"`
}

func formalPublication(t *testing.T, m *Manager, key string) sharelease.Publication {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	var raw []byte
	if err := m.db.QueryRow(`SELECT data FROM share_publications WHERE json_extract(data,'$.request.key')=?`, key).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	p, err := sharelease.DecodePublication(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestShareFormalTLCWitnesses(t *testing.T) {
	manifest := os.Getenv("ARM_FORMAL_WITNESSES")
	if manifest == "" {
		t.Fatal("formal witness input required; run make tla-traces")
	}
	var cases []shareFormalCase
	if err := formalReadJSON(manifest, &cases); err != nil || len(cases) == 0 || len(cases) > 32 {
		t.Fatal("complete bounded TLC witness manifest required", err)
	}
	for _, c := range cases {
		t.Run(c.Target, func(t *testing.T) { formalRunWitness(t, c) })
	}
}
func formalRunWitness(t *testing.T, c shareFormalCase) {
	var expected []shareFormalState
	if err := formalReadJSON(c.Path, &expected); err != nil || len(expected) < 2 || len(expected) > 128 {
		t.Fatal("invalid TLC witness", err)
	}
	for _, s := range expected {
		if s.Schema != "arm.share-preparation/v1" {
			t.Fatal("unsupported trace schema")
		}
	}
	if strings.Contains(c.Target, "restart") || c.Target == "crash_running" || c.Target == "reconciled" {
		formalRunCrashWitness(t, c, expected)
		return
	}
	f := newShareFixture(t)
	_, _, releaseNative := blockShareNativeIO(t, f.m)
	o := &shareFormalObserver{key: "formal-" + c.Target, storage: f.storage.ID, events: make(chan shareFormalState, 256), gates: map[string]chan struct{}{"worker_start": make(chan struct{}), "native_begin": make(chan struct{}), "settle": make(chan struct{}), "reconcile": make(chan struct{})}, native: "never"}
	shareFormalObservers.Store(f.m.home, o)
	openGate := func(point string) {
		o.mu.Lock()
		defer o.mu.Unlock()
		gate := o.gates[point]
		if gate != nil {
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
	}
	t.Cleanup(func() {
		shareFormalObservers.Delete(f.m.home)
		for _, p := range []string{"worker_start", "native_begin", "settle", "reconcile"} {
			openGate(p)
		}
		releaseNative()
	})
	f.m.mu.Lock()
	f.m.traceShareLocked("init", "")
	f.m.mu.Unlock()
	q := shareRequest(f.storage, o.key)
	if c.Target == "expired" {
		q.TTL = 2 * time.Second
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		p   sharelease.Publication
		err error
	}
	answer := make(chan result, 1)
	started := false
	prepare := func() {
		if started {
			t.Fatal("driver attempted to reconstruct preparation")
		}
		started = true
		go func() { p, e := f.m.PrepareShare(ctx, q); answer <- result{p, e} }()
	}
	unknown := func() { shareLoseNextCommitAcknowledgment(f.m) }
	mark := func(action string) { f.m.mu.Lock(); defer f.m.mu.Unlock(); f.m.traceShareLocked(action, "") }
	var observed []shareFormalState
	for index, want := range expected {
		if index > 0 {
			switch want.Action {
			case "admit":
				prepare()
			case "admit_unknown":
				unknown()
				prepare()
			case "worker_start":
				openGate("worker_start")
			case "worker_start_unknown":
				unknown()
				openGate("worker_start")
			case "native_begin":
				openGate("native_begin")
			case "native_join":
				if c.Target == "failed" {
					if err := unix.Mkfifo(filepath.Join(f.storage.ManagedDir, "unsupported-fifo"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				releaseNative()
			case "native_skipped":
				cancel()
				openGate("worker_start")
			case "prepared":
				openGate("settle")
			case "prepared_unknown":
				unknown()
				openGate("settle")
			case "ending", "ending_unknown":
				p := formalPublication(t, f.m, q.Key)
				if want.Action == "ending_unknown" {
					unknown()
				}
				if want.Outcome == "expired" {
					time.Sleep(max(0, time.Until(p.Expires)) + time.Millisecond)
					f.m.tick()
				} else {
					_, err := f.m.End(p.Lease, want.Outcome)
					if want.Action == "ending" && err != nil {
						t.Fatal(err)
					}
				}
			case "cancelled", "failed", "cancelled_unknown", "failed_unknown":
				if strings.HasSuffix(want.Action, "_unknown") {
					unknown()
				}
				if expected[index-1].Phase == "prepared" {
					p := formalPublication(t, f.m, q.Key)
					_, err := f.m.End(p.Lease, want.Outcome)
					if !strings.HasSuffix(want.Action, "_unknown") && err != nil {
						t.Fatal(err)
					}
				} else {
					openGate("settle")
				}
			case "retry":
				if _, err := f.m.PrepareShare(t.Context(), q); err != nil {
					t.Fatal(err)
				}
				mark("retry")
			case "lookup":
				if _, found, err := f.m.LookupSharePreparation(q.Key); err != nil || !found {
					t.Fatal(found, err)
				}
				mark("lookup")
			case "lookup_unavailable":
				if _, _, err := f.m.LookupSharePreparation(q.Key); errorCode(err) != "share_evidence_unavailable" {
					t.Fatal("unknown commit reported an exact outcome", err)
				}
				mark("lookup_unavailable")
			case "rejected":
				if _, err := f.m.db.Exec(`CREATE TEMP TRIGGER formal_reject_commit BEFORE INSERT ON events WHEN NEW.type='lease.created' BEGIN SELECT RAISE(ABORT,'formal known precommit rollback'); END`); err != nil {
					t.Fatal(err)
				}
				prepare()
				select {
				case a := <-answer:
					if a.err == nil {
						t.Fatal("known rollback admitted native work")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("rollback request blocked")
				}
				mark("rejected")
			case "commit_unavailable":
				o.mu.Lock()
				o.rollbackBeforeCommit = true
				o.mu.Unlock()
				prepare()
			default:
				t.Fatalf("real driver scenario still unsupported: %s at%d", want.Action, index)
			}
		}
		select {
		case got := <-o.events:
			observed = append(observed, got)
			if !reflect.DeepEqual(got, want) {
				a, _ := json.Marshal(got)
				b, _ := json.Marshal(want)
				t.Fatalf("actual Manager differs from TLC at%d:\nGo %s\nTLC%s", index, a, b)
			}
		case <-time.After(5 * time.Second):
			o.mu.Lock()
			err := o.err
			o.mu.Unlock()
			t.Fatalf("no real Manager event for TLC step%d %s: %v", index, want.Action, err)
		}
	}
	o.mu.Lock()
	err := o.err
	count := len(o.records)
	o.mu.Unlock()
	if err != nil || count != len(expected) {
		t.Fatal("unmatched actual events or trace observation error", count, err)
	}
	if err := formalWriteJSON(c.Output, observed); err != nil {
		t.Fatal(err)
	}
}
