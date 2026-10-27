package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncservice"
	"golang.org/x/sys/unix"
)

func TestSyncPreparationAdmissionPrecedesNativeScan(t *testing.T) {
	for _, denial := range []string{"cpu", "memory", "disk", "draining"} {
		t.Run(denial, func(t *testing.T) {
			m, root, parent := syncManagerFixture(t)
			i, rows, control := sourceSyncIntent(t, m, root)
			// Scan would fail on this real special file if it were reached.
			if err := unix.Mkfifo(filepath.Join(root.Path(), "must-not-scan"), 0600); err != nil {
				t.Fatal(err)
			}
			switch denial {
			case "cpu":
				cpuReading(m, 8, 8, 8, 0, 0)
			case "memory":
				memoryReading(m, 1<<30, 0, 0)
			case "disk":
				diskReading(m, 1<<30, 0, 0)
			case "draining":
				m.mu.Lock()
				m.draining = true
				m.mu.Unlock()
			}
			_, err := m.ReserveSyncSource(context.Background(), i, rows, root, parent, control)
			var problem *model.Error
			if !errors.As(err, &problem) || problem.Code != "insufficient_capacity" && problem.Code != "backend_stopping" {
				t.Fatalf("unadmitted native scan reached: %v", err)
			}
			var count int
			if err := m.db.QueryRow(`SELECT COUNT(*) FROM leases`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("denied preparation left leases: %d %v", count, err)
			}
			matches, err := filepath.Glob(filepath.Join(m.home, ".ARMcase*"))
			if err != nil || len(matches) != 0 {
				t.Fatal("denied preparation touched scratch", matches, err)
			}
		})
	}
}

func TestSyncPreparationEndCancelsButCannotReleaseBeforeOriginalJoin(t *testing.T) {
	m, _, _ := syncManagerFixture(t)
	prep, err := m.beginSyncPreparation(t.Context(), "preparation-key-12345", syncreconcile.Local, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prep.finish()
	l, err := prep.owner.Lease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Acquire(model.Request{Kind: syncPreparationKind, Resources: syncPreparationResources, Reason: "forged", TTL: time.Minute}, nil); err == nil {
		t.Fatal("wire kind forged preparation authority")
	}
	ending, err := m.End(l.ID, "cancelled")
	if err != nil || ending.State != "ending" {
		t.Fatal(ending, err)
	}
	if prep.owner.Context().Err() == nil {
		t.Fatal("End did not cancel exact preparation owner")
	}
	if _, err = prep.owner.FinishAfterJoin(); err == nil {
		t.Fatal("cancellation released unjoined preparation")
	}
	saved, err := m.Inspect(l.ID)
	if err != nil || !consuming(saved.State) {
		t.Fatal("lost charged preparation", saved, err)
	}
}

func TestSyncPreparationUnknownScratchRetainsAfterJoin(t *testing.T) {
	m, _, _ := syncManagerFixture(t)
	prep, err := m.beginSyncPreparation(t.Context(), "uncertain-scratch-12345", syncreconcile.Local, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	prep.scratchUncertain()
	if err = prep.finish(); err == nil {
		t.Fatal("unknown scratch cleanup released")
	}
	l, err := prep.owner.Lease()
	if err != nil || l.State != "needs_attention" || l.Ended != nil {
		t.Fatal(l, err)
	}
	if _, err = m.Reconcile(l.ID); err == nil {
		t.Fatal("generic reconciliation released scratch obligation")
	}
}

func TestSyncPreparationCloseWaitsForOriginalNativeJoin(t *testing.T) {
	m, _, _ := syncManagerFixture(t)
	prep, err := m.beginSyncPreparation(t.Context(), "close-preparation-12345", syncreconcile.Local, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- m.Close() }()
	select {
	case <-prep.owner.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel owner")
	}
	select {
	case err := <-finished:
		t.Fatal("Close returned before native join", err)
	default:
	}
	if err = prep.finish(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close failed to join")
	}
}

func TestSyncPreparationNativeVolumeBoundAndScratchCleanFailureReleases(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	volume, err := m.syncVolume(root, parent)
	if err != nil || !strings.Contains(volume, "/fsid:") || !strings.Contains(volume, "/pool:") {
		t.Fatal(volume, err)
	}
	// Native traversal fails after the scratch probe has positively cleaned.
	if err = os.WriteFile(filepath.Join(root.Path(), "changed"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ReserveSyncSource(t.Context(), i, rows, root, parent, control); err == nil {
		t.Fatal("changed observation admitted")
	}
	m.mu.Lock()
	ls, err := m.list()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(ls) != 1 || ls[0].Request.Kind != syncPreparationKind || ls[0].State != "terminal" {
		t.Fatal("known returned preparation retained", ls)
	}
}

func TestSyncPreparationSampledVolumeMismatchRefuses(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	m.observationMu.Lock()
	m.diskAvailability.Volume = "fsid:unrelated"
	m.observationMu.Unlock()
	if _, err := m.syncVolume(root, parent); err == nil {
		t.Fatal("unrelated sampled volume used for native roots")
	}
}

func TestSyncOutgoingStageFloorCoversEitherSource(t *testing.T) {
	for _, side := range []syncreconcile.Side{syncreconcile.Local, syncreconcile.Remote} {
		receiver := syncreconcile.Local
		if side == syncreconcile.Local {
			receiver = syncreconcile.Remote
		}
		i := syncservice.Intent{Side: side, Actions: []syncreconcile.Action{{Side: receiver, Source: side, After: &syncreconcile.Entry{Kind: syncreconcile.File, Size: 4 << 20}}}}
		resources := model.Resources{CPU: .25, Memory: 2 << 20, Disk: 2 << 20}
		if err := syncPhysicalPromise(i, nil, resources); err == nil {
			t.Fatalf("%s outgoing source stage undercharged", side)
		}
		resources.Disk = 16 << 20
		if err := syncPhysicalPromise(i, nil, resources); err != nil {
			t.Fatalf("adequate %s stage promise refused: %v", side, err)
		}
	}
}

func TestSyncPreparationRestartDoesNotInferJoinFromPIDZero(t *testing.T) {
	m, _, _ := syncManagerFixture(t)
	prep, err := m.beginSyncPreparation(t.Context(), "restart-preparation-12345", syncreconcile.Local, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = prep.finish(); err != nil {
		t.Fatal(err)
	}
	l, err := prep.owner.Lease()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the durable active row left by a vanished in-process worker.
	l.State, l.Ended, l.Outcome = "active", nil, ""
	m.mu.Lock()
	err = m.put(l, "test.unjoined_preparation")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(m.home, m.capacity)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	recovered, err := next.Inspect(l.ID)
	if err != nil || recovered.State != "needs_attention" || recovered.Outcome != "sync_preparation_supervision_lost" {
		t.Fatal(recovered, err)
	}
	if _, err = next.Reconcile(l.ID); err == nil {
		t.Fatal("PID-zero was accepted as original join evidence")
	}
}

func TestSyncPreparationResetProofDistinguishesJoinedAndRetained(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(strconv.FormatBool(uncertain), func(t *testing.T) {
			m, _, _ := syncManagerFixture(t)
			prep, err := m.beginSyncPreparation(t.Context(), "reset-preparation-12345", syncreconcile.Local, time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			if uncertain {
				prep.scratchUncertain()
			}
			err = prep.finish()
			if (err != nil) != uncertain {
				t.Fatal(err)
			}
			if err = m.Close(); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(m.home, "daemon.lock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			source, err := database.Offline(t.Context(), m.home)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			proof, err := database.ProveDatabaseSettled(t.Context(), source, strconv.Itoa(os.Geteuid()))
			if err != nil || proof.Settled == uncertain {
				t.Fatalf("joined=%t settled=%t: %v", !uncertain, proof.Settled, err)
			}
		})
	}
}

func TestSyncPreparationAutomaticUnknownScratchCannotBecomeSuccess(t *testing.T) {
	m, _, _ := syncManagerFixture(t)
	prep, err := m.beginSyncPreparation(t.Context(), "auto-uncertain-prep-12345", syncreconcile.Local, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	prep.scratchUncertain()
	prep.cancel()
	close(prep.joined)
	select {
	case <-prep.owner.workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("automatic settlement did not return")
	}
	l, err := prep.owner.FinishAfterJoin()
	if err == nil || l.State != "needs_attention" {
		t.Fatal("automatic settlement masked retained cleanup", l, err)
	}
}

func TestSyncPreparationPeerAggregateCeilingAndExpiry(t *testing.T) {
	m, _, _ := syncManagerFixture(t)
	grant := &syncPreparationGrant{source: strings.Repeat("a", 32), certificate: strings.Repeat("b", 64), revision: 1, limit: syncPreparationResources, validUntil: time.Now().Add(time.Minute)}
	first, err := m.beginSyncPreparation(t.Context(), "peer-preparation-12345", syncreconcile.Remote, time.Minute, grant)
	if err != nil {
		t.Fatal(err)
	}
	defer first.finish()
	if _, err = m.beginSyncPreparation(t.Context(), "peer-preparation-23456", syncreconcile.Remote, time.Minute, grant); err == nil || !strings.Contains(err.Error(), "aggregate peer ceiling") {
		t.Fatal("peer preparation borrowed additional authority", err)
	}
	expired := *grant
	expired.validUntil = time.Now().Add(-time.Second)
	if _, err = m.beginSyncPreparation(t.Context(), "peer-preparation-34567", syncreconcile.Remote, time.Minute, &expired); err == nil {
		t.Fatal("expired peer preparation admitted")
	}
}

func TestSyncPreparationHandoffRollbackRetainsOriginalCharge(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	prep, err := m.beginSyncPreparation(t.Context(), i.Key, i.Side, control.TTL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prep.finish()
	admission, err := m.prepareSyncAdmission(prep.owner.Context(), i, rows, root, parent, prep)
	if err != nil {
		t.Fatal(err)
	}
	prep.nativeFinished()
	// A real SQLite abort occurs after attempted final admission writes. The
	// transaction must restore the still-owned preparation charge in full.
	if _, err = m.db.Exec(`CREATE TRIGGER fail_preparation_handoff BEFORE UPDATE ON leases WHEN OLD.id='` + prep.owner.id + `' BEGIN SELECT RAISE(ABORT,'retained handoff failure'); END`); err != nil {
		t.Fatal(err)
	}
	defer m.db.Exec(`DROP TRIGGER fail_preparation_handoff`)
	r := model.Request{Host: m.host, Key: mesh.Key(m.host, "sync-source\x00"+i.Key), Reason: i.Reason, Kind: "execution", TTL: control.TTL, Resources: i.LocalResources, Peer: control.Target, OriginKey: i.Key}
	m.mu.Lock()
	_, err = m.acquireWithAdmission(r, nil, false, admission)
	m.mu.Unlock()
	if err == nil {
		t.Fatal("failed handoff admitted")
	}
	l, err := prep.owner.Lease()
	if err != nil || l.State != "active" || l.Revision != prep.owner.result.Revision {
		t.Fatal("rollback lost original charge", l, err)
	}
	var operations, bindings, leases int
	if err = m.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sync_operations),(SELECT COUNT(*) FROM sync_lease_bindings),(SELECT COUNT(*) FROM leases)`).Scan(&operations, &bindings, &leases); err != nil || operations != 0 || bindings != 0 || leases != 1 {
		t.Fatal("partial handoff survived rollback", operations, bindings, leases, err)
	}
}

func TestSyncAdmissionSharesExactVersionedWorkerCommand(t *testing.T) {
	if syncWorkerCommand != "arm sync-worker --protocol=1" {
		t.Fatal("unexpected worker command version")
	}
	if syncWorkerCommand != syncservice.FixedWorkerCommand {
		t.Fatal("Manager and protocol command bytes differ")
	}

	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	control.CommandSHA256 = sshDigest([]byte("arm sync-worker"))
	digest, err := control.Digest()
	if err != nil {
		t.Fatal(err)
	}
	i.WorkerCommandSHA256, i.SSHControlSHA256 = control.CommandSHA256, digest
	if _, err = m.ReserveSyncSource(t.Context(), i, rows, root, parent, control); err == nil {
		t.Fatal("unversioned worker admitted")
	}
	var count int
	if err = m.db.QueryRow(`SELECT COUNT(*) FROM leases`).Scan(&count); err != nil || count != 0 {
		t.Fatal("wrong command reached preparation", count, err)
	}
}

func TestSyncPreparationElapsedExpiryRefusesCreditAndHandoff(t *testing.T) {
	m, _, _ := syncManagerFixture(t)
	c := &controlledLeaseClock{sample: m.leaseClockLast}
	m.leaseClockRead = c.read
	prep, err := m.beginSyncPreparation(t.Context(), "native-preparation-fence", syncreconcile.Local, time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prep.finish()
	prep.nativeFinished()
	admission := &syncAdmission{manager: m, preparation: prep, ctx: t.Context()}
	original := prep.owner.result
	c.sample.Tick = original.Clock.End
	if prep.owner.Context().Err() != nil {
		t.Fatal("fixture context should remain live")
	}
	if _, err = admission.withoutPreparation([]model.Lease{original}); err == nil {
		t.Fatal("expired preparation lent admission credit")
	}
	tx, err := m.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = admission.handoffTx(tx, original)
	_ = tx.Rollback()
	if err == nil {
		t.Fatal("expired preparation transferred charge")
	}
	current, err := prep.owner.Lease()
	if err != nil || current.State != "active" || current.Revision != original.Revision {
		t.Fatal("refused handoff changed charge", current, err)
	}
}
