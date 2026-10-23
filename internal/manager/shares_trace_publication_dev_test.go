//go:build arm_devtrace

package manager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/sharelease"
)

// Each scope really blocks in an OS pipe read. Cancellation cannot manufacture
// native join; only releasing the corresponding owned descriptor permits it.
func publicationBlockNative(t *testing.T, m *Manager) func(string) {
	t.Helper()
	var reads [2]*os.File
	var writes [2]*os.File
	var once [2]sync.Once
	for i := range 2 {
		var err error
		reads[i], writes[i], err = os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
	}
	var entered atomic.Int32
	release := func(scope string) {
		i := 0
		if scope == "verification" {
			i = 1
		}
		once[i].Do(func() { _ = writes[i].Close() })
	}
	m.shares.beforeNativeBuild = func(context.Context) {
		i := int(entered.Add(1)) - 1
		if i > 1 {
			t.Error("unexpected extra native entry")
			return
		}
		var b [1]byte
		_, _ = reads[i].Read(b[:])
	}
	t.Cleanup(func() {
		release("preparation")
		release("verification")
		for _, f := range reads {
			_ = f.Close()
		}
	})
	return release
}
func TestSharePublicationFormalWitnesses(t *testing.T) {
	manifest := os.Getenv("ARM_PUBLICATION_WITNESSES")
	if manifest == "" {
		t.Fatal("publication witness manifest required")
	}
	var cases []shareFormalCase
	if err := formalReadJSON(manifest, &cases); err != nil || len(cases) == 0 || len(cases) > 40 {
		t.Fatal("complete bounded publication manifest required", err)
	}
	for _, c := range cases {
		t.Run(c.Target, func(t *testing.T) { publicationRunWitness(t, c) })
	}
}
func publicationRunWitness(t *testing.T, c shareFormalCase) {
	var expected []publicationFormalState
	if err := formalReadJSON(c.Path, &expected); err != nil || len(expected) < 2 || len(expected) > 128 {
		t.Fatal("invalid publication witness", err)
	}
	if strings.Contains(c.Target, "restart") || c.Target == "crash_unknown" || c.Target == "reconciled" || c.Target == "retirement_decision_recovery" {
		publicationRunCrashWitness(t, c, expected)
		return
	}
	f := newShareFixture(t)
	release := publicationBlockNative(t, f.m)
	o := &publicationFormalObserver{key: "formal-publication-" + c.Target, storage: f.storage.ID, events: make(chan publicationFormalState, 256), gates: map[string]chan struct{}{}, native: map[string]publicationNative{}}
	for _, scope := range []string{"preparation", "verification"} {
		for _, point := range []string{"worker_start", "native_begin", "settle", "supervisor_drop"} {
			o.gates[scope+":"+point] = make(chan struct{})
		}
	}
	publicationFormalObservers.Store(f.m.home, o)
	open := func(scope, point string) {
		o.mu.Lock()
		defer o.mu.Unlock()
		g := o.gates[scope+":"+point]
		if g != nil {
			select {
			case <-g:
			default:
				close(g)
			}
		}
	}
	t.Cleanup(func() {
		publicationFormalObservers.Delete(f.m.home)
		f.m.mu.Lock()
		delete(f.m.shares.state.Consumers, sharelease.OperationKey{Principal: "formal", Key: "retained-consumer"})
		delete(f.m.shares.state.Fences, sharelease.OperationKey{Principal: "formal", Key: "retained-fence"})
		f.m.mu.Unlock()
		for _, s := range []string{"preparation", "verification"} {
			for _, p := range []string{"worker_start", "native_begin", "settle", "supervisor_drop"} {
				open(s, p)
			}
		}
		release("preparation")
		release("verification")
	})
	mark := func(label string) { f.m.mu.Lock(); defer f.m.mu.Unlock(); f.m.tracePublicationLocked(label, "") }
	mark("init")
	prepareCtx, prepareCancel := context.WithCancel(t.Context())
	defer prepareCancel()
	verifyCtx, verifyCancel := context.WithCancel(t.Context())
	defer verifyCancel()
	q := shareRequest(f.storage, o.key)
	var approval sharelease.PublishRequest
	started := false
	armFault := func(nondurable bool) {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.rollbackBeforeCommit = nondurable
		o.loseNextAck = !nondurable
	}
	var originalParts string
	var observed []publicationFormalState
	for i, want := range expected {
		if want.Schema != "arm.share-publication/v1" {
			t.Fatal("unsupported publication trace schema")
		}
		if i > 0 {
			prior := expected[i-1]
			switch want.Action {
			case "admit", "admit_unknown":
				if started {
					t.Fatal("driver repeated preparation")
				}
				started = true
				if strings.HasSuffix(want.Action, "_unknown") {
					armFault(false)
				}
				go func() { _, _ = f.m.PrepareShare(prepareCtx, q) }()
			case "worker_start", "worker_start_unknown":
				if strings.HasSuffix(want.Action, "_unknown") {
					armFault(false)
				}
				open(want.Scope, "worker_start")
			case "native_begin":
				open(want.Scope, "native_begin")
			case "native_join":
				if want.Scope == "preparation" {
					release("preparation")
				}
			case "compare":
				if want.Comparison == "mismatch" {
					if err := os.WriteFile(filepath.Join(f.storage.ManagedDir, "payload"), []byte("changed after preparation"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				release("verification")
			case "prepared", "prepared_unknown", "published", "published_unknown", "failed", "failed_unknown", "cancelled", "cancelled_unknown":
				if strings.HasSuffix(want.Action, "_unknown") {
					armFault(false)
				}
				open(want.Scope, "settle")
			case "supervisor_drop":
				open(want.Scope, "supervisor_drop")
			case "verify_intent", "verify_intent_unknown", "commit_unavailable":
				p := formalPublication(t, f.m, q.Key)
				originalParts = manifestRows(t, f.m, p.ID)
				approval = publicationRequest(p)
				if want.Action == "commit_unavailable" {
					armFault(true)
				} else if want.Action == "verify_intent_unknown" {
					armFault(false)
				}
				go func() { _, _ = f.m.PublishShare(verifyCtx, approval) }()
			case "native_skipped":
				if want.Scope == "verification" {
					verifyCancel()
				} else {
					prepareCancel()
				}
				open(want.Scope, "worker_start")
			case "ending", "ending_unknown":
				if strings.HasSuffix(want.Action, "_unknown") {
					armFault(false)
				}
				p := formalPublication(t, f.m, q.Key)
				_, err := f.m.End(p.Lease, want.Outcome)
				if want.Action == "ending" && err != nil {
					t.Fatal(err)
				}
			case "retiring", "retiring_unknown", "retired_unknown":
				if i+1 < len(expected) && expected[i+1].Action == "retired_unknown" {
					o.mu.Lock()
					o.faultAfter = "retiring"
					o.mu.Unlock()
				}
				if strings.HasSuffix(want.Action, "_unknown") {
					armFault(false)
				}
				p := formalPublication(t, f.m, q.Key)
				_, err := f.m.End(p.Lease, want.EndDecision)
				if want.Action == "retiring" && err != nil && (i+1 >= len(expected) || expected[i+1].Action != "retired_unknown") {
					t.Fatal(err)
				}
			case "retired": // emitted by the same real End call as retiring
			case "retained_evidence":
				p := formalPublication(t, f.m, q.Key)
				f.m.mu.Lock()
				f.m.shares.state.Consumers[sharelease.OperationKey{Principal: "formal", Key: "retained-consumer"}] = sharelease.Consumer{Admission: sharelease.Admission{Control: sharelease.Control{Share: p.ID}}}
				if want.Evidence == 2 {
					f.m.shares.state.Fences[sharelease.OperationKey{Principal: "formal", Key: "retained-fence"}] = sharelease.Fence{Control: sharelease.Control{Share: p.ID}}
				}
				f.m.tracePublicationLocked("retained_evidence", p.ID)
				f.m.mu.Unlock()
			case "rejected":
				p := formalPublication(t, f.m, q.Key)
				beforeRows := shareRowCount(t, f.m, "share_journal")
				if _, err := f.m.End(p.Lease, "released"); errorCode(err) != "needs_attention" {
					t.Fatal("retained histories did not refuse retirement", err)
				}
				after := formalPublication(t, f.m, q.Key)
				l, err := f.m.Inspect(p.Lease)
				if err != nil || !consuming(l.State) || l.Ended != nil || !reflect.DeepEqual(p, after) || shareRowCount(t, f.m, "share_journal") != beforeRows {
					t.Fatal("refused retirement mutated charged lifetime", err)
				}
				mark("rejected")
			case "retry":
				before := formalPublication(t, f.m, q.Key)
				got, err := f.m.PublishShare(t.Context(), approval)
				if err != nil || !reflect.DeepEqual(got, before) {
					t.Fatal("exact retry mutated publication", err)
				}
				mark("retry")
			default:
				t.Fatal("unsupported publication witness action", want.Action, prior.Action)
			}
		}
		select {
		case got := <-o.events:
			observed = append(observed, got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("actual publication step%d differs:\nGo  %+v\nTLC %+v", i, got, want)
			}
			if originalParts != "" {
				p := formalPublication(t, f.m, q.Key)
				if got.Original && manifestRows(t, f.m, p.ID) != originalParts {
					t.Fatal("original raw manifest parts changed")
				}
			}
		case <-time.After(8 * time.Second):
			o.mu.Lock()
			err := o.err
			o.mu.Unlock()
			t.Fatal("missing actual publication observation", want.Action, err)
		}
	}
	o.mu.Lock()
	err, n := o.err, len(o.records)
	o.mu.Unlock()
	if err != nil || n != len(expected) {
		t.Fatal(fmt.Sprintf("extra/missing publication observations%d/%d", n, len(expected)), err)
	}
	if err = formalWriteJSON(c.Output, observed); err != nil {
		t.Fatal(err)
	}
}
