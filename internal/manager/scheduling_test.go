package manager

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func deadlineAfter(d time.Duration) *time.Time { at := time.Now().UTC().Add(d); return &at }
func TestSchedulingDeadlineRejectedDurablyAndRetryCanonical(t *testing.T) {
	m := newManager(t)
	r := request("missed")
	r.StartDeadline = deadlineAfter(-time.Second)
	l, err := m.Acquire(r, nil)
	if err != nil || l.State != "terminal" || l.Outcome != "rejected" || l.Started != nil || l.Expires != nil || l.QueueInfo.Rejection.Code != "start_deadline_exceeded" {
		t.Fatalf("%+v %v", l, err)
	}
	stamp := r.StartDeadline.In(time.FixedZone("offset", 3600))
	r.StartDeadline = &stamp
	retry, err := m.Acquire(r, nil)
	if err != nil || retry.ID != l.ID || retry.Revision != l.Revision {
		t.Fatalf("retry %+v %v", retry, err)
	}
	events, err := m.Events(0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type == "lease.rejected" && e.Entity == l.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("missing rejection journal")
	}
}
func TestSchedulingDeadlineNoCommandAfterQueueWait(t *testing.T) {
	m := newManager(t)
	holder, err := m.Acquire(request("hold"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := request("never-execute")
	r.Queue = true
	r.StartDeadline = deadlineAfter(time.Minute)
	output := filepath.Join(t.TempDir(), "executed")
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sh", "-c", "touch \"$1\"", "fixture", output}, CWD: t.TempDir()})
	if err != nil || l.State != "queued" {
		t.Fatalf("%+v %v", l, err)
	}
	wall := r.StartDeadline.Add(time.Second)
	m.scheduleWallNow = func() time.Time { return wall }
	m.tick()
	rejected, _ := m.Inspect(l.ID)
	if rejected.Outcome != "rejected" || rejected.QueueInfo.Rejection.Code != "start_deadline_exceeded" {
		t.Fatalf("%+v", rejected)
	}
	if _, err = m.End(holder.ID, "released"); err != nil {
		t.Fatal(err)
	}
	m.tick()
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("missed request executed", err)
	}
}
func TestSchedulingQueueExplanationFIFOAndGrantTTL(t *testing.T) {
	m := newManager(t)
	holder, _ := m.Acquire(request("holder"), nil)
	r := request("head")
	r.Queue = true
	r.StartDeadline = deadlineAfter(time.Minute)
	head, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	tailReq := request("tail")
	tailReq.Resources.CPU = .25
	tailReq.Queue = true
	tail, err := m.Acquire(tailReq, nil)
	if err != nil {
		t.Fatal(err)
	}
	if head.Started != nil || head.Expires != nil || head.QueueInfo.Position != 1 || head.QueueInfo.Estimate.Kind != "advisory" || head.QueueInfo.Estimate.Start == nil || !head.QueueInfo.Estimate.Start.Equal(*holder.Expires) {
		t.Fatalf("head %+v", head)
	}
	if tail.QueueInfo.Position != 2 || tail.QueueInfo.Estimate.Kind != "unknown" || tail.QueueInfo.Blockers[0].Code != "queue_order" {
		t.Fatalf("tail %+v", tail)
	}
	oldRev := head.Revision
	m.tick()
	unchanged, _ := m.Inspect(head.ID)
	if unchanged.Revision != oldRev {
		t.Fatal("identical explanation journals on every tick")
	}
	m.End(holder.ID, "released")
	m.tick()
	active, _ := m.Inspect(head.ID)
	if active.State != "active" || active.QueueInfo != nil || active.Started == nil || active.Expires.Sub(*active.Started) != r.TTL {
		t.Fatalf("grant %+v", active)
	}
}
func TestSchedulingUnknownInventoryBecomesImpossibleWithoutFIFOBlocking(t *testing.T) {
	m := newManager(t)
	m.capacity.Memory = 0
	m.observationMu.Lock()
	m.memoryAvailability = model.MemoryAvailability{}
	m.memoryObserved = time.Now()
	m.observationMu.Unlock()
	r := request("unknown-memory")
	r.Resources = model.Resources{Memory: 2000}
	r.Queue = true
	head, err := m.Acquire(r, nil)
	if err != nil || head.State != "queued" {
		t.Fatalf("%+v %v", head, err)
	}
	if head.QueueInfo.Estimate.Kind != "unknown" || head.QueueInfo.Blockers[0].Code != "observation_unavailable" {
		t.Fatalf("%+v", head.QueueInfo)
	}
	tailReq := request("known-cpu")
	tailReq.Queue = true
	tail, err := m.Acquire(tailReq, nil)
	if err != nil {
		t.Fatal(err)
	}
	memoryReading(m, 1000, 1000, 0)
	m.tick()
	head, _ = m.Inspect(head.ID)
	tail, _ = m.Inspect(tail.ID)
	if head.Outcome != "rejected" || head.QueueInfo.Rejection.Code != "capacity_exceeded" || tail.State != "active" {
		t.Fatalf("head %+v tail %+v", head, tail)
	}
}
func TestSchedulingDeadlineJournalFailureCannotGrant(t *testing.T) {
	m := newManager(t)
	holder, _ := m.Acquire(request("journal-holder"), nil)
	r := request("journal-wait")
	r.Queue = true
	r.StartDeadline = deadlineAfter(time.Minute)
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.db.Exec(`CREATE TRIGGER reject_deadline BEFORE UPDATE ON leases WHEN NEW.id='` + l.ID + `' AND json_extract(NEW.data,'$.outcome')='rejected' BEGIN SELECT RAISE(ABORT,'fixture rejection journal failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	wall := r.StartDeadline.Add(time.Second)
	m.scheduleWallNow = func() time.Time { return wall }
	m.End(holder.ID, "released")
	m.tick()
	still, _ := m.Inspect(l.ID)
	if still.State != "queued" || still.Started != nil {
		t.Fatalf("failed durable rejection granted %+v", still)
	}
	m.db.Exec(`DROP TRIGGER reject_deadline`)
	m.tick()
	rejected, _ := m.Inspect(l.ID)
	if rejected.Outcome != "rejected" {
		t.Fatalf("%+v", rejected)
	}
}
func TestSchedulingRecoveryDeadlineAndClockRollback(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	capacity := model.Resources{CPU: 1, Disk: 100}
	m, err := Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	holder, _ := m.Acquire(request("recovery-hold"), nil)
	r := request("recovery-wait")
	r.Queue = true
	r.StartDeadline = deadlineAfter(time.Hour)
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash snapshot whose queued deadline passed while offline.
	saved, _ := m.get(l.ID)
	past := time.Now().Add(-time.Second).UTC()
	saved.Request.StartDeadline = &past
	b, _ := json.Marshal(saved)
	_, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(b), l.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = holder
	m.Close()
	m, err = Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	recovered, _ := m.Inspect(l.ID)
	if recovered.Outcome != "rejected" || recovered.QueueInfo.Rejection.Code != "start_deadline_exceeded" {
		t.Fatalf("recovered %+v", recovered)
	}
	r = request("clock-rollback")
	r.Queue = true
	r.StartDeadline = deadlineAfter(time.Hour)
	clock := time.Now().UTC().Add(time.Second)
	m.scheduleHighwater = clock
	m.scheduleWallNow = func() time.Time { return clock.Add(-time.Second) }
	rejected, err := m.Acquire(r, nil)
	if err != nil || rejected.QueueInfo.Rejection.Code != "scheduler_clock_rollback" {
		t.Fatalf("rollback %+v %v", rejected, err)
	}
}
func TestSchedulingRemoteDeadlineExplicitlyUnsupported(t *testing.T) {
	m := newManager(t)
	r := request("remote-deadline")
	r.StartDeadline = deadlineAfter(time.Minute)
	_, err := m.AcquirePeer(strings.Repeat("a", 64), r, nil, model.Resources{CPU: 1}, time.Hour)
	if errorCode(err) != "unsupported_remote_claim" {
		t.Fatal(err)
	}
}

func TestSchedulingGateRechecksDeadlineAfterJournal(t *testing.T) {
	m := newManager(t)
	r := request("gate-deadline")
	r.StartDeadline = deadlineAfter(time.Minute)
	output := filepath.Join(t.TempDir(), "executed")
	now := time.Now().UTC()
	calls := 0
	// Advancing wall time only after lease.started proves that the private shell
	// gate is still closed while the durable process record is committed.
	m.scheduleWallNow = func() time.Time {
		calls++
		var n int
		m.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='lease.started'`).Scan(&n)
		if n > 0 {
			return r.StartDeadline.Add(time.Second)
		}
		return now
	}
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sh", "-c", "touch \"$1\"", "fixture", output}, CWD: t.TempDir()})
	if err != nil || l.Outcome != "rejected" || l.PID != 0 || l.Process != nil || l.Started != nil || len(m.jobs) != 0 {
		t.Fatalf("%+v %v (clock calls %d)", l, err, calls)
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("gate released after missed deadline", err)
	}
}
func TestSchedulingMonotonicDeadlineSurvivesUnchangedWall(t *testing.T) {
	m := newManager(t)
	m.Acquire(request("mono-holder"), nil)
	now := time.Now()
	wall := now.UTC()
	elapsed := now
	m.scheduleWallNow = func() time.Time { return wall }
	m.scheduleElapsedNow = func() time.Time { return elapsed }
	r := request("mono-wait")
	r.Queue = true
	deadline := wall.Add(time.Minute)
	r.StartDeadline = &deadline
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	elapsed = elapsed.Add(2 * time.Minute)
	m.tick()
	l, _ = m.Inspect(l.ID)
	if l.Outcome != "rejected" || l.QueueInfo.Rejection.Code != "start_deadline_exceeded" {
		t.Fatalf("%+v", l)
	}
}
func TestSchedulingGrowableZeroPoolAndNoBypass(t *testing.T) {
	m := newManager(t)
	p := makePool(t, m, "sched-growable", "global", 0)
	r := poolRequest(p, 2)
	r.Queue = true
	head, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	tailReq := request("pool-tail")
	tailReq.Queue = true
	tail, err := m.Acquire(tailReq, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.tick()
	head, _ = m.Inspect(head.ID)
	tail, _ = m.Inspect(tail.ID)
	if head.State != "queued" || head.QueueInfo.Blockers[0].Code != "pool_capacity" || tail.State != "queued" {
		t.Fatalf("head %+v tail %+v", head, tail)
	}
	_, err = m.MutatePool(model.PoolMutation{Operation: "adjust", ID: p.ID, Delta: count(2), Reason: "capacity now available", Key: "grow-sched"})
	if err != nil {
		t.Fatal(err)
	}
	m.tick()
	head, _ = m.Inspect(head.ID)
	tail, _ = m.Inspect(tail.ID)
	if head.State != "active" || tail.State != "active" {
		t.Fatalf("head %+v tail %+v", head, tail)
	}
}

func TestSchedulingRawQueuedGrantRechecksAfterJournal(t *testing.T) {
	m := newManager(t)
	holder, _ := m.Acquire(request("raw-delay-holder"), nil)
	r := request("raw-grant-delay")
	r.Queue = true
	r.StartDeadline = deadlineAfter(time.Minute)
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	wall := time.Now().UTC()
	m.scheduleWallNow = func() time.Time {
		var n int
		m.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='lease.granted' AND entity=?`, l.ID).Scan(&n)
		if n > 0 {
			return r.StartDeadline.Add(time.Second)
		}
		return wall
	}
	m.End(holder.ID, "released")
	m.tick()
	l, _ = m.Inspect(l.ID)
	if l.Outcome != "rejected" || l.Started != nil || l.Expires != nil {
		t.Fatalf("%+v", l)
	}
}
func TestSchedulingRejectionFailureDoesNotPreventActiveExpiry(t *testing.T) {
	m := newManager(t)
	r := request("expires-despite-queue-failure")
	r.TTL = time.Nanosecond
	holder, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	wait := request("queue-reject-failure")
	wait.Queue = true
	wait.StartDeadline = deadlineAfter(time.Minute)
	queued, err := m.Acquire(wait, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.db.Exec(`CREATE TRIGGER reject_queue_failure BEFORE UPDATE ON leases WHEN NEW.id='` + queued.ID + `' AND json_extract(NEW.data,'$.outcome')='rejected' BEGIN SELECT RAISE(ABORT,'fixture failed rejection'); END`)
	if err != nil {
		t.Fatal(err)
	}
	wall := wait.StartDeadline.Add(time.Second)
	m.scheduleWallNow = func() time.Time { return wall }
	m.tick()
	expired, _ := m.Inspect(holder.ID)
	if expired.Outcome != "expired" {
		t.Fatalf("queue blocked unrelated expiry %+v", expired)
	}
}

func TestSchedulingPortsDeadlineClosesOnlyTentativeBindings(t *testing.T) {
	m := newManager(t)
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.Addr().String()
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	r := portRequest("port-slow-deadline")
	r.StartDeadline = deadlineAfter(time.Minute)
	r.Ports[0].Port = port
	now := time.Now().UTC()
	crossed := false
	m.scheduleWallNow = func() time.Time {
		if crossed {
			return r.StartDeadline.Add(time.Second)
		}
		socket, err := net.Listen("tcp4", address)
		if err != nil {
			crossed = true
			return r.StartDeadline.Add(time.Second)
		}
		socket.Close()
		return now
	}
	l, err := m.Acquire(r, nil)
	if err != nil || l.Outcome != "rejected" || len(l.Ports) != 0 || len(m.portSockets) != 0 {
		t.Fatalf("%+v %v", l, err)
	}
	probe, err = net.Listen("tcp4", address)
	if err != nil {
		t.Fatal("tentative binding leaked", err)
	}
	probe.Close()
}
func TestSchedulingStorageDeadlineUsesOwnedCompensation(t *testing.T) {
	m := newManager(t)
	r := request("storage-slow-deadline")
	r.Kind = "storage"
	r.Resources = model.Resources{Disk: 100}
	r.StartDeadline = deadlineAfter(time.Minute)
	now := time.Now().UTC()
	crossed := false
	m.scheduleWallNow = func() time.Time {
		entries, _ := os.ReadDir(filepath.Join(m.home, "storage"))
		if len(entries) > 0 {
			crossed = true
		}
		if crossed {
			return r.StartDeadline.Add(time.Second)
		}
		return now
	}
	l, err := m.Acquire(r, nil)
	if err != nil || l.Outcome != "rejected" || l.State != "ending" || l.Artifact == nil || l.Artifact.Identity == nil || l.QueueInfo.Rejection.Code != "start_deadline_exceeded" {
		t.Fatalf("%+v %v", l, err)
	}
	eventually(t, func() bool {
		current, _ := m.Inspect(l.ID)
		return current.State == "terminal" || current.State == "needs_attention"
	})
	l, _ = m.Inspect(l.ID)
	if l.Outcome != "rejected" {
		t.Fatalf("compensation lost rejected outcome %+v", l)
	}
}

func TestSchedulingCreationClockAndLeaseCommitTogether(t *testing.T) {
	m := newManager(t)
	_, err := m.db.Exec(`CREATE TRIGGER reject_clock BEFORE INSERT ON metadata WHEN NEW.key='scheduler_clock' BEGIN SELECT RAISE(ABORT,'fixture clock journal failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	r := request("atomic-clock")
	r.StartDeadline = deadlineAfter(time.Minute)
	if _, err = m.Acquire(r, nil); err == nil {
		t.Fatal("lease granted without durable clock checkpoint")
	}
	var count int
	m.db.QueryRow(`SELECT COUNT(*) FROM leases`).Scan(&count)
	if count != 0 {
		t.Fatal("lease escaped failed clock transaction")
	}
	m.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='lease.created'`).Scan(&count)
	if count != 0 {
		t.Fatal("lease event escaped failed clock transaction")
	}
	m.db.Exec(`DROP TRIGGER reject_clock`)
	l, err := m.Acquire(r, nil)
	if err != nil || l.State != "active" {
		t.Fatalf("retry %+v %v", l, err)
	}
}
