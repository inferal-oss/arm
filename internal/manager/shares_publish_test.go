package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharelease"
)

func preparedPublication(t *testing.T, f *shareFixture, ttl time.Duration) sharelease.Publication {
	t.Helper()
	q := shareRequest(f.storage, "publication-prepare")
	if ttl > 0 {
		q.TTL = ttl
	}
	p, err := f.m.PrepareShare(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	shareWait(t, func() bool {
		f.m.mu.Lock()
		defer f.m.mu.Unlock()
		return f.m.shares.state.Publications[p.ID].State == "prepared" && f.m.shares.workers[p.ID] == nil
	})
	p, err = f.m.InspectShare(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func publicationRequest(p sharelease.Publication) sharelease.PublishRequest {
	return sharelease.PublishRequest{Schema: 1, Key: "verify-once", Share: p.ID, Generation: p.Generation, Revision: p.Revision, PreparedSHA256: p.Summary.SHA256, Reason: "approve exact immutable fixture generation"}
}
func waitPublicationDone(t *testing.T, m *Manager, id string) sharelease.Publication {
	t.Helper()
	shareWait(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		p := m.shares.state.Publications[id]
		return m.shares.workers[id] == nil && p.WorkerState == "joined"
	})
	p, err := m.InspectShare(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func manifestRows(t *testing.T, m *Manager, id string) string {
	t.Helper()
	rows, err := m.db.Query(`SELECT data FROM share_manifest_parts WHERE share=? ORDER BY part`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var all strings.Builder
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		all.WriteString(s)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return all.String()
}
func TestSharePublicationExactDecisionRetainsOriginalEvidenceAndReservation(t *testing.T) {
	f := newShareFixture(t)
	p := preparedPublication(t, f, 0)
	q := publicationRequest(p)
	original := manifestRows(t, f.m, p.ID)
	var native atomic.Int32
	f.m.shares.beforeNativeBuild = func(context.Context) { native.Add(1) }
	if _, err := f.m.PublishShare(t.Context(), q); err != nil {
		t.Fatal(err)
	}
	published := waitPublicationDone(t, f.m, p.ID)
	if published.State != "published" || published.Published == nil || published.Publish == nil || *published.Publish != q || published.PreparationWorker != p.Worker || published.Request != p.Request || *published.Summary != *p.Summary || !published.Prepared.Equal(*p.Prepared) || published.JournalSlots != 3 || original != manifestRows(t, f.m, p.ID) {
		t.Fatalf("original evidence changed: %+v", published)
	}
	lease, err := f.m.Inspect(p.Lease)
	if err != nil || lease.State != "active" || lease.Request.Resources != p.Request.Resources || !lease.Expires.Equal(p.Expires) {
		t.Fatal(lease, err)
	}
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != 384<<20 {
		t.Fatal(status.Reserved, err)
	}
	for i := 0; i < 3; i++ {
		got, err := f.m.PublishShare(t.Context(), q)
		if err != nil || got.Revision != published.Revision {
			t.Fatal(got, err)
		}
	}
	// Error receipts also cross the in-process API boundary and must be detached.
	changed := q
	changed.Key = "rejected-replacement"
	receipt, conflict := f.m.PublishShare(t.Context(), changed)
	if conflict == nil || receipt.Publish == nil || receipt.Summary == nil {
		t.Fatal(receipt, conflict)
	}
	receipt.Publish.Reason = "mutated conflict receipt"
	receipt.Summary.SHA256 = strings.Repeat("f", 64)
	receipt, busy := f.m.ReconcileShare(t.Context(), p.ID, published.Revision, "joined generation cannot reconcile")
	if busy == nil || receipt.Publish == nil {
		t.Fatal(receipt, busy)
	}
	receipt.Publish.Reason = "mutated reconcile receipt"
	checked, e := f.m.InspectShare(p.ID)
	if e != nil || checked.Publish.Reason != q.Reason || checked.Summary.SHA256 != p.Summary.SHA256 {
		t.Fatal("error receipt aliases durable authority", checked, e)
	}
	if native.Load() != 1 {
		t.Fatal("key retry started duplicate verification", native.Load())
	}
	for _, change := range []func(*sharelease.PublishRequest){func(q *sharelease.PublishRequest) { q.Reason = "changed" }, func(q *sharelease.PublishRequest) { q.Key = "replacement" }, func(q *sharelease.PublishRequest) { q.Revision++ }, func(q *sharelease.PublishRequest) { q.PreparedSHA256 = strings.Repeat("f", 64) }} {
		changed := q
		change(&changed)
		if _, err = f.m.PublishShare(t.Context(), changed); errorCode(err) != "idempotency_conflict" {
			t.Fatal(err)
		}
	}
	got, found, err := f.m.LookupSharePublication(q.Key)
	if err != nil || !found || got.ID != p.ID {
		t.Fatal(got, found, err)
	}
	// Detached exported receipts cannot mutate authority-owned current intent.
	got.Publish.Reason = "caller mutation"
	got, _, _ = f.m.LookupSharePublication(q.Key)
	if got.Publish.Reason != q.Reason {
		t.Fatal("intent aliases caller memory")
	}
	if _, err = f.m.End(p.Lease, "released"); err != nil {
		t.Fatal(err)
	}
	final, _ := f.m.InspectShare(p.ID)
	if final.State != "retired" || final.Retired == nil || final.JournalSlots != 0 || manifestRows(t, f.m, p.ID) != original {
		t.Fatal(final)
	}
	snapshot, err := sharelease.Load(t.Context(), f.m.db, f.m.host, f.m.owner)
	if err != nil || snapshot.Capacity.ReservedSlots != 0 {
		t.Fatal(snapshot.Capacity, err)
	}
	want := []string{"reserved", "worker_started", "prepared", "verify_reserved", "verify_started", "published", "retiring", "retired"}
	var transitions []string
	for _, j := range snapshot.Journal {
		transitions = append(transitions, j.Record.Transition)
	}
	if !reflect.DeepEqual(transitions, want) {
		t.Fatal(transitions)
	}
	f.close()
	reopened, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, found, err = reopened.LookupSharePublication(q.Key)
	if err != nil || !found || got.State != "retired" {
		t.Fatal(got, found, err)
	}
}

func TestSharePublicationRejectsCurrentTreeChangesWithoutReplacingManifest(t *testing.T) {
	for _, mutation := range []string{"content", "metadata", "addition", "deletion", "hardlink", "root"} {
		t.Run(mutation, func(t *testing.T) {
			f := newShareFixture(t)
			p := preparedPublication(t, f, 0)
			before := manifestRows(t, f.m, p.ID)
			payload := filepath.Join(f.storage.ManagedDir, "payload")
			var err error
			switch mutation {
			case "content":
				err = os.WriteFile(payload, []byte("unapproved bytes"), 0600)
			case "metadata":
				err = os.Chmod(payload, 0700)
			case "addition":
				err = os.WriteFile(filepath.Join(f.storage.ManagedDir, "added"), []byte("new"), 0600)
			case "deletion":
				err = os.Remove(payload)
			case "hardlink":
				err = os.Link(payload, filepath.Join(f.storage.ManagedDir, "alias"))
			case "root":
				err = os.Rename(f.storage.ManagedDir, f.storage.ManagedDir+"-old")
				if err == nil {
					err = os.Mkdir(f.storage.ManagedDir, 0700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.m.PublishShare(t.Context(), publicationRequest(p))
			if mutation == "root" {
				if err == nil {
					t.Fatal("adopted replaced root")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			final := waitPublicationDone(t, f.m, p.ID)
			if final.State != "failed" || final.Retired == nil || final.Published != nil || final.PreparationWorker != p.PreparationWorker || *final.Summary != *p.Summary || manifestRows(t, f.m, p.ID) != before {
				t.Fatalf("changed manifest replaced original: %+v", final)
			}
			lease, _ := f.m.Inspect(p.Lease)
			if lease.State != "terminal" || lease.Outcome != "failed" {
				t.Fatal(lease)
			}
		})
	}
}

func TestSharePublicationBlockedVerifierRetainsClaimsUntilActualJoin(t *testing.T) {
	for _, operation := range []string{"release", "cancel", "storage-release", "expiry", "close"} {
		t.Run(operation, func(t *testing.T) {
			f := newShareFixture(t)
			ttl := time.Duration(0)
			if operation == "expiry" {
				ttl = 350 * time.Millisecond
			}
			p := preparedPublication(t, f, ttl)
			entered, cancelled, release := blockShareNativeIO(t, f.m)
			if _, err := f.m.PublishShare(t.Context(), publicationRequest(p)); err != nil {
				t.Fatal(err)
			}
			<-entered
			var err error
			done := make(chan struct{})
			switch operation {
			case "release":
				_, err = f.m.End(p.Lease, "released")
			case "cancel":
				_, err = f.m.End(p.Lease, "cancelled")
			case "storage-release":
				_, err = f.m.End(f.storage.ID, "released")
			case "expiry":
				time.Sleep(time.Until(p.Expires) + 10*time.Millisecond)
				f.m.tick()
			case "close":
				go func() { f.close(); close(done) }()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("native cancellation did not arrive")
			}
			select {
			case <-done:
				t.Fatal("Close released native worker early")
			default:
			}
			status, err := f.m.Status()
			if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != 384<<20 || status.Reserved.Disk != 1<<20 {
				t.Fatal(status.Reserved, err)
			}
			if _, err = os.Stat(filepath.Join(f.storage.ManagedDir, "payload")); err != nil {
				t.Fatal("storage removed before native join", err)
			}
			if operation != "close" {
				other, e := f.m.Acquire(model.Request{Resources: model.Resources{CPU: .25}, TTL: time.Minute, Reason: "independent work during blocked verifier", Key: "verification-independent"}, nil)
				if e != nil {
					t.Fatal(e)
				}
				f.m.End(other.ID, "released")
			}
			release()
			if operation == "close" {
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("Close did not join")
				}
				return
			}
			final := waitPublicationDone(t, f.m, p.ID)
			if final.State != "cancelled" || final.Retired == nil || final.Published != nil {
				t.Fatal(final)
			}
			status, _ = f.m.Status()
			if status.Reserved.CPU != 0 || status.Reserved.Memory != 0 {
				t.Fatal(status.Reserved)
			}
		})
	}
}

func TestSharePublicationAdmissionRollbackAndLostAcknowledgmentNeverStartNative(t *testing.T) {
	for _, fault := range []string{"rollback", "lost-ack"} {
		t.Run(fault, func(t *testing.T) {
			f := newShareFixture(t)
			p := preparedPublication(t, f, 0)
			q := publicationRequest(p)
			var native atomic.Int32
			f.m.shares.beforeNativeBuild = func(context.Context) { native.Add(1) }
			before := shareRowCount(t, f.m, "share_journal")
			if fault == "rollback" {
				_, err := f.m.db.Exec(`CREATE TEMP TRIGGER reject_verify BEFORE UPDATE ON share_publications WHEN json_extract(NEW.data,'$.state')='verifying' BEGIN SELECT RAISE(ABORT,'reject verification intent'); END`)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				shareLoseNextCommitAcknowledgment(f.m)
			}
			_, err := f.m.PublishShare(t.Context(), q)
			if err == nil {
				t.Fatal("accepted failed publication admission")
			}
			if native.Load() != 0 {
				t.Fatal("unacknowledged intent started native verifier")
			}
			if fault == "rollback" {
				got, e := f.m.InspectShare(p.ID)
				if e != nil || got.Publish != nil || got.Revision != p.Revision || shareRowCount(t, f.m, "share_journal") != before {
					t.Fatal(got, e)
				}
				return
			}
			if errorCode(err) != "share_outcome_unknown" {
				t.Fatal(err)
			}
			if _, _, err = f.m.LookupSharePublication(q.Key); errorCode(err) != "share_evidence_unavailable" {
				t.Fatal("cache reported false absence", err)
			}
			snapshot, err := sharelease.Load(t.Context(), f.m.db, f.m.host, f.m.owner)
			if err != nil || snapshot.Publications[p.ID].State != "verifying" {
				t.Fatal(snapshot.Publications[p.ID], err)
			}
			f.close()
			m, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			got, found, err := m.LookupSharePublication(q.Key)
			if err != nil || !found || got.WorkerState != "unknown" || got.WorkerPhase != "verification" || got.PreparationWorker != p.PreparationWorker || len(m.shares.workers) != 0 {
				t.Fatal(got, found, err)
			}
			retry, err := m.PublishShare(t.Context(), q)
			if err != nil || retry.Revision != got.Revision {
				t.Fatal("retry replayed intent", retry, err)
			}
			if _, err = m.ReconcileShare(t.Context(), got.ID, got.Revision, "same process is still present"); errorCode(err) != "needs_attention" {
				t.Fatal("inferred same-process worker absent", err)
			}
		})
	}
}

func TestSharePublicationFinalLostAcknowledgmentRestoresDurableDecision(t *testing.T) {
	f := newShareFixture(t)
	p := preparedPublication(t, f, 0)
	q := publicationRequest(p)
	entered, _, release := blockShareNativeIO(t, f.m)
	if _, err := f.m.PublishShare(t.Context(), q); err != nil {
		t.Fatal(err)
	}
	<-entered
	shareLoseNextCommitAcknowledgment(f.m)
	release()
	shareWait(t, func() bool { f.m.mu.Lock(); defer f.m.mu.Unlock(); return f.m.shares.workers[p.ID] == nil })
	if _, _, err := f.m.LookupSharePublication(q.Key); errorCode(err) != "share_evidence_unavailable" {
		t.Fatal(err)
	}
	snapshot, err := sharelease.Load(t.Context(), f.m.db, f.m.host, f.m.owner)
	if err != nil || snapshot.Publications[p.ID].State != "published" {
		t.Fatal(snapshot.Publications[p.ID], err)
	}
	f.close()
	m, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	got, found, err := m.LookupSharePublication(q.Key)
	if err != nil || !found || got.State != "published" || got.WorkerState != "joined" {
		t.Fatal(got, found, err)
	}
	if _, err = m.End(p.Lease, "released"); err != nil {
		t.Fatal(err)
	}
}

func TestSharePublicationRestartUnknownRetiresOnlyAfterExactDeadWorkerObservation(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "authority")
	ready := filepath.Join(root, "ready.json")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestShareCrashChildWriter$", "-test.timeout=60s")
	child.Env = append(os.Environ(), "ARM_PRIVATE_SHARE_CRASH_HOME="+home, "ARM_PRIVATE_SHARE_CRASH_READY="+ready, "ARM_PRIVATE_SHARE_CRASH_PHASE=verification")
	child.Stdout = io.Discard
	child.Stderr = os.Stderr
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	var evidence struct {
		Publication sharelease.Publication
		Storage     model.Lease
	}
	shareWait(t, func() bool { raw, e := os.ReadFile(ready); return e == nil && json.Unmarshal(raw, &evidence) == nil })
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if child.Wait() == nil {
		t.Fatal("child survived")
	}
	m, err := Open(home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	p, err := m.InspectShare(evidence.Publication.ID)
	if err != nil || p.State != "needs_attention" || p.WorkerPhase != "verification" || p.Publish == nil || p.Summary == nil || len(m.shares.workers) != 0 {
		t.Fatal(p, err)
	}
	if _, err = m.End(p.Lease, "released"); errorCode(err) != "needs_attention" {
		t.Fatal(err)
	}
	retry, err := m.PublishShare(t.Context(), *p.Publish)
	if err != nil || retry.Revision != p.Revision {
		t.Fatal(retry, err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.Memory != 384<<20 {
		t.Fatal(status.Reserved, err)
	}
	final, err := m.ReconcileShare(t.Context(), p.ID, p.Revision, "exact verifier process died; retire unexposed generation")
	if err != nil || final.State != "cancelled" || final.PreparationWorker != evidence.Publication.PreparationWorker || final.Publish == nil || final.Published != nil {
		t.Fatal(final, err)
	}
}

func TestSharePublicationRetirementRefusesAnyConsumerOrUnavailableEvidence(t *testing.T) {
	for _, kind := range []string{"consumer", "fence", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			f := newShareFixture(t)
			p := preparedPublication(t, f, 0)
			if _, err := f.m.PublishShare(t.Context(), publicationRequest(p)); err != nil {
				t.Fatal(err)
			}
			waitPublicationDone(t, f.m, p.ID)
			f.m.mu.Lock()
			key := sharelease.OperationKey{Principal: "private", Key: "consumer"}
			switch kind {
			case "consumer":
				f.m.shares.state.Consumers[key] = sharelease.Consumer{Admission: sharelease.Admission{Control: sharelease.Control{Share: p.ID}}}
			case "fence":
				f.m.shares.state.Fences[key] = sharelease.Fence{Control: sharelease.Control{Share: p.ID}}
			case "unavailable":
				f.m.shares.failure = errors.New("missing validated cache")
			}
			f.m.mu.Unlock()
			if _, err := f.m.End(p.Lease, "released"); err == nil {
				t.Fatal("retired without positively empty evidence")
			}
			l, _ := f.m.Inspect(p.Lease)
			if !consuming(l.State) || l.Ended != nil {
				t.Fatal(l)
			}
			// Restore test-only fault before cleanup; no fake rows enter SQL authority.
			f.m.mu.Lock()
			delete(f.m.shares.state.Consumers, key)
			delete(f.m.shares.state.Fences, key)
			f.m.shares.failure = nil
			f.m.mu.Unlock()
		})
	}
}

func TestSharePublicationRejectsIntentWithoutFinalDiagnosticHeadroom(t *testing.T) {
	f := newShareFixture(t)
	request := shareRequest(f.storage, "bounded-publication")
	request.Reason = strings.Repeat("<&", 500) //6KiB canonical request reason, still admits preparation.
	p, err := f.m.PrepareShare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	shareWait(t, func() bool {
		f.m.mu.Lock()
		defer f.m.mu.Unlock()
		return f.m.shares.state.Publications[p.ID].State == "prepared" && f.m.shares.workers[p.ID] == nil
	})
	p, err = f.m.InspectShare(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	var native atomic.Int32
	f.m.shares.beforeNativeBuild = func(context.Context) { native.Add(1) }
	q := publicationRequest(p)
	q.Reason = strings.Repeat("x", 4000)
	_, err = f.m.PublishShare(t.Context(), q)
	if errorCode(err) != "share_capacity_exceeded" {
		t.Fatal("intent did not preserve finalization record headroom", err)
	}
	current, err := f.m.InspectShare(p.ID)
	if err != nil || current.Publish != nil || current.Revision != p.Revision || native.Load() != 0 {
		t.Fatal(current, err)
	}
	q.Reason = "bounded explicit approval"
	if _, err = f.m.PublishShare(t.Context(), q); err != nil {
		t.Fatal(err)
	}
	waitPublicationDone(t, f.m, p.ID)
	if _, err = f.m.End(p.Lease, "released"); err != nil {
		t.Fatal(err)
	}
}

func TestSharePublicationRecoveredRetirementPreservesCommittedOutcome(t *testing.T) {
	// The retiring transaction really commits before its ACK is lost. Reopen the
	// complete ledger, then race later intentions against the original decision.
	for _, original := range []string{"released", "cancelled", "expired"} {
		t.Run(original, func(t *testing.T) {
			f := newShareFixture(t)
			p := preparedPublication(t, f, 0)
			if _, err := f.m.PublishShare(t.Context(), publicationRequest(p)); err != nil {
				t.Fatal(err)
			}
			waitPublicationDone(t, f.m, p.ID)
			shareLoseNextCommitAcknowledgment(f.m)
			if _, err := f.m.End(p.Lease, original); errorCode(err) != "share_outcome_unknown" {
				t.Fatal(err)
			}
			durable, err := f.m.get(p.Lease)
			if err != nil || durable.State != "ending" || durable.Outcome != original {
				t.Fatal(durable, err)
			}
			f.close()
			m, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			recovered, err := m.InspectShare(p.ID)
			if err != nil || recovered.State != "retiring" {
				t.Fatal(recovered, err)
			}
			var wg sync.WaitGroup
			errs := make(chan error, 3)
			for _, followup := range []string{"released", "cancelled", "expired"} {
				wg.Go(func() {
					lease, e := m.End(p.Lease, followup)
					if e != nil {
						errs <- e
						return
					}
					if lease.Outcome != original || lease.State != "terminal" {
						errs <- fmt.Errorf("committed %s retirement changed to %+v on %s", original, lease, followup)
					}
				})
			}
			wg.Wait()
			close(errs)
			for e := range errs {
				t.Error(e)
			}
			final, err := m.InspectShare(p.ID)
			if err != nil || final.State != "retired" || final.JournalSlots != 0 {
				t.Fatal(final, err)
			}
			var n int
			if err = m.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='share.retired' AND entity=?`, p.Lease).Scan(&n); err != nil || n != 1 {
				t.Fatal("competing followups duplicated settlement", n, err)
			}
		})
	}
}

func TestSharePublicationRetiringWithoutRecordedDecisionIsUnavailable(t *testing.T) {
	for _, decision := range []string{"", "unknown-end"} {
		t.Run(decision, func(t *testing.T) {
			f := newShareFixture(t)
			p := preparedPublication(t, f, 0)
			if _, err := f.m.PublishShare(t.Context(), publicationRequest(p)); err != nil {
				t.Fatal(err)
			}
			waitPublicationDone(t, f.m, p.ID)
			shareLoseNextCommitAcknowledgment(f.m)
			if _, err := f.m.End(p.Lease, "released"); errorCode(err) != "share_outcome_unknown" {
				t.Fatal(err)
			}
			if _, err := f.m.db.Exec(`UPDATE leases SET data=json_set(data,'$.outcome',?) WHERE id=?`, decision, p.Lease); err != nil {
				t.Fatal(err)
			}
			f.close()
			m, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if m.sharePolicyError() == nil {
				t.Fatal("startup adopted a retiring row without a valid end decision")
			}
			if _, err = m.End(p.Lease, "cancelled"); errorCode(err) != "share_evidence_unavailable" {
				t.Fatal("invented recovery end decision", err)
			}
			lease, err := m.Inspect(p.Lease)
			if err != nil || lease.State != "ending" || lease.Ended != nil || lease.Outcome != decision {
				t.Fatal(lease, err)
			}
			if _, err = m.StorageHandoff(f.storage.ID); errorCode(err) != "share_evidence_unavailable" {
				t.Fatal("ambiguous retiring decision permitted storage handoff", err)
			}
		})
	}
}

func TestSharePublicationRecoveredRetirementExpiryKeepsReleaseDecision(t *testing.T) {
	f := newShareFixture(t)
	p := preparedPublication(t, f, 3*time.Second)
	if _, err := f.m.PublishShare(t.Context(), publicationRequest(p)); err != nil {
		t.Fatal(err)
	}
	waitPublicationDone(t, f.m, p.ID)
	shareLoseNextCommitAcknowledgment(f.m)
	if _, err := f.m.End(p.Lease, "released"); errorCode(err) != "share_outcome_unknown" {
		t.Fatal(err)
	}
	f.close()
	// Trigger actual fixed-deadline expiry after restarting an unresolved retiring
	// generation, not just an explicit End("expired") call.
	time.Sleep(max(time.Duration(0), time.Until(p.Expires)) + 10*time.Millisecond)
	m, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.tick()
	lease, err := m.End(p.Lease, "cancelled")
	if err != nil || lease.State != "terminal" || lease.Outcome != "released" {
		t.Fatal("expiry/late cancellation rewrote committed release", lease, err)
	}
}
