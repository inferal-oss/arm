package manager

import (
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func TestBulkEndDurableSnapshotAndPartialFailure(t *testing.T) {
	m := newManager(t)
	r := request("unverified")
	r.Resources.CPU = .5
	unknown, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	unknown.State = "needs_attention"
	unknown.Failure = "recovered group cannot be verified"
	if err = m.put(unknown, "test.recovered"); err != nil {
		t.Fatal(err)
	}
	r.Key = "ordinary"
	ordinary, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	bulk, err := m.BulkEnd(model.BulkEndRequest{Action: "release", Key: "batch"})
	if err != nil || len(bulk.Results) != 2 || len(bulk.Remaining) != 1 || bulk.Remaining[0].ID != unknown.ID || bulk.Remaining[0].Error == nil {
		t.Fatalf("lost partial failure: %+v %v", bulk, err)
	}
	ended, _ := m.Inspect(ordinary.ID)
	if ended.State != "terminal" || ended.Outcome != "released" {
		t.Fatalf("ordinary lease survived: %+v", ended)
	}
	r.Key = "after-snapshot"
	later, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := m.BulkEnd(model.BulkEndRequest{Action: "release", Key: "batch"})
	if err != nil || len(retry.Results) != 2 {
		t.Fatalf("lost retry snapshot: %+v %v", retry, err)
	}
	still, _ := m.Inspect(later.ID)
	if still.State != "active" {
		t.Fatal("retry swept up new work")
	}
	if _, err = m.BulkEnd(model.BulkEndRequest{Action: "cancel", Key: "batch"}); err == nil {
		t.Fatal("changed action reused key")
	}
}

func TestShutdownWaitCancelsQueueBlocksAdmissionAndRenewal(t *testing.T) {
	m := newManager(t)
	l, err := m.Acquire(request("active"), nil)
	if err != nil {
		t.Fatal(err)
	}
	queuedRequest := request("queued")
	queuedRequest.Queue = true
	q, err := m.Acquire(queuedRequest, nil)
	if err != nil || q.State != "queued" {
		t.Fatal(err, q.State)
	}
	if _, err = m.BeginShutdown(model.ShutdownRequest{}); err == nil {
		t.Fatal("idle-only shutdown ended active leases")
	}
	report, err := m.BeginShutdown(model.ShutdownRequest{Wait: true})
	if err != nil || report.Phase != "draining" || len(report.CancelledQueued) != 1 || report.CancelledQueued[0].State != "terminal" {
		t.Fatalf("queue not cancelled during drain: %+v %v", report, err)
	}
	if _, err = m.Acquire(request("new"), nil); err == nil {
		t.Fatal("admission remained open")
	}
	if _, err = m.Renew(l.ID, 2*time.Minute); err == nil {
		t.Fatal("renewal prolonged drain")
	}
	m.tick()
	q, _ = m.Inspect(q.ID)
	if q.State != "terminal" || q.Outcome != "cancelled" {
		t.Fatalf("queue granted during drain: %+v", q)
	}
	if _, err = m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	report, err = m.AdvanceShutdown()
	if err != nil || report.Phase != "ready" || len(report.Remaining) != 0 {
		t.Fatalf("drain never settled: %+v %v", report, err)
	}
}

func TestShutdownTimeoutReleasesAndKeepsUnresolvedCharged(t *testing.T) {
	m := newManager(t)
	l, err := m.Acquire(request("unverified-shutdown"), nil)
	if err != nil {
		t.Fatal(err)
	}
	l.State = "needs_attention"
	l.Failure = "supervision was lost"
	if err = m.put(l, "test.lost"); err != nil {
		t.Fatal(err)
	}
	report, err := m.BeginShutdown(model.ShutdownRequest{Wait: true, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	report, err = m.AdvanceShutdown()
	if err != nil || report.ForceApplied {
		t.Fatal("forced early", err)
	}
	past := time.Now().Add(-time.Second)
	report.Deadline = &past
	m.shutdownDeadline = past
	if err = m.saveShutdown(report, "test.timeout"); err != nil {
		t.Fatal(err)
	}
	report, err = m.AdvanceShutdown()
	if err != nil || !report.ForceApplied || report.Phase != "blocked" || len(report.Remaining) != 1 || report.Results[0].Error == nil {
		t.Fatalf("unverified obligation disappeared: %+v %v", report, err)
	}
	s, _ := m.Status()
	if s.Reserved.CPU != 1 {
		t.Fatal("unknown resource became free")
	}
}

func TestShutdownRestartExplicitlyRecordsInterruptedDrain(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	budget := model.Resources{CPU: 1, Disk: 2048}
	m, err := Open(home, budget)
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire(request("restart"), nil)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	if _, err = m.BeginShutdown(model.ShutdownRequest{Wait: true, Timeout: time.Hour}); err != nil {
		m.Close()
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, budget)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	report, err := m.ShutdownStatus()
	if err != nil || report.Phase != "interrupted" {
		t.Fatalf("restart hid interrupted intent: %+v %v", report, err)
	}
	if _, err = m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Acquire(request("after-restart"), nil); err != nil {
		t.Fatal("restart stranded process-local drain", err)
	}
	events, err := m.Events(0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "shutdown.interrupted" {
			found = true
		}
	}
	if !found {
		t.Fatal("interruption was not journalled")
	}
}

func TestBulkScopeSurvivesAuthorityRestart(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	budget := model.Resources{CPU: 1, Disk: 2048}
	m, err := Open(home, budget)
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Acquire(request("before-restart"), nil)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	operation := model.BulkEndRequest{Action: "cancel", Key: "persistent-batch"}
	if _, err = m.BulkEnd(operation); err != nil {
		m.Close()
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, budget)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	later, err := m.Acquire(request("new-authority-work"), nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := m.BulkEnd(operation)
	if err != nil || len(report.Results) != 1 || report.Results[0].ID != first.ID {
		t.Fatalf("forgot durable bulk scope: %+v %v", report, err)
	}
	current, _ := m.Inspect(later.ID)
	if current.State != "active" {
		t.Fatal("retry after restart cancelled newer work")
	}
}

func TestBulkScopeJournalFailureDoesNotBeginRelease(t *testing.T) {
	m := newManager(t)
	l, err := m.Acquire(request("journal-before-end"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`CREATE TRIGGER reject_bulk BEFORE INSERT ON events WHEN NEW.type='leases.bulk_started' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	operation := model.BulkEndRequest{Action: "release", Key: "journal-rollback"}
	if _, err = m.BulkEnd(operation); err == nil {
		t.Fatal("failed journal began an unrecorded bulk operation")
	}
	current, _ := m.Inspect(l.ID)
	if current.State != "active" {
		t.Fatal("release began before durable scope")
	}
	var count int
	if err = m.db.QueryRow(`SELECT count(*) FROM metadata WHERE key='bulk:journal-rollback'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphaned scope escaped transaction", err)
	}
	if _, err = m.db.Exec(`DROP TRIGGER reject_bulk`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.BulkEnd(operation); err != nil {
		t.Fatal("exact operation could not retry after rollback", err)
	}
}

func TestBulkCancelRacesExpiryWithoutDoubleSettlement(t *testing.T) {
	m := newManager(t)
	r := request("expiry-race")
	r.TTL = time.Millisecond
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { m.tick() })
	wg.Go(func() {
		if _, err := m.BulkEnd(model.BulkEndRequest{Action: "cancel", Key: "expiry-cancel"}); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	end, err := m.Inspect(l.ID)
	if err != nil || end.State != "terminal" {
		t.Fatalf("not settled: %+v %v", end, err)
	}
	s, _ := m.Status()
	if s.Reserved.CPU != 0 {
		t.Fatal("double settlement damaged capacity")
	}
}

func TestShutdownRejectsConflictingOptions(t *testing.T) {
	m := newManager(t)
	for _, r := range []model.ShutdownRequest{{Wait: true, Force: true}, {Timeout: time.Second}, {Wait: true, Timeout: -time.Second}} {
		if _, err := m.BeginShutdown(r); err == nil {
			t.Fatalf("accepted conflicting request: %+v", r)
		}
	}
}

func TestShutdownReconnectKeepsDeadlineAndForceEscalates(t *testing.T) {
	m := newManager(t)
	if _, err := m.Acquire(request("drain-escalation"), nil); err != nil {
		t.Fatal(err)
	}
	first, err := m.BeginShutdown(model.ShutdownRequest{Wait: true, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	deadline := m.shutdownDeadline
	retry, err := m.BeginShutdown(model.ShutdownRequest{Wait: true, Timeout: time.Hour})
	if err != nil || retry.ID != first.ID || m.shutdownDeadline != deadline {
		t.Fatal("reconnect restarted timeout", err)
	}
	if _, err = m.BeginShutdown(model.ShutdownRequest{Wait: true, Timeout: 2 * time.Hour}); err == nil {
		t.Fatal("changed timeout silently reset the drain")
	}
	forced, err := m.BeginShutdown(model.ShutdownRequest{Force: true})
	if err != nil || forced.ID != first.ID || forced.Phase != "forcing" || forced.Deadline != nil {
		t.Fatalf("force did not escalate existing drain: %+v %v", forced, err)
	}
	final, err := m.AdvanceShutdown()
	if err != nil || final.Phase != "ready" || !final.ForceApplied {
		t.Fatalf("force escalation failed: %+v %v", final, err)
	}
}

func TestFailedForceEscalationRetainsOriginalDeadline(t *testing.T) {
	m := newManager(t)
	if _, err := m.Acquire(request("failed-escalation"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BeginShutdown(model.ShutdownRequest{Wait: true, Timeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	deadline := m.shutdownDeadline
	if _, err := m.db.Exec(`CREATE TRIGGER reject_force BEFORE INSERT ON events WHEN NEW.type='shutdown.force_requested' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.BeginShutdown(model.ShutdownRequest{Force: true}); err == nil {
		t.Fatal("failed force journal was accepted")
	}
	report, err := m.ShutdownStatus()
	if err != nil || report.Phase != "draining" || m.shutdownDeadline != deadline {
		t.Fatalf("failed escalation erased original drain deadline: %+v %v", report, err)
	}
	m.shutdownDeadline = time.Now().Add(-time.Second)
	report, err = m.AdvanceShutdown()
	if err != nil || report.Phase != "ready" || !report.ForceApplied {
		t.Fatalf("original timer could no longer force: %+v %v", report, err)
	}
}

func TestForcedShutdownReportsPerLeaseFailureWithoutWaitingForever(t *testing.T) {
	m := newManager(t)
	r := request("force-error")
	r.Resources.CPU = .5
	failed, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Key = "force-ok"
	healthy, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`CREATE TRIGGER reject_one_release BEFORE INSERT ON events WHEN NEW.type='lease.released' AND json_extract(NEW.data,'$.request.idempotency_key')='force-error' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.BeginShutdown(model.ShutdownRequest{Force: true}); err != nil {
		t.Fatal(err)
	}
	report, err := m.AdvanceShutdown()
	if err != nil || report.Phase != "blocked" || len(report.Remaining) != 1 || report.Remaining[0].ID != failed.ID {
		t.Fatalf("failed release stayed forcing indefinitely: %+v %v", report, err)
	}
	firstID := report.ID
	good, _ := m.Inspect(healthy.ID)
	bad, _ := m.Inspect(failed.ID)
	if good.State != "terminal" || bad.State != "active" {
		t.Fatalf("partial release was not preserved: good=%+v bad=%+v", good, bad)
	}
	if _, err = m.db.Exec(`DROP TRIGGER reject_one_release`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.BeginShutdown(model.ShutdownRequest{Force: true}); err != nil {
		t.Fatal(err)
	}
	report, err = m.AdvanceShutdown()
	if err != nil || report.ID != firstID || report.Phase != "ready" || len(report.Remaining) != 0 {
		t.Fatalf("explicit retry did not settle original frozen scope: %+v %v", report, err)
	}
}

func TestBulkCancelOwnedBundleRetainsProviderUncertainty(t *testing.T) {
	m := newManager(t)
	m.Start()
	foreign := exec.Command("/bin/sleep", "30")
	if err := foreign.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { foreign.Process.Kill(); foreign.Wait() }()
	s, err := m.Acquire(storageRequest("bulk-storage"), nil)
	if err != nil {
		t.Fatal(err)
	}
	pr := portRequest("bulk-ports")
	pr.TTL = 2 * time.Minute
	p, err := m.Acquire(pr, nil)
	if err != nil {
		t.Fatal(err)
	}
	capacity := int64(1)
	pool, err := m.MutatePool(model.PoolMutation{Operation: "create", Name: "bulk-slot", Scope: "global", Capacity: &capacity, Reason: "exercise bundled shutdown", Key: "bulk-slot"})
	if err != nil {
		t.Fatal(err)
	}
	r := request("bulk-owned-job")
	r.TTL = 30 * time.Second
	r.StorageLease = s.ID
	r.PortLease = p.ID
	r.Pools = []model.PoolClaim{{Pool: pool.Pool.ID, Quantity: 1}}
	run := RunRequest{Request: r, CWD: t.TempDir(), Command: []string{"/bin/sleep", "30"}}
	l, err := m.Acquire(r, &run)
	if err != nil {
		t.Fatal(err)
	}
	report, err := m.BulkEnd(model.BulkEndRequest{Action: "cancel", Key: "cancel-bundle"})
	if err != nil || len(report.Results) != 3 {
		t.Fatalf("did not cancel full bundle: %+v %v", report, err)
	}
	storageEventually(t, func() bool {
		child, _ := m.Inspect(l.ID)
		storage, _ := m.Inspect(s.ID)
		ports, _ := m.Inspect(p.ID)
		return child.State == "terminal" && storage.State != "ending" && ports.State != "ending"
	})
	child, _ := m.Inspect(l.ID)
	if child.Outcome != "cancelled" {
		t.Fatalf("lost child cancellation: %+v", child)
	}
	if err := syscall.Kill(foreign.Process.Pid, 0); err != nil {
		t.Fatal("bulk operation signalled an unrelated process", err)
	}
	status, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	storage, _ := m.Inspect(s.ID)
	if storage.State == "needs_attention" {
		if status.Reserved.Disk != s.Request.Resources.Disk {
			t.Fatal("unverified disk was declared free")
		}
		if _, err = m.StorageHandoff(s.ID); err != nil {
			t.Fatal(err)
		}
	} else if storage.State != "terminal" {
		t.Fatalf("unexpected storage result: %+v", storage)
	}
	views, err := m.Pools(false)
	if err != nil || len(views) != 1 || views[0].Reserved != 0 {
		t.Fatalf("custom permits not returned: %+v %v", views, err)
	}
}
