package syncservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/syncfs"
	"getarm.org/internal/syncreconcile"
)

func TestWorkspaceLedgerRecordsBeforeStageAndRefusesDuplicateRole(t *testing.T) {
	ctx := context.Background()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	selectedPath, parentPath := filepath.Join(base, "selected"), filepath.Join(base, "private")
	for _, path := range []string{selectedPath, parentPath} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	selected, err := syncfs.OpenRoot(selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	parent, err := syncfs.OpenRoot(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	rootID, err := selected.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	r := request()
	r.Binding.Local.RootID = rootID
	plan, err := syncreconcile.Reconcile(r)
	if err != nil {
		t.Fatal(err)
	}
	intent, rows, err := NewIntent("workspace-ledger-key-123", digest("arm sync-worker fixed"), digest("reviewed SSH control"), "workspace ledger test", syncreconcile.Local, r, plan, promises(), promises(), time.Now().UTC().Truncate(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	reserve(t, db, intent, rows)
	ledger := &Ledger{DB: db, Key: intent.Key, Side: intent.Side}
	reservation := syncfs.Reservation{Key: intent.Key, PlanDigest: intent.PlanDigest, Generation: intent.Generation, Side: intent.Side, DiskBytes: intent.LocalResources.Disk}
	first, err := syncfs.CreateWorkspace(selected, parent, reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := ledger.RecordWorkspace(ctx, "receiver", selected, parent, first); err != nil {
		t.Fatal(err)
	}
	second, err := syncfs.CreateWorkspace(selected, parent, reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := ledger.RecordWorkspace(ctx, "receiver", selected, parent, second); !errors.Is(err, ErrUnknown) {
		t.Fatalf("duplicate receiver role was adopted: %v", err)
	}
	if err := ledger.RecordWorkspace(ctx, "source-local-to-remote", selected, parent, second); err != nil {
		t.Fatal(err)
	}
	got, err := ledger.Workspaces(ctx)
	if err != nil || len(got) != 2 || got[0].Role != "receiver" || got[0].Path != first.Path() || got[1].Role != "source-local-to-remote" || got[1].Path != second.Path() {
		t.Fatalf("retained workspace locators differ: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(second.Path(), ".arm-sync-owner.json")); err != nil {
		t.Fatalf("duplicate-role refusal removed native evidence: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER refuse_workspace_record BEFORE INSERT ON sync_workspaces WHEN NEW.role='source-local-to-local' BEGIN SELECT RAISE(ABORT,'retained workspace write refused'); END`); err != nil {
		t.Fatal(err)
	}
	third, err := syncfs.CreateWorkspace(selected, parent, reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer third.Close()
	if err := ledger.RecordWorkspace(ctx, "source-local-to-local", selected, parent, third); !errors.Is(err, ErrUnknown) {
		t.Fatalf("failed journal write appeared to authorize staging: %v", err)
	}
	if _, err := os.Stat(filepath.Join(third.Path(), ".arm-sync-owner.json")); err != nil {
		t.Fatalf("failed journal write removed native evidence: %v", err)
	}
	if got, err := ledger.Workspaces(ctx); err != nil || len(got) != 2 {
		t.Fatalf("failed journal write changed durable locator set: %+v %v", got, err)
	}
	if _, err := db.Exec(`DROP TRIGGER refuse_workspace_record`); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(third.StagePath(), third.StagePath()+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(third.StagePath(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := ledger.RecordWorkspace(ctx, "source-local-to-local", selected, parent, third); !errors.Is(err, syncfs.ErrChanged) {
		t.Fatalf("changed held stage was recorded: %v", err)
	}
	if got, err := ledger.Workspaces(ctx); err != nil || len(got) != 2 {
		t.Fatalf("changed held stage changed durable locator set: %+v %v", got, err)
	}
	if _, err := db.Exec(`DELETE FROM sync_workspaces WHERE key=? AND side=? AND role='receiver'`, intent.Key, intent.Side); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Workspaces(ctx); !errors.Is(err, ErrUnknown) {
		t.Fatalf("source locator without receiver was accepted: %v", err)
	}
}
