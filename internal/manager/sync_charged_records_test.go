package manager

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncscope"
)

// The fixture exercises only durable SQL shape and retained original charge.
// Its made-up XFS strings cannot construct a native owner or positive proof.
func TestChargedScopePreparingRowSurvivesRestartAndBlocksReset(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "arm")
	capacity := model.Resources{CPU: 1, Memory: 1 << 30, Disk: 128 << 20}
	clock := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "charged-scope-test", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	m, err := openWithSyncSchema(home, capacity, clock.read, nil, "3")
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 8, 8, 0, 8, 0)
	memoryReading(m, 1<<30, 1<<30, 0)
	diskReading(m, 1<<30, 1<<30, 0)
	key := "charged-scope-test-12345"
	request := model.Request{Host: m.host, Key: mesh.Key(m.host, "sync-preparation\x00local\x00"+key), Reason: "durable scope fixture", TTL: time.Minute,
		Resources: model.Resources{CPU: 1, Memory: 768 << 20, Disk: 1 << 20}}
	scope := &syncScopeReservation{ctx: t.Context(), key: key, side: syncreconcile.Local, host: m.host, owner: m.owner,
		scopeID: "test-only-project", mountID: "test-only-mount", projectID: 42,
		projectRootID: "project", selectedRootID: "selected", homeRootID: "home", ownerEpoch: 1,
		enrollmentDigest: strings.Repeat("a", 64), ownershipDigest: strings.Repeat("b", 64), chargedHard: 1 << 20}
	joined := make(chan struct{})
	wrong := *scope
	wrong.key = "different-original-key-12345"
	if _, err := m.acquireOwnedTransitScope(t.Context(), request, joined, syncPreparationKind, nil, &wrong); err == nil {
		m.Close()
		t.Fatal("different charged row key accepted for original preparation lease")
	}
	owner, err := m.acquireOwnedTransitScope(t.Context(), request, joined, syncPreparationKind, nil, scope)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	joinedClosed, managerClosed := false, false
	var hold *syncscope.Hold
	defer func() {
		if hold != nil {
			hold.Release()
		}
		if !joinedClosed {
			m.mu.Lock()
			owner.recoveryReason = "test-only original join cannot settle native project"
			m.mu.Unlock()
			close(joined)
			<-owner.workerDone
		}
		if !managerClosed {
			_ = m.Close()
		}
	}()
	gate := newSyncScopeGate()
	original, err := syncscope.BindOriginal(t.Context(), m.db, &m.mu, gate.core, owner.ctx, clock.read,
		syncscope.Identity{Key: key, Side: "local", LeaseID: owner.id, Host: m.host, Owner: m.owner})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	owner.chargedOriginal = original
	m.mu.Unlock()
	hold, err = original.TryHold(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := hold.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if verifier, err := gate.tryVerify(t.Context()); err == nil || verifier != nil {
		t.Fatal("second verifier overlapped exact original hold")
	}
	hold.Release()
	hold.Release()
	hold = nil
	if _, err := m.db.Exec(`UPDATE leases SET run=? WHERE id=?`, strings.Repeat("r", 70000), owner.id); err != nil {
		t.Fatal(err)
	}
	if held, err := original.TryHold(t.Context()); err == nil || held != nil {
		if held != nil {
			held.Release()
		}
		t.Fatal("live original adopted a saved run")
	}
	if _, err := m.db.Exec(`UPDATE leases SET run=NULL WHERE id=?`, owner.id); err != nil {
		t.Fatal(err)
	}
	insertSecondChargedScope(t, m, key)
	if held, err := original.TryHold(t.Context()); err == nil || held != nil {
		if held != nil {
			held.Release()
		}
		t.Fatal("live original adopted a second charged scope")
	}
	if _, err := m.db.Exec(`DELETE FROM sync_charged_scopes WHERE key=?`, "charged-reader-second-12345"); err != nil {
		t.Fatal(err)
	}
	assertCurrent := func(label string) {
		t.Helper()
		held, err := original.TryHold(t.Context())
		if held != nil {
			held.Release()
		}
		if err != nil || held == nil {
			t.Fatalf("%s: restored original unavailable: %v", label, err)
		}
	}
	assertCurrent("before lifecycle controls")
	prep := &syncPreparation{owner: owner, chargedOriginal: original, chargedScope: &syncDedicatedScope{}}
	assertUnavailable := func(label string) {
		t.Helper()
		held, err := original.TryHold(t.Context())
		if held != nil {
			held.Release()
		}
		if !errors.Is(err, syncscope.ErrOriginal) || held != nil {
			t.Fatalf("%s: original hold admitted: %v", label, err)
		}
		if _, err := prep.verifyChargedOriginal(t.Context(), key, syncreconcile.Local); errorCode(err) != "sync_preparation_unavailable" {
			t.Fatalf("%s: verification admitted: %v", label, err)
		}
		if _, err := prep.beginChargedEffect(t.Context(), syncfs.CompleteScopeProof{}); errorCode(err) != "sync_preparation_unavailable" {
			t.Fatalf("%s: effect admitted: %v", label, err)
		}
	}
	func() {
		m.mu.Lock()
		prior := m.relayTransits[owner.id]
		m.replaceRelayTransitLocked(&RelayTransit{id: owner.id})
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			m.relayTransits[owner.id] = prior
			m.mu.Unlock()
		}()
		assertUnavailable("replaced original registration")
	}()
	status, err := m.InspectSyncKey(t.Context(), key, syncreconcile.Local)
	if err != nil || status.Lease.ID != owner.id || status.OperationState != "preparing" {
		t.Fatalf("preparing original not inspectable: %+v %v", status, err)
	}
	m.mu.Lock()
	owner.recoveryReason = "test-only original join cannot settle native project"
	m.mu.Unlock()
	close(joined)
	joinedClosed = true
	if _, err := owner.FinishAfterJoin(); err == nil {
		t.Fatal("unresolved original settled")
	}
	err = m.Close()
	managerClosed = true
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openWithLeaseClock(home, capacity, clock.read); err == nil {
		t.Fatal("ordinary schema-2 Open adopted charged schema-3 home")
	}
	reopened, err := openWithSyncSchema(home, capacity, clock.read, nil, "3")
	if err != nil {
		t.Fatal(err)
	}
	status, err = reopened.InspectSyncKey(t.Context(), key, syncreconcile.Local)
	if err != nil || status.Lease.ID != owner.id || status.Lease.State != "needs_attention" {
		t.Fatalf("restarted original charge was lost: %+v %v", status, err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	source, err := database.Offline(t.Context(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	proof, err := database.ProveDatabaseSettled(t.Context(), source, strconv.Itoa(os.Geteuid()))
	if err != nil || proof.Settled || len(proof.Blockers) == 0 {
		t.Fatalf("reset proof lost charged original: %+v %v", proof, err)
	}
}

// Closing publishes revocation while the original context and SQL row are
// still live. This isolates the interval before closeRelayTransits cancels it.
func TestChargedOriginalClosingRevokesBeforeOwnerCancellation(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clock := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "charged-close-test", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	m, err := openWithSyncSchema(filepath.Join(base, "arm"), model.Resources{CPU: 1, Memory: 1 << 30, Disk: 128 << 20}, clock.read, nil, "3")
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 8, 8, 0, 8, 0)
	memoryReading(m, 1<<30, 1<<30, 0)
	diskReading(m, 1<<30, 1<<30, 0)
	key := "charged-close-test-12345"
	request := model.Request{Host: m.host, Key: mesh.Key(m.host, "sync-preparation\x00local\x00"+key), Reason: "close original fixture", TTL: time.Minute,
		Resources: model.Resources{CPU: 1, Memory: 768 << 20, Disk: 1 << 20}}
	scope := &syncScopeReservation{ctx: t.Context(), key: key, side: syncreconcile.Local, host: m.host, owner: m.owner,
		scopeID: "test-only-project", mountID: "test-only-mount", projectID: 42,
		projectRootID: "project", selectedRootID: "selected", homeRootID: "home", ownerEpoch: 1,
		enrollmentDigest: strings.Repeat("a", 64), ownershipDigest: strings.Repeat("b", 64), chargedHard: 1 << 20}
	joined := make(chan struct{})
	owner, err := m.acquireOwnedTransitScope(t.Context(), request, joined, syncPreparationKind, nil, scope)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	defer func() {
		m.mu.Lock()
		owner.recoveryReason = "test-only original join cannot settle native project"
		m.mu.Unlock()
		close(joined)
		<-owner.workerDone
		_ = m.Close()
	}()
	gate := newSyncScopeGate()
	original, err := syncscope.BindOriginal(t.Context(), m.db, &m.mu, gate.core, owner.ctx, clock.read,
		syncscope.Identity{Key: key, Side: "local", LeaseID: owner.id, Host: m.host, Owner: m.owner})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	owner.chargedOriginal = original
	m.mu.Unlock()
	held, err := original.TryHold(t.Context())
	if err != nil || held == nil {
		t.Fatalf("live original unavailable before close: %v", err)
	}
	held.Release()
	m.mu.Lock()
	m.closing = true
	original.Revoke()
	m.mu.Unlock()
	if owner.ctx.Err() != nil {
		t.Fatal("close-stage control canceled original context")
	}
	m.mu.Lock()
	lease, err := m.get(owner.id)
	m.mu.Unlock()
	if err != nil || lease.State != "active" {
		t.Fatalf("close-stage control changed physical lease: %+v %v", lease, err)
	}
	held, err = original.TryHold(t.Context())
	if held != nil {
		held.Release()
	}
	if !errors.Is(err, syncscope.ErrOriginal) || held != nil {
		t.Fatalf("closing original admitted hold: %v", err)
	}
	prep := &syncPreparation{owner: owner, chargedOriginal: original, chargedScope: &syncDedicatedScope{}}
	if _, err := prep.verifyChargedOriginal(t.Context(), key, syncreconcile.Local); errorCode(err) != "sync_preparation_unavailable" {
		t.Fatalf("closing original admitted verification: %v", err)
	}
	if _, err := prep.beginChargedEffect(t.Context(), syncfs.CompleteScopeProof{}); errorCode(err) != "sync_preparation_unavailable" {
		t.Fatalf("closing original admitted effect: %v", err)
	}
}
