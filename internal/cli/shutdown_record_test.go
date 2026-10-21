package cli

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/yrashk/arm/internal/model"
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
