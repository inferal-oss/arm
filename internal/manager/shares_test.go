package manager

import (
	"context"
	"encoding/json"
	"errors"
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

type shareFixture struct {
	m       *Manager
	storage model.Lease
	close   func()
}

func newShareFixture(t *testing.T) *shareFixture {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "authority"), model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	close := func() {
		once.Do(func() {
			if err := m.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(close)
	s, err := m.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1 << 20}, TTL: 5 * time.Minute, Reason: "synthetic immutable input staging", Key: "share-source"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(s.ManagedDir, "payload"), []byte("approved fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return &shareFixture{m, s, close}
}
func shareRequest(s model.Lease, key string) sharelease.PrepareRequest {
	return sharelease.PrepareRequest{MemoryPolicy: sharelease.PreparationMemory().Policy, Key: key, Storage: s.ID, StorageRevision: s.Revision, Reason: "prepare exact immutable fixture input", TTL: time.Minute, Resources: model.Resources{CPU: 1, Memory: sharelease.PreparationMemory().Total}}
}
func blockShareNativeIO(t *testing.T, m *Manager) (<-chan struct{}, <-chan struct{}, func()) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	entered, cancelled := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { write.Close() }) }
	t.Cleanup(func() { release(); read.Close() })
	m.shares.beforeNativeBuild = func(ctx context.Context) {
		close(entered)
		go func() { <-ctx.Done(); close(cancelled) }()
		var b [1]byte
		_, _ = read.Read(b[:])
	}
	return entered, cancelled, release
}
func shareWait(t *testing.T, fn func() bool) {
	t.Helper()
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("share lifecycle did not settle")
}
func shareRowCount(t *testing.T, m *Manager, table string) int {
	t.Helper()
	var n int
	if err := m.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func TestSharePreparationUsesOrdinaryAtomicAdmissionAndPrivateManifest(t *testing.T) {
	f := newShareFixture(t)
	q := shareRequest(f.storage, "prepare-once")
	p, err := f.m.PrepareShare(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	shareWait(t, func() bool { current, e := f.m.InspectShare(p.ID); return e == nil && current.State == "prepared" })
	p, err = f.m.InspectShare(p.ID)
	if err != nil || p.Summary == nil || p.Published != nil || p.WorkerState != "joined" {
		t.Fatal(p, err)
	}
	lease, err := f.m.Inspect(p.Lease)
	if err != nil || lease.Request.Kind != "share-publication" || lease.Request.StorageLease != f.storage.ID || lease.State != "active" || lease.Request.Resources != q.Resources {
		t.Fatal(lease, err)
	}
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != q.Resources.Memory || status.Reserved.Disk != f.storage.Request.Resources.Disk {
		t.Fatal(status.Reserved, err)
	}
	preview, err := f.m.StoragePreview(t.Context(), f.storage.ID)
	if err != nil || preview.CanClean || !reflect.DeepEqual(preview.Consumers, []string{p.Lease}) {
		t.Fatal(preview, err)
	}
	if _, err = f.m.Renew(p.Lease, time.Hour); errorCode(err) != "share_lifetime_fixed" {
		t.Fatal("generic renewal changed fixed review scope", err)
	}
	if _, err = f.m.Renew(f.storage.ID, time.Hour); errorCode(err) != "storage_in_use" {
		t.Fatal("backing revision changed while pinned", err)
	}
	if _, err = f.m.StorageHandoff(f.storage.ID); errorCode(err) != "storage_in_use" {
		t.Fatal("handed off pinned storage", err)
	}
	replay, err := f.m.PrepareShare(t.Context(), q)
	if err != nil || replay.ID != p.ID || replay.Revision != p.Revision {
		t.Fatal("key replay reconstructed native work", replay, err)
	}
	q.Reason = "different reason"
	if _, err = f.m.PrepareShare(t.Context(), q); errorCode(err) != "idempotency_conflict" {
		t.Fatal(err)
	}
	if _, err = f.m.End(p.Lease, "released"); err != nil {
		t.Fatal(err)
	}
	final, err := f.m.InspectShare(p.ID)
	if err != nil || final.State != "cancelled" || final.Retired == nil {
		t.Fatal(final, err)
	}
	state, err := sharelease.Load(t.Context(), f.m.db, f.m.host, f.m.owner)
	if err != nil || state.Capacity.ReservedSlots != 0 || len(state.Journal) < 4 {
		t.Fatal(state.Capacity, err)
	}
	source, _ := f.m.Inspect(f.storage.ID)
	if source.State != "active" {
		t.Fatal("publication retirement ended independent storage")
	}
}
func TestShareReleaseStorageReleaseAndExpiryHoldUntilActualNativeJoin(t *testing.T) {
	for _, operation := range []string{"release", "storage-release", "expiry"} {
		t.Run(operation, func(t *testing.T) {
			f := newShareFixture(t)
			entered, cancelled, release := blockShareNativeIO(t, f.m)
			q := shareRequest(f.storage, operation)
			if operation == "expiry" {
				q.TTL = 80 * time.Millisecond
			}
			p, err := f.m.PrepareShare(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("native pipe I/O did not block")
			}
			switch operation {
			case "release":
				_, err = f.m.End(p.Lease, "released")
			case "storage-release":
				_, err = f.m.End(f.storage.ID, "released")
			case "expiry":
				time.Sleep(100 * time.Millisecond)
				f.m.tick()
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("stop did not reach native worker outside manager lock")
			}
			current, _ := f.m.Inspect(p.Lease)
			status, e := f.m.Status()
			if current.State != "ending" || e != nil || status.Reserved.CPU != 1 || status.Reserved.Memory != q.Resources.Memory || status.Reserved.Disk != 1<<20 {
				t.Fatalf("released promise before native join state%s resources%+v err%v", current.State, status.Reserved, e)
			}
			if _, err = os.Stat(filepath.Join(f.storage.ManagedDir, "payload")); err != nil {
				t.Fatal("backing removed before native worker joined", err)
			}
			// Status/ordinary unrelated admission remain available while actual native
			// pipe.Read is blocked, proving no Manager.mu is held over the I/O or join.
			other, e := f.m.Acquire(model.Request{Resources: model.Resources{CPU: .25}, TTL: time.Minute, Reason: "independent CPU claim while native I/O is blocked", Key: "unrelated"}, nil)
			if e != nil {
				t.Fatal(e)
			}
			f.m.End(other.ID, "released")
			release()
			shareWait(t, func() bool { l, _ := f.m.Inspect(p.Lease); return l.State == "terminal" })
			current, _ = f.m.Inspect(p.Lease)
			if operation == "expiry" && current.Outcome != "expired" {
				t.Fatal("lost explicit expiry", current.Outcome)
			}
			status, _ = f.m.Status()
			if status.Reserved.CPU != 0 || status.Reserved.Memory != 0 {
				t.Fatal("joined worker resource promise retained", status.Reserved)
			}
			if operation == "storage-release" {
				shareWait(t, func() bool {
					f.m.tick()
					l, _ := f.m.Inspect(f.storage.ID)
					return l.State == "terminal" || l.State == "needs_attention"
				})
				l, _ := f.m.Inspect(f.storage.ID)
				if l.Artifact.Phase == "owned" {
					t.Fatal("storage cleanup never advanced after join", l.Failure)
				}
			}
		})
	}
}
func TestShareCloseCancelsBeforeJoinAndKeepsAuthorityUntilNativeDone(t *testing.T) {
	f := newShareFixture(t)
	entered, cancelled, release := blockShareNativeIO(t, f.m)
	p, err := f.m.PrepareShare(t.Context(), shareRequest(f.storage, "close"))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	done := make(chan struct{})
	go func() { f.close(); close(done) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Close waited before cancelling native scope")
	}
	select {
	case <-done:
		t.Fatal("Close released authority while native worker remained blocked")
	case <-time.After(40 * time.Millisecond):
	}
	status, err := f.m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory == 0 {
		t.Fatal(status.Reserved, err)
	}
	release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not join after native completion")
	}
	reopened, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	final, err := reopened.InspectShare(p.ID)
	if err != nil || final.WorkerState != "joined" || final.Retired == nil {
		t.Fatal(final, err)
	}
}
func TestShareAtomicFailureRollsBackLeaseBudgetJournalAndNoNativeWork(t *testing.T) {
	f := newShareFixture(t)
	var started atomic.Int32
	f.m.shares.beforeNativeBuild = func(context.Context) { started.Add(1) }
	beforeLeases := shareRowCount(t, f.m, "leases")
	beforeBindings := shareRowCount(t, f.m, "lease_budget_bindings")
	beforeEvents := shareRowCount(t, f.m, "events")
	if _, err := f.m.db.Exec(`CREATE TEMP TRIGGER reject_share_commit BEFORE INSERT ON events WHEN NEW.type='lease.created' BEGIN SELECT RAISE(ABORT,'private failure after share intent insert'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := f.m.PrepareShare(t.Context(), shareRequest(f.storage, "rollback"))
	if err == nil {
		t.Fatal("injected transaction failure accepted")
	}
	if started.Load() != 0 || shareRowCount(t, f.m, "leases") != beforeLeases || shareRowCount(t, f.m, "lease_budget_bindings") != beforeBindings || shareRowCount(t, f.m, "events") != beforeEvents {
		t.Fatal("failed transaction leaked native worker/lease/budget/events")
	}
	var group int
	if err = f.m.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name GLOB 'share_*'`).Scan(&group); err != nil || group != 0 {
		t.Fatal("rollback leaked optional schema/journal", group, err)
	}
	if _, found, err := f.m.LookupSharePreparation("rollback"); err != nil || found {
		t.Fatal(found, err)
	}
}
func TestWireCannotAuthorizeShareKindsOrRuntimeAdmission(t *testing.T) {
	f := newShareFixture(t)
	for _, kind := range []string{"share-preparation", "share-publication", "share-consumer"} {
		q := model.Request{Kind: kind, StorageLease: f.storage.ID, Resources: model.Resources{CPU: 1, Memory: sharelease.PreparationMemory().Total}, TTL: time.Minute, Reason: "forged wire authority", Key: kind}
		if _, err := f.m.Acquire(q, nil); errorCode(err) != "invalid_request" {
			t.Fatal(kind, err)
		}
	}
	var r RunRequest
	if err := json.Unmarshal([]byte(`{"kind":"share-publication","shareAdmission":{"preparation":{"worker":"run"}},"beforeNativeBuild":true}`), &r); err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Acquire(r.Request, &r); errorCode(err) != "invalid_request" {
		t.Fatal("raw runtime hook accepted", err)
	}
	if f.m.shares.state.Enabled || len(f.m.shares.workers) != 0 {
		t.Fatal("wire attempt initialized native share authority")
	}
}

// The child is an actual separately owned Go process. It is killed ONLY through
// its live exec.Cmd handle after durable intent + real native pipe I/O; no stored
// PID grants control. Restart/reconciliation never replay Build.
func TestShareCrashChildWriter(t *testing.T) {
	home := os.Getenv("ARM_PRIVATE_SHARE_CRASH_HOME")
	ready := os.Getenv("ARM_PRIVATE_SHARE_CRASH_READY")
	if home == "" {
		return
	}
	m, err := Open(home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	source, err := m.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1 << 20}, TTL: 5 * time.Minute, Reason: "private abrupt-death fixture", Key: "crash-source"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source.ManagedDir, "payload"), []byte("private managed fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer write.Close()
	defer read.Close()
	entered := make(chan struct{})
	m.shares.beforeNativeBuild = func(context.Context) { close(entered); var b [1]byte; _, _ = read.Read(b[:]) }
	p, err := m.PrepareShare(context.Background(), shareRequest(source, "crash-prepare"))
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	raw, _ := json.Marshal(struct {
		Publication sharelease.Publication
		Storage     model.Lease
	}{p, source})
	if err = os.WriteFile(ready, raw, 0600); err != nil {
		t.Fatal(err)
	}
	select {}
}
func TestShareRestartHoldsUnknownUntilExactWorkerAbsenceReconciled(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "authority")
	ready := filepath.Join(root, "ready.json")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(exe, "-test.run=^TestShareCrashChildWriter$", "-test.timeout=60s")
	child.Env = append(os.Environ(), "ARM_PRIVATE_SHARE_CRASH_HOME="+home, "ARM_PRIVATE_SHARE_CRASH_READY="+ready)
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
	shareWait(t, func() bool {
		raw, err := os.ReadFile(ready)
		return err == nil && json.Unmarshal(raw, &evidence) == nil
	})
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = child.Wait(); err == nil {
		t.Fatal("fixture did not die abruptly")
	}
	m, err := Open(home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	p, err := m.InspectShare(evidence.Publication.ID)
	if err != nil || p.State != "needs_attention" || p.WorkerState != "unknown" {
		t.Fatal(p, err)
	}
	status, err := m.Status()
	if err != nil || status.Reserved.CPU != 1 || status.Reserved.Memory == 0 || status.Reserved.Disk != 1<<20 {
		t.Fatal("restart inferred no obligation", status.Reserved, err)
	}
	if _, err = m.StorageHandoff(evidence.Storage.ID); errorCode(err) != "storage_in_use" {
		t.Fatal("orphan allowed handoff", err)
	}
	if _, err = m.End(p.Lease, "released"); errorCode(err) != "needs_attention" {
		t.Fatal("orphan ordinary release returned credits", err)
	}
	if len(m.shares.workers) != 0 {
		t.Fatal("restart automatically replayed hashing")
	}
	result, err := m.ReconcileShare(t.Context(), p.ID, p.Revision, "observed exact prior process absent; retire unresolved preparation")
	if err != nil || result.State != "cancelled" || result.Retired == nil {
		t.Fatal(result, err)
	}
	final, _ := m.Inspect(p.Lease)
	if final.Outcome != "interrupted" || final.State != "terminal" {
		t.Fatal(final)
	}
	if _, err = m.ReconcileShare(t.Context(), p.ID, p.Revision, "stale observation"); !errors.As(err, new(*model.Error)) {
		t.Fatal("old revision unexpectedly reused", err)
	}
}

func TestShareDamagedLedgerHoldsOrphanStorageButUnrelatedExpiryWorks(t *testing.T) {
	for _, damage := range []string{"missing-publication", "corrupt-record", "missing-publication-and-kind"} {
		t.Run(damage, func(t *testing.T) {
			f := newShareFixture(t)
			q := shareRequest(f.storage, "damaged-ledger")
			p, err := f.m.PrepareShare(t.Context(), q)
			if err != nil {
				t.Fatal(err)
			}
			shareWait(t, func() bool { v, e := f.m.InspectShare(p.ID); return e == nil && v.State == "prepared" })
			ordinary, err := f.m.Acquire(model.Request{Resources: model.Resources{CPU: 1}, TTL: time.Millisecond, Reason: "unrelated safe expiry", Key: "safe-expiry"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if damage == "corrupt-record" {
				_, err = f.m.db.Exec(`UPDATE share_publications SET data='{}' WHERE id=?`, p.ID)
			} else {
				_, err = f.m.db.Exec(`DELETE FROM share_publications WHERE id=?`, p.ID)
				if err == nil && damage == "missing-publication-and-kind" {
					_, err = f.m.db.Exec(`UPDATE leases SET data=json_set(data,'$.request.kind','execution') WHERE id=?`, p.Lease)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			f.close()
			m, err := Open(f.m.home, model.Resources{CPU: 4, Memory: 768 << 20, Disk: 64 << 20})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if m.sharePolicyError() == nil {
				t.Fatal("damaged ledger silently treated as absent")
			}
			if _, err = m.End(p.Lease, "released"); errorCode(err) != "share_evidence_unavailable" {
				t.Fatal("orphan credits returned", err)
			}
			preview, err := m.StoragePreview(t.Context(), f.storage.ID)
			if err != nil || preview.CanClean {
				t.Fatal(preview, err)
			}
			if _, err = m.StorageHandoff(f.storage.ID); errorCode(err) != "share_evidence_unavailable" {
				t.Fatal("orphan handoff accepted", err)
			}
			backing, e := m.End(f.storage.ID, "released")
			if e != nil || backing.State != "needs_attention" || backing.Ended != nil {
				t.Fatal("ambiguous storage obligation released", backing, e)
			}
			m.tick()
			ended, err := m.Inspect(ordinary.ID)
			if err != nil || ended.State != "terminal" || ended.Outcome != "expired" {
				t.Fatal("share degradation prevented unrelated expiry", ended, err)
			}
			held, err := m.Inspect(p.Lease)
			if err != nil || !consuming(held.State) {
				t.Fatal("orphan claim lost", held, err)
			}
			contents, err := os.ReadFile(filepath.Join(f.storage.ManagedDir, "payload"))
			if err != nil || string(contents) != "approved fixture" {
				t.Fatal("ambiguous backing files changed", err)
			}
		})
	}
}

func TestShareTransitionPlanRejectsUnsupportedPublicationBeforeTransaction(t *testing.T) {
	f := newShareFixture(t)
	p, err := f.m.PrepareShare(t.Context(), shareRequest(f.storage, "transition-guard"))
	if err != nil {
		t.Fatal(err)
	}
	shareWait(t, func() bool { v, e := f.m.InspectShare(p.ID); return e == nil && v.State == "prepared" })
	p, err = f.m.InspectShare(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	before := shareRowCount(t, f.m, "share_journal")
	at := shareMaxTime(p.Updated, time.Now().UTC())
	p.State = "published"
	p.Published = &at
	p.Updated = at
	p.Revision++
	p.JournalSlots--
	if _, err = f.m.planSharePublication(p, "published"); errorCode(err) != "share_evidence_unavailable" {
		t.Fatal("unjoined public lifecycle accepted", err)
	}
	if shareRowCount(t, f.m, "share_journal") != before {
		t.Fatal("refusal changed journal")
	}
}

func TestSharePreparationRefusesBelowMemoryFloorBeforeNativeWorkOrMutation(t *testing.T) {
	f := newShareFixture(t)
	var native atomic.Int32
	f.m.shares.beforeNativeBuild = func(context.Context) { native.Add(1) }
	before := shareRowCount(t, f.m, "leases")
	events := shareRowCount(t, f.m, "events")
	for _, kind := range []string{"below-floor", "unknown-policy"} {
		q := shareRequest(f.storage, kind)
		if kind == "below-floor" {
			q.Resources.Memory--
		} else {
			q.MemoryPolicy = "unreviewed-policy"
		}
		if _, err := f.m.PrepareShare(t.Context(), q); errorCode(err) != "invalid_request" {
			t.Fatal(kind, err)
		}
	}
	if native.Load() != 0 || shareRowCount(t, f.m, "leases") != before || shareRowCount(t, f.m, "events") != events || f.m.shares.state.Enabled {
		t.Fatal("unfunded request started or mutated admitted state")
	}
	if sharelease.PreparationMemory().Total != 384<<20 {
		t.Fatal("typed default policy changed without review")
	}
}

func TestShareFinalDiagnosticCannotOutgrowAdmittedRecord(t *testing.T) {
	f := newShareFixture(t)
	q := shareRequest(f.storage, "large-final-record")
	q.Reason = strings.Repeat("<&", 500)
	p, err := f.m.PrepareShare(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	shareWait(t, func() bool { v, e := f.m.InspectShare(p.ID); return e == nil && v.State == "prepared" })
	p, err = f.m.InspectShare(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := shareMaxTime(p.Updated, time.Now().UTC())
	p.State = "failed"
	p.WorkerState = "joined"
	p.Retired = &at
	p.Updated = at
	p.JournalSlots = 0
	p.Revision++
	p.Reason = "Native worker joined; bounded failure retained"
	p.Failure = shareDiagnostic(errors.New(strings.Repeat("<&\"\n", 2000)))
	raw, err := sharelease.Canonical(p)
	if err != nil {
		t.Fatal("accepted original request could not retain final bounded diagnostic", err)
	}
	if _, err = sharelease.DecodePublication(raw); err != nil {
		t.Fatal(err)
	}
	diagnostic, _ := json.Marshal(p.Failure)
	if len(diagnostic) > 3800 || !strings.Contains(p.Failure, "truncated") {
		t.Fatal("serialized diagnostic not bounded", len(diagnostic))
	}
}
