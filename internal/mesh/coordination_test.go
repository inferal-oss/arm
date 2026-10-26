package mesh

import (
	"encoding/json"
	"github.com/inferal-oss/arm/internal/local"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func coordinationHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "state")
	if e := local.Prepare(home); e != nil {
		t.Fatal(e)
	}
	return home
}
func TestCoordinationCrossWorkerTargetAndRevision(t *testing.T) {
	home := coordinationHome(t)
	digest := strings.Repeat("a", 64)
	one, e := ClaimCoordination(home, "one", "connect", digest, "ssh:alias")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ClaimCoordination(home, "two", "connect", digest, "ssh:alias"); e == nil {
		t.Fatal("duplicate target worker")
	}
	if e = one.BindTarget("host:actual"); e != nil {
		t.Fatal(e)
	}
	if _, e = ClaimCoordination(home, "two", "connect", digest, "host:actual"); e == nil {
		t.Fatal("alias bypassed stable target")
	}
	if _, e = ClaimCoordination(home, "one", "connect", strings.Repeat("b", 64), "ssh:alias"); e == nil {
		t.Fatal("key changed payload")
	}
	raw, ok, e := ReadCoordination(home, "one")
	if e != nil || !ok {
		t.Fatal(e)
	}
	stale := CoordinationGuard{home: one.home, key: one.key, worker: one.worker, revision: one.revision}
	if e = one.Change(func(op *Coordination) error { op.Phase = "working"; return nil }); e != nil {
		t.Fatal(e)
	}
	if e = stale.Change(func(op *Coordination) error { op.Phase = "completed"; return nil }); e == nil {
		t.Fatal("stale worker revision accepted")
	}
	raw.WorkerExpires = time.Now().Add(-time.Minute)
	if e = saveCoordination(home, raw); e != nil {
		t.Fatal(e)
	}
	if _, e = ClaimCoordination(home, "three", "connect", digest, "host:actual"); e == nil {
		t.Fatal("expired uncertain worker lost target protection")
	}
	if e = one.Heartbeat(); e == nil {
		t.Fatal("expired worker resurrected")
	}
}
func TestCoordinationReviewBoundCASAndSingleDecision(t *testing.T) {
	home := coordinationHome(t)
	g, e := ClaimCoordination(home, "review", "connect", strings.Repeat("a", 64), "ssh:target")
	if e != nil {
		t.Fatal(e)
	}
	if e = g.Change(func(op *Coordination) error {
		op.Phase = "awaiting_review"
		op.Review = json.RawMessage(`{"host":"exact","grants":["observe-host"]}`)
		op.ReviewHash = "exact-hash"
		op.ReviewRevision = op.Revision + 1
		op.ReviewExpires = time.Now().Add(time.Minute)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	op, _, _ := ReadCoordination(home, "review")
	if _, e = DecideCoordination(home, "review", op.ReviewRevision, "changed", true); e == nil {
		t.Fatal("different plan approved")
	}
	if _, e = DecideCoordination(home, "review", op.ReviewRevision+1, op.ReviewHash, true); e == nil {
		t.Fatal("different revision approved")
	}
	if _, e = DecideCoordination(home, "review", op.ReviewRevision, op.ReviewHash, false); e != nil {
		t.Fatal(e)
	}
	if _, e = DecideCoordination(home, "review", op.ReviewRevision, op.ReviewHash, true); e == nil {
		t.Fatal("decline converted into approval")
	}
	current, e := g.Read()
	if e != nil || current.Phase != "review_declined" {
		t.Fatal(current, e)
	}
	if e = g.Change(func(op *Coordination) error { op.Phase = "declined"; op.Worker = ""; return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = ClaimCoordination(home, "new", "connect", strings.Repeat("b", 64), "ssh:target"); e != nil {
		t.Fatal("declined target retained", e)
	}
}

func TestCoordinationDurableWorkerCapAndProspectiveAttemptTarget(t *testing.T) {
	home := coordinationHome(t)
	digest := strings.Repeat("c", 64)
	parent, e := ClaimCoordination(home, "parent", "connect", digest, "ssh:parent")
	if e != nil {
		t.Fatal(e)
	}
	if e = parent.Change(func(op *Coordination) error { op.Phase = "completed"; op.Worker = ""; return nil }); e != nil {
		t.Fatal(e)
	}
	other, e := ClaimCoordination(home, "other", "connect", digest, "host:busy")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ClaimCoordinationAttempt(home, "parent", "host:busy"); e == nil {
		t.Fatal("attempt appended already-owned target")
	}
	second, e := ClaimCoordination(home, "second", "connect", digest, "ssh:second")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ClaimCoordination(home, "third", "connect", digest, "ssh:third"); e == nil {
		t.Fatal("cross-process durable worker cap bypassed")
	}
	if _, e = ClaimCoordinationAttempt(home, "parent", "ssh:parent"); e == nil {
		t.Fatal("attempt bypassed durable worker cap")
	}
	_ = other
	_ = second
}

func TestCoordinationResumeChildNormalizesToImmutableRoot(t *testing.T) {
	home := coordinationHome(t)
	digest := strings.Repeat("d", 64)
	root, e := ClaimCoordination(home, "root", "disconnect", digest, "host:target")
	if e != nil {
		t.Fatal(e)
	}
	if e = root.Change(func(op *Coordination) error { op.Phase = "incomplete"; op.Approved = true; op.Worker = ""; return nil }); e != nil {
		t.Fatal(e)
	}
	child, e := ClaimCoordinationAttempt(home, "root", "host:target")
	if e != nil {
		t.Fatal(e)
	}
	if e = child.Change(func(op *Coordination) error { op.Phase = "incomplete"; op.Approved = true; op.Worker = ""; return nil }); e != nil {
		t.Fatal(e)
	}
	next, e := ClaimCoordinationAttempt(home, child.Key(), "host:target")
	if e != nil {
		t.Fatal(e)
	}
	op, _, _ := ReadCoordination(home, next.Key())
	if op.ParentKey != "root" || op.ResumeFrom != child.Key() {
		t.Fatal("new attempt nested or lost resume provenance", op)
	}
	if e = next.Change(func(op *Coordination) error { op.Phase = "completed"; op.Worker = ""; return nil }); e != nil {
		t.Fatal(e)
	}
	if e = ResolveCoordinationParent(home, next.Key()); e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"root", child.Key()} {
		op, _, e = ReadCoordination(home, key)
		if e != nil || op.ResolvedBy != next.Key() {
			t.Fatal("unresolved ancestor kept target charged", key, op, e)
		}
	}
	if _, e = ClaimCoordination(home, "fresh", "connect", digest, "host:target"); e != nil {
		t.Fatal("resolved family kept target exclusion", e)
	}
}

func TestOperatorAuditReasonsMustContainText(t *testing.T) {
	if e := (Connector{ID: "fixture", Destination: "fixture", Binary: "arm", Reason: " \t\n"}).Validate(); e == nil {
		t.Fatal("blank connector reason accepted")
	}
	home := filepath.Join(t.TempDir(), "absent")
	if _, e := HandoffCoordination(home, "key", 1, "hash", " \t "); e == nil {
		t.Fatal("blank handoff reason accepted")
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("invalid reason created state")
	}
}
