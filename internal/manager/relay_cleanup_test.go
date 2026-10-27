package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func relayCleanupOwner(t *testing.T, m *Manager, key string) (*RelayTransit, func()) {
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
		select {
		case <-h.workerDone:
		case <-time.After(3 * time.Second):
			t.Error("original relay worker did not return")
		}
	})
	return h, finish
}
func relayCleanupManager(t *testing.T) (*Manager, string, func() error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	var once sync.Once
	var closeErr error
	closeManager := func() error { once.Do(func() { closeErr = m.Close() }); return closeErr }
	// Original owners register their positive join cleanup after this callback.
	t.Cleanup(func() { _ = closeManager() })
	return m, home, closeManager
}
func relayCleanupAwait(t *testing.T, c <-chan struct{}, reason string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(3 * time.Second):
		t.Fatal(reason)
	}
}

type relayCleanupFailedCloser struct{ *os.File }

func (relayCleanupFailedCloser) Close() error { return syscall.EIO }

func TestRelayCleanupUncertainRetainsOpenDescriptorThroughCloseAndRestart(t *testing.T) {
	m, home, closeManager := relayCleanupManager(t)
	h, finish := relayCleanupOwner(t, m, "relay-cleanup-native-failure")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if err = (relayCleanupFailedCloser{writer}).Close(); !errors.Is(err, syscall.EIO) {
		t.Fatal(err)
	}
	const reason = "Original native descriptor close was not positively verified"
	if err = h.MarkCleanupUncertain(reason); err != nil {
		t.Fatal(err)
	}
	if err = h.MarkCleanupUncertain("later reason cannot erase the original failure"); err != nil {
		t.Fatal(err)
	}
	relayCleanupAwait(t, h.Context().Done(), "cleanup uncertainty did not cancel its original owner")
	l, err := h.Lease()
	if err != nil || l.State != "needs_attention" || l.Ended != nil || l.Failure != reason {
		t.Fatal(l, err)
	}
	if _, err = h.FinishAfterJoin(); errorCode(err) != "relay_not_joined" {
		t.Fatal("cleanup error bypassed worker join", err)
	}
	assertRelayCharge(t, m, true)
	closed := make(chan error, 1)
	go func() { closed <- closeManager() }()
	select {
	case err = <-closed:
		t.Fatal("Close returned before positive original worker join", err)
	default:
	}
	// The actual original native descriptor remains usable despite every worker
	// subsequently joining. Worker completion alone must never release its charge.
	if _, err = writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	var actual [1]byte
	if _, err = io.ReadFull(reader, actual[:]); err != nil || actual[0] != 'x' {
		t.Fatal(actual, err)
	}
	finish()
	select {
	case err = <-closed:
		if errorCode(err) != "needs_attention" {
			t.Fatal("Close hid cleanup uncertainty", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close held the manager mutex or waited beyond positive join")
	}
	if _, err = h.FinishAfterJoin(); errorCode(err) != "needs_attention" {
		t.Fatal("joined worker erased native cleanup failure", err)
	}
	// Reopen the actual retained database, rather than manufacturing a terminal
	// record or reconstructing authority from a descriptor.
	reopened, err := Open(home, model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	l, err = reopened.Inspect(h.id)
	if err != nil || l.State != "needs_attention" || l.Ended != nil {
		t.Fatal(l, err)
	}
	assertRelayCharge(t, reopened, true)
	if _, err = reopened.Reconcile(h.id); errorCode(err) != "needs_attention" {
		t.Fatal("restart reconciled failed native close from PID0", err)
	}
	if _, err = reopened.End(h.id, "released"); errorCode(err) != "needs_attention" {
		t.Fatal("restart released failed cleanup", err)
	}
	events, err := reopened.Events(0)
	if err != nil {
		t.Fatal(err)
	}
	marked, retained := false, false
	for _, event := range events {
		if event.Entity != h.id {
			continue
		}
		if event.Type == "relay.cleanup_uncertain" {
			marked = true
		}
		if event.Type == "relay.cleanup_recovery_required" {
			retained = true
		}
		if event.Type == "sync.preparation_recovery_required" {
			t.Fatal("generic cleanup used sync-only event")
		}
	}
	if !marked || !retained {
		t.Fatal("cleanup evidence missing", marked, retained)
	}
}

func TestRelayCleanupUncertainRejectsForgedJoinedAndSettledHandles(t *testing.T) {
	for _, mode := range []string{"forged", "joined", "settled"} {
		t.Run(mode, func(t *testing.T) {
			m := newManager(t)
			h, finish := relayCleanupOwner(t, m, "relay-cleanup-"+mode)
			if mode == "forged" {
				forged := *h
				if err := forged.MarkCleanupUncertain("forged native close"); errorCode(err) != "relay_owner_changed" {
					t.Fatal(err)
				}
				select {
				case <-h.Context().Done():
					t.Fatal("forged handle cancelled the genuine owner")
				default:
				}
				m.mu.Lock()
				reason := h.recoveryReason
				m.mu.Unlock()
				if reason != "" {
					t.Fatal("forged handle poisoned original owner")
				}
				if err := h.MarkCleanupUncertain(" "); errorCode(err) != "invalid_request" {
					t.Fatal(err)
				}
			} else {
				finish()
				if mode == "settled" {
					relayCleanupAwait(t, h.workerDone, "original relay did not settle")
				}
				if err := h.MarkCleanupUncertain("late native close"); errorCode(err) != "relay_owner_changed" {
					t.Fatal(err)
				}
			}
			finish()
			relayCleanupAwait(t, h.workerDone, "original clean relay did not settle")
			assertRelayCharge(t, m, false)
		})
	}
}

func TestRelayCleanupUncertainChangedBindingIsStickyWithoutOverwritingRow(t *testing.T) {
	m := newManager(t)
	h, finish := relayCleanupOwner(t, m, "relay-cleanup-changed-binding")
	original, err := h.Lease()
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.PID = 2147483647
	m.mu.Lock()
	err = m.put(changed, "test.changed_native_binding")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = h.MarkCleanupUncertain("Original native close failed"); errorCode(err) != "relay_owner_changed" {
		t.Fatal(err)
	}
	relayCleanupAwait(t, h.Context().Done(), "changed row kept exact original cleanup workers live")
	stored, err := h.Lease()
	if err != nil || stored.PID != changed.PID || stored.State != changed.State {
		t.Fatal("cleanup marker overwrote changed row", stored, err)
	}
	assertRelayCharge(t, m, true)
	// Restore only this fixture's original durable binding, then prove that
	// the process-local failure remains irreversible even after restoration.
	m.mu.Lock()
	err = m.put(original, "test.restore_binding")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	finish()
	relayCleanupAwait(t, h.workerDone, "uncertain relay workers did not join")
	l, err := h.FinishAfterJoin()
	if errorCode(err) != "needs_attention" || l.State != "needs_attention" || l.Ended != nil {
		t.Fatal(l, err)
	}
	assertRelayCharge(t, m, true)
}

func TestRelayCleanupUncertainWriteFailureCannotBecomeTerminal(t *testing.T) {
	m := newManager(t)
	h, finish := relayCleanupOwner(t, m, "relay-cleanup-write-failure")
	if _, err := m.db.Exec(`CREATE TRIGGER reject_cleanup_attention BEFORE UPDATE ON leases WHEN json_extract(NEW.data,'$.state')='needs_attention' BEGIN SELECT RAISE(ABORT,'owned cleanup persistence refusal'); END`); err != nil {
		t.Fatal(err)
	}
	if err := h.MarkCleanupUncertain("Native cleanup failed before ledger write"); err == nil {
		t.Fatal("cleanup persistence failure hidden")
	}
	relayCleanupAwait(t, h.Context().Done(), "failed cleanup write left original owner live")
	assertRelayCharge(t, m, true)
	finish()
	relayCleanupAwait(t, h.workerDone, "failed persistence prevented positive worker join")
	if _, err := h.FinishAfterJoin(); err == nil {
		t.Fatal("private cleanup failure was cleared by join after failed write")
	}
	l, err := h.Lease()
	if err != nil || l.State != "active" || l.Ended != nil {
		t.Fatal("failed cleanup writes released original row", l, err)
	}
	assertRelayCharge(t, m, true)
	if _, err = m.db.Exec(`DROP TRIGGER reject_cleanup_attention`); err != nil {
		t.Fatal(err)
	}
	l, err = h.FinishAfterJoin()
	if errorCode(err) != "needs_attention" || l.State != "needs_attention" || l.Ended != nil || l.Failure != "Native cleanup failed before ledger write" {
		t.Fatal(l, err)
	}
	assertRelayCharge(t, m, true)
}

func TestRelayCleanupCloseCancelsBeforeUnreadableLedger(t *testing.T) {
	m, home, closeManager := relayCleanupManager(t)
	h, finish := relayCleanupOwner(t, m, "relay-cleanup-read-failure")
	// Removing the ordinary table name makes End fail before its cancellation
	// defer. Close must still cancel its exact registered capability.
	m.mu.Lock()
	_, err := m.db.Exec(`ALTER TABLE leases RENAME TO retained_leases`)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- closeManager() }()
	relayCleanupAwait(t, h.Context().Done(), "Close could not cancel owner after lease read failed")
	if err = h.MarkCleanupUncertain("Original cleanup failed while ledger unreadable"); err == nil {
		t.Fatal("unreadable cleanup ledger accepted marker")
	}
	finish()
	select {
	case err = <-closed:
		if err == nil {
			t.Fatal("Close hid unreadable cleanup ledger")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung after exact workers positively joined")
	}
	// Inspect the genuine retained row after database close. No terminal update
	// could have succeeded under the missing table, and the original charge remains.
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw []byte
	if err = db.QueryRow(`SELECT data FROM retained_leases WHERE id=?`, h.id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var l model.Lease
	if err = json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if !consuming(l.State) || l.Ended != nil || l.Request.Resources.CPU != .25 || l.Request.Resources.Memory != 64 {
		t.Fatal("unreadable ledger cleanup freed original charge", l)
	}
	m.mu.Lock()
	reason := h.recoveryReason
	m.mu.Unlock()
	if reason == "" {
		t.Fatal("failed read cleared private cleanup failure")
	}
}

func TestRelayCleanupUncertainAfterCancellationStillRequiresOriginalJoin(t *testing.T) {
	m := newManager(t)
	h, finish := relayCleanupOwner(t, m, "relay-cleanup-after-cancel")
	if _, err := m.End(h.id, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err := h.MarkCleanupUncertain("Cancelled native cleanup was not verified"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.FinishAfterJoin(); errorCode(err) != "relay_not_joined" {
		t.Fatal(err)
	}
	assertRelayCharge(t, m, true)
	finish()
	relayCleanupAwait(t, h.workerDone, "cancelled original relay workers did not join")
	l, err := h.FinishAfterJoin()
	if errorCode(err) != "needs_attention" || l.Ended != nil {
		t.Fatal(l, err)
	}
	assertRelayCharge(t, m, true)
}
