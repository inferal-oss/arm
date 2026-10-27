package syncworker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncservice"
)

func TestWorkspaceReviewJournalFailurePrecedesStageBytes(t *testing.T) {
	p := setupPair(t)
	write(t, p.local, "f", "private source payload")
	req := requestPair(t, p, syncreconcile.Push, syncreconcile.Additive, nil, nil)
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	res := model.Resources{CPU: 1, Memory: 64 << 20, Disk: 64 << 20}
	i, rows, err := syncservice.NewIntent("workspace-stage-review", FixedCommandSHA256(), hash("reviewed SSH control"), "stage order review", syncreconcile.Local, req, plan, res, res, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	reserveTest(t, p.localDB, i, rows)
	if _, err = p.localDB.Exec(`CREATE TRIGGER refuse_workspace_record BEFORE INSERT ON sync_workspaces BEGIN SELECT RAISE(ABORT,'retain native stage'); END`); err != nil {
		t.Fatal(err)
	}
	e := &endpoint{Side: i.Side, Root: p.local, WorkParent: p.localWork, Names: p.names, Metadata: p.metadata, Ledger: &syncservice.Ledger{DB: p.localDB, Key: i.Key, Side: i.Side}}
	w, dirs, err := e.materialize(t.Context(), plan, batch{Source: syncreconcile.Local, Receiver: syncreconcile.Remote}, res.Disk)
	if w != nil {
		defer w.Close()
	}
	if !errors.Is(err, syncservice.ErrUnknown) || w == nil || len(dirs) != 0 {
		t.Fatalf("journal failure did not retain original empty workspace: %v", err)
	}
	for _, path := range []string{w.StagePath(), w.Quarantine.Path()} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatal("stage bytes preceded durable locator", path, err)
		}
	}
	if contents(t, p.local, "f") != "private source payload" {
		t.Fatal("failed locator changed source")
	}
	if _, err := os.Stat(filepath.Join(w.Path(), ".arm-sync-owner.json")); err != nil {
		t.Fatal("failed locator removed native evidence", err)
	}
	records, err := e.Ledger.Workspaces(t.Context())
	if err != nil || len(records) != 0 {
		t.Fatal("failed locator appeared committed", records, err)
	}
	found, err := syncfs.FindWorkspaces(t.Context(), p.local, p.localWork, w.Reservation)
	if err != nil || len(found) != 1 {
		t.Fatal("failed locator hid native obligation", len(found), err)
	}
	for _, candidate := range found {
		candidate.Close()
	}
	lookup, err := e.Ledger.Lookup(t.Context())
	if err != nil || lookup.State != "reserved" || len(lookup.Effects) != 0 {
		t.Fatal("failed locator advanced operation", lookup.State, err)
	}
}
