package manager

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/leaseclock"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

// The private schema-3 namespace adds five objects to the already supported
// run-history and Unix-guard catalog. A complete empty namespace must remain
// provable; admitting one extra object must still fail closed.
func TestChargedScopeForwardGuardAndRunProofCompose(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "arm")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := local.Lock(home, "daemon.lock")
	if err != nil {
		t.Fatal(err)
	}
	lockClosed := false
	defer func() {
		if !lockClosed {
			_ = lock.Close()
		}
	}()
	clock := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "composed-catalog", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	m, err := openWithSyncSchemaAndStartupLock(home, model.Resources{CPU: 1, Memory: 128 << 20, Disk: 128 << 20}, clock.read, nil, "3", lock)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = m.Close()
		}
	})
	var charged, guard, runs, total int
	if err := m.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='sync_charged_scopes' OR name GLOB 'sync_charged_scopes_*' OR name GLOB 'sqlite_autoindex_sync_charged_scopes_*'`).Scan(&charged); err != nil {
		t.Fatal(err)
	}
	if err := m.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='forward_unix_pairs' OR name GLOB 'forward_unix_*_guard' OR name GLOB 'sqlite_autoindex_forward_unix_pairs_*'`).Scan(&guard); err != nil {
		t.Fatal(err)
	}
	if err := m.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name IN ('proxy_runs','proxy_run_chunks') OR name GLOB 'sqlite_autoindex_proxy_runs_*' OR name GLOB 'sqlite_autoindex_proxy_run_chunks_*'`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := m.db.QueryRow(`SELECT count(*) FROM sqlite_schema`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if charged != 5 || guard != 7 || runs != 5 || total > 164 || m.syncSchemaVersion != "3" {
		t.Fatalf("complete composed catalog differs: charged=%d guard=%d runs=%d total=%d version=%q", charged, guard, runs, total, m.syncSchemaVersion)
	}
	t.Logf("composed schema census: charged=%d guard=%d runs=%d total=%d", charged, guard, runs, total)
	owner := m.owner
	err = m.Close()
	closed = true
	if err != nil {
		t.Fatal(err)
	}
	err = lock.Close()
	lockClosed = true
	if err != nil {
		t.Fatal(err)
	}
	prove := func() (database.SettledProof, error) {
		t.Helper()
		source, err := database.Offline(t.Context(), home)
		if err != nil {
			return database.SettledProof{}, err
		}
		defer source.Close()
		return database.ProveDatabaseSettled(t.Context(), source, owner)
	}
	if proof, err := prove(); err != nil || !proof.Complete || !proof.Settled {
		t.Fatalf("complete empty composed namespace was not proved: %+v %v", proof, err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE unexpected_composed_catalog (id TEXT)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if proof, err := prove(); err == nil || proof.Settled {
		t.Fatalf("unknown composed schema object was accepted: %+v %v", proof, err)
	}
}
