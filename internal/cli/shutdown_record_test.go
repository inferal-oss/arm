package cli

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

func TestFinalShutdownRequiresMatchingDurableReadyRecord(t *testing.T) {
	home := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE metadata (key TEXT PRIMARY KEY,value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	record := model.ShutdownReport{ID: "expected", Phase: "ready", ForceApplied: true, Results: []model.LeaseResult{{ID: "lease", State: "terminal", Outcome: "released"}}}
	write := func() {
		t.Helper()
		b, _ := json.Marshal(record)
		if _, err = db.Exec(`INSERT OR REPLACE INTO metadata VALUES('shutdown',?)`, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	write()
	got, err := readFinalShutdown(home, "expected")
	if err != nil || !got.ForceApplied || len(got.Results) != 1 || got.Results[0].State != "terminal" {
		t.Fatalf("lost final frame: %+v %v", got, err)
	}
	if _, err = readFinalShutdown(home, "other"); err == nil {
		t.Fatal("accepted another shutdown's result")
	}
	record.Phase = "draining"
	write()
	if _, err = readFinalShutdown(home, "expected"); err == nil {
		t.Fatal("external backend exit was reported as completed drain")
	}
	absent := filepath.Join(t.TempDir(), "absent")
	if _, err = readFinalShutdown(absent, "expected"); err == nil {
		t.Fatal("missing state accepted")
	}
	if _, err = os.Stat(absent); !os.IsNotExist(err) {
		t.Fatal("read-only final-state inspection created state")
	}
}

func TestFinalShutdownAfterLostStatusRechecksEndpointAndAuthority(t *testing.T) {
	home := t.TempDir()
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE metadata (key TEXT PRIMARY KEY,value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	record := model.ShutdownReport{ID: "expected", Phase: "ready", Results: []model.LeaseResult{{ID: "lease", State: "terminal"}}}
	b, _ := json.Marshal(record)
	if _, err = db.Exec(`INSERT INTO metadata VALUES('shutdown',?)`, string(b)); err != nil {
		t.Fatal(err)
	}
	ep := local.Endpoint{URL: "ws://127.0.0.1:1234/v1/ws", Token: "fixture", PID: os.Getpid()}
	if err = local.AtomicJSON(filepath.Join(home, "endpoint.json"), ep); err != nil {
		t.Fatal(err)
	}
	// A caller has read this endpoint, but its final status request loses the
	// transport while backend cleanup removes discovery and releases the lock.
	if _, err = local.ReadEndpoint(home); err != nil {
		t.Fatal(err)
	}
	if _, released, err := readReleasedShutdown(home, "expected"); released || err != nil {
		t.Fatalf("accepted completion while authority still held: released=%v err=%v", released, err)
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readReleasedShutdown(home, "expected"); err == nil {
		t.Fatal("stale endpoint accepted as completed shutdown")
	}
	if err = os.Remove(filepath.Join(home, "endpoint.json")); err != nil {
		t.Fatal(err)
	}
	final, released, err := readReleasedShutdown(home, "expected")
	if err != nil || !released || len(final.Results) != 1 || final.Results[0].ID != "lease" {
		t.Fatalf("lost status discarded verified completion: %+v released=%v err=%v", final, released, err)
	}
	if _, _, err = readReleasedShutdown(home, "replacement"); err == nil {
		t.Fatal("accepted a different authority's shutdown result")
	}
}
