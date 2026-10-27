package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/model"
)

func relayRequest(key string) model.Request {
	return model.Request{Key: key, Reason: "bounded opaque relay fixture", Resources: model.Resources{CPU: .25, Memory: 64}, TTL: time.Minute}
}

func acquireTestRelay(t *testing.T, m *Manager, key string) (*RelayTransit, func()) {
	t.Helper()
	joined := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(joined) }) }
	h, err := m.AcquireRelayTransit(t.Context(), relayRequest(key), joined)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		finish()
		if _, err := h.FinishAfterJoin(); err != nil {
			t.Error(err)
		}
	})
	return h, finish
}

func assertRelayCharge(t *testing.T, m *Manager, want bool) {
	t.Helper()
	s, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if want && (s.Reserved.CPU != .25 || s.Reserved.Memory != 64) || !want && (s.Reserved.CPU != 0 || s.Reserved.Memory != 0) {
		t.Fatalf("relay charge=%+v, want consuming=%v", s.Reserved, want)
	}
}

func TestRelayTransitReleaseRequiresOriginalJoin(t *testing.T) {
	m := newManager(t)
	h, finish := acquireTestRelay(t, m, "relay-release")
	l, err := h.Lease()
	if err != nil {
		t.Fatal(err)
	}
	ending, err := m.End(l.ID, "released")
	if err != nil || ending.State != "ending" || ending.Ended != nil {
		t.Fatal(ending, err)
	}
	select {
	case <-h.Context().Done():
	default:
		t.Fatal("release did not cancel owner")
	}
	if _, err = h.FinishAfterJoin(); errorCode(err) != "relay_not_joined" {
		t.Fatal("settled before original join", err)
	}
	assertRelayCharge(t, m, true)
	finish()
	terminal, err := h.FinishAfterJoin()
	if err != nil || terminal.State != "terminal" || terminal.Outcome != "released" || terminal.Ended == nil {
		t.Fatal(terminal, err)
	}
	assertRelayCharge(t, m, false)
	if duplicate, err := h.FinishAfterJoin(); err != nil || duplicate.Revision != terminal.Revision {
		t.Fatal("duplicate acknowledgement changed settlement", duplicate, err)
	}
}

func TestRelayTransitAutomaticClosureDoesNotClaimExecutionCompleted(t *testing.T) {
	m := newManager(t)
	h, finish := acquireTestRelay(t, m, "relay-automatic-close")
	finish()
	<-h.workerDone
	l, err := h.Lease()
	if err != nil || l.State != "terminal" || l.Outcome != "relay_closed" {
		t.Fatal(l, err)
	}
	assertRelayCharge(t, m, false)
}

func TestRelayTransitExpiryAndForceStopKeepBlockedJoinCharged(t *testing.T) {
	for _, mode := range []string{"expiry", "force"} {
		t.Run(mode, func(t *testing.T) {
			m := newManager(t)
			h, finish := acquireTestRelay(t, m, "relay-"+mode)
			l, err := h.Lease()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "expiry" {
				m.mu.Lock()
				expired := time.Now().Add(-time.Second)
				expireLeaseClockForTest(t, &l, expired)
				err = m.put(l, "test.expired_clock")
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				m.tick()
			} else {
				if _, err = m.BeginShutdown(model.ShutdownRequest{Force: true}); err != nil {
					t.Fatal(err)
				}
				r, err := m.AdvanceShutdown()
				if err != nil || r.Phase == "ready" {
					t.Fatal("Stop claimed completion before transport joined", r, err)
				}
			}
			select {
			case <-h.Context().Done():
			default:
				t.Fatal("ending allocation did not cancel owner")
			}
			current, err := h.Lease()
			if err != nil || current.State != "ending" {
				t.Fatal(current, err)
			}
			assertRelayCharge(t, m, true)
			finish()
			if _, err = h.FinishAfterJoin(); err != nil {
				t.Fatal(err)
			}
			assertRelayCharge(t, m, false)
			if mode == "force" {
				r, err := m.AdvanceShutdown()
				if err != nil || r.Phase != "ready" {
					t.Fatal(r, err)
				}
			}
		})
	}
}

func TestRelayTransitExactOwnerAndPublicMarkerDenial(t *testing.T) {
	m := newManager(t)
	q := relayRequest("forged-public")
	q.Kind = relayTransitKind
	if _, err := m.Acquire(q, nil); errorCode(err) != "invalid_request" {
		t.Fatal("wire marker accepted", err)
	}
	h, finish := acquireTestRelay(t, m, "relay-exact")
	l, err := h.Lease()
	if err != nil {
		t.Fatal(err)
	}
	fakeJoined := make(chan struct{})
	close(fakeJoined)
	forged := &RelayTransit{manager: m, id: l.ID, key: l.Request.Key, joined: fakeJoined}
	if _, err = forged.FinishAfterJoin(); errorCode(err) != "relay_owner_changed" {
		t.Fatal("forged owner settled allocation", err)
	}
	if _, err = m.AcquireRelayTransit(t.Context(), relayRequest(l.Request.Key), make(chan struct{})); errorCode(err) != "idempotency_conflict" {
		t.Fatal("adopted existing transit", err)
	}
	if _, err = m.Renew(l.ID, time.Hour); errorCode(err) != "invalid_request" {
		t.Fatal("extended fixed transport authority", err)
	}
	assertRelayCharge(t, m, true)
	finish()
	if _, err = h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
}

func TestRelayTransitAdmissionFailureStartsNoOwner(t *testing.T) {
	m := newManager(t)
	closed := make(chan struct{})
	close(closed)
	if _, err := m.AcquireRelayTransit(t.Context(), relayRequest("closed-join"), closed); errorCode(err) != "invalid_request" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.AcquireRelayTransit(ctx, relayRequest("canceled"), make(chan struct{})); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`CREATE TRIGGER reject_relay BEFORE INSERT ON leases WHEN json_extract(NEW.data,'$.request.kind')='relay-transit' BEGIN SELECT RAISE(ABORT,'retained test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AcquireRelayTransit(t.Context(), relayRequest("failed-ledger"), make(chan struct{})); err == nil {
		t.Fatal("admitted rejected durable insert")
	}
	if len(m.relayTransits) != 0 {
		t.Fatal("failed admission registered transport owner")
	}
	assertRelayCharge(t, m, false)
}

func TestRelayTransitChangedDurableBindingCannotSettle(t *testing.T) {
	m := newManager(t)
	h, finish := acquireTestRelay(t, m, "relay-changed-binding")
	original, err := h.Lease()
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.PID = 2147483647
	m.mu.Lock()
	err = m.put(changed, "test.foreign_pid")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Reconcile(original.ID); errorCode(err) != "needs_attention" {
		t.Fatal("saved PID used as relay closure proof", err)
	}
	finish()
	if _, err = h.FinishAfterJoin(); errorCode(err) != "relay_owner_changed" {
		t.Fatal("changed durable binding was settled", err)
	}
	assertRelayCharge(t, m, true)
	m.mu.Lock()
	err = m.put(original, "test.restore_binding")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
}

func TestRelayTransitJoinPersistenceFailureRetainsCharge(t *testing.T) {
	m := newManager(t)
	h, finish := acquireTestRelay(t, m, "relay-write-failure")
	if _, err := m.db.Exec(`CREATE TRIGGER reject_relay_join BEFORE UPDATE ON leases WHEN json_extract(NEW.data,'$.request.kind')='relay-transit' AND json_extract(NEW.data,'$.state')='terminal' BEGIN SELECT RAISE(ABORT,'retained finalization failure'); END`); err != nil {
		t.Fatal(err)
	}
	finish()
	if _, err := h.FinishAfterJoin(); err == nil {
		t.Fatal("lost finalization acknowledged")
	}
	assertRelayCharge(t, m, true)
	if _, err := m.db.Exec(`DROP TRIGGER reject_relay_join`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	assertRelayCharge(t, m, false)
}

func TestRelayTransitEndingWriteFailureStillCancelsAndRetainsCharge(t *testing.T) {
	m := newManager(t)
	h, finish := acquireTestRelay(t, m, "relay-ending-failure")
	l, err := h.Lease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`CREATE TRIGGER reject_relay_ending BEFORE UPDATE ON leases WHEN json_extract(NEW.data,'$.request.kind')='relay-transit' AND json_extract(NEW.data,'$.state')='ending' BEGIN SELECT RAISE(ABORT,'retained ending failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.End(l.ID, "released"); err == nil {
		t.Fatal("ending write failure was hidden")
	}
	select {
	case <-h.Context().Done():
	default:
		t.Fatal("failed ending write left live owner uncancelled")
	}
	current, err := h.Lease()
	if err != nil || current.State != "active" {
		t.Fatal("failed write did not preserve original charged row", current, err)
	}
	assertRelayCharge(t, m, true)
	if _, err = h.FinishAfterJoin(); errorCode(err) != "relay_not_joined" {
		t.Fatal("failed ending cancelled resources before join", err)
	}
	finish()
	if _, err = h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	assertRelayCharge(t, m, false)
}

func TestRelayTransitCloseWaitsWithoutHoldingManagerMutex(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 1, 1, 0, 1, 0)
	joined := make(chan struct{})
	h, err := m.AcquireRelayTransit(t.Context(), relayRequest("relay-close"), joined)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case <-h.Context().Done():
	case <-time.After(2 * time.Second):
		close(joined)
		t.Fatal("Close did not cancel original owner")
	}
	select {
	case err := <-closed:
		close(joined)
		t.Fatal("Close returned before actual join", err)
	default:
	}
	// Reading here requires Manager.mu; Close must not retain it while waiting.
	l, err := h.Lease()
	if err != nil || l.State != "ending" {
		close(joined)
		t.Fatal(l, err)
	}
	close(joined)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not join transport settlement")
	}
}

func TestRelayTransitCloseCancelsEvenWhenEndingWriteFails(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 1, 1, 0, 1, 0)
	joined := make(chan struct{})
	h, err := m.AcquireRelayTransit(t.Context(), relayRequest("relay-close-write-failure"), joined)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`CREATE TRIGGER reject_relay_close BEFORE UPDATE ON leases WHEN json_extract(NEW.data,'$.request.kind')='relay-transit' AND json_extract(NEW.data,'$.state')='ending' BEGIN SELECT RAISE(ABORT,'retained Close failure'); END`); err != nil {
		close(joined)
		m.Close()
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case <-h.Context().Done():
	case <-time.After(2 * time.Second):
		close(joined)
		t.Fatal("failed Close write did not cancel owner")
	}
	select {
	case err := <-closed:
		close(joined)
		t.Fatal("failed Close write bypassed join", err)
	default:
	}
	assertRelayCharge(t, m, true)
	close(joined)
	select {
	case err := <-closed:
		if err == nil {
			t.Fatal("Close hid ending persistence failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after late join")
	}
	l, err := h.FinishAfterJoin()
	if err != nil || l.State != "terminal" || l.Outcome != "relay_closed" {
		t.Fatal("late positive join was not settled", l, err)
	}
}

func TestRelayTransitRecoveredMarkerRemainsChargedAndBlocksReset(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 1, 1, 0, 1, 0)
	joined := make(chan struct{})
	h, err := m.AcquireRelayTransit(t.Context(), relayRequest("relay-recovery"), joined)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	checkpoint, err := h.Lease()
	if err != nil {
		t.Fatal(err)
	}
	close(joined)
	if _, err = h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	// Install the genuine pre-join durable checkpoint to exercise recovery of
	// a lost owner. This is a recovery-state fixture, not a native crash test.
	m.mu.Lock()
	err = m.put(checkpoint, "test.restore_prejoin_checkpoint")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(home, model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	l, err := reopened.Inspect(checkpoint.ID)
	if err != nil || l.State != "needs_attention" || l.PID != 0 {
		t.Fatal(l, err)
	}
	if _, err = reopened.End(l.ID, "released"); errorCode(err) != "needs_attention" {
		t.Fatal("recovered marker released", err)
	}
	if _, err = reopened.Reconcile(l.ID); errorCode(err) != "needs_attention" {
		t.Fatal("PID0 used as closure proof", err)
	}
	assertRelayCharge(t, reopened, true)
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	source, err := database.Offline(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	proof, err := database.ProveDatabaseSettled(t.Context(), source, l.Owner)
	if err != nil || !proof.Complete || proof.Settled {
		t.Fatal("reset did not retain lost relay duty", proof, err)
	}
}

func TestRelayTransitSettledMarkerAllowsResetProof(t *testing.T) {
	testJoinedTransitResetProof(t, "")
}

func TestForwardTransitSettledMarkerAllowsResetProof(t *testing.T) {
	testJoinedTransitResetProof(t, "forward_closed")
}

func testJoinedTransitResetProof(t *testing.T, outcome string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 1, 1, 0, 1, 0)
	joined := make(chan struct{})
	h, err := m.AcquireRelayTransit(t.Context(), relayRequest("relay-reset-settled"), joined)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	if outcome != "" {
		if _, err = m.End(h.id, outcome); err != nil {
			t.Fatal(err)
		}
	}
	close(joined)
	if _, err = h.FinishAfterJoin(); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	source, err := database.Offline(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	proof, err := database.ProveDatabaseSettled(t.Context(), source, m.owner)
	if err != nil || !proof.Complete || !proof.Settled {
		t.Fatal("joined relay was not understood by reset proof", proof, err)
	}
}
