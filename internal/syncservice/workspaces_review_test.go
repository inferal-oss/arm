package syncservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/syncfs"
	"github.com/inferal-oss/arm/internal/syncreconcile"
)

func reviewWorkspaceLedger(t *testing.T) (*Ledger, *syncfs.Root, *syncfs.Root, *syncfs.Workspace) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	open := func(name string) *syncfs.Root {
		p := filepath.Join(base, name)
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
		r, err := syncfs.OpenRoot(p)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { r.Close() })
		return r
	}
	selected, parent := open("selected"), open("private")
	id, err := selected.NativeID()
	if err != nil {
		t.Fatal(err)
	}
	req := request()
	req.Binding.Local.RootID = id
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	i, rows, err := NewIntent("workspace-review-key-123", digest("worker"), digest("control"), "workspace review", syncreconcile.Local, req, plan, promises(), promises(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	db := openDB(t)
	reserve(t, db, i, rows)
	l := &Ledger{DB: db, Key: i.Key, Side: i.Side}
	w, err := syncfs.CreateWorkspace(selected, parent, syncfs.Reservation{Key: i.Key, Side: i.Side, PlanDigest: i.PlanDigest, Generation: i.Generation, DiskBytes: i.LocalResources.Disk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return l, selected, parent, w
}

func TestWorkspaceReviewLocatorByteBounds(t *testing.T) {
	for _, kind := range []string{"oversize-path", "invalid-utf8-path", "nul-path", "oversize-root"} {
		t.Run(kind, func(t *testing.T) {
			l, selected, parent, w := reviewWorkspaceLedger(t)
			if err := l.RecordWorkspace(t.Context(), "receiver", selected, parent, w); err != nil {
				t.Fatal(err)
			}
			path := w.Path()
			switch kind {
			case "oversize-path":
				path = "/" + strings.Repeat("a", syncreconcile.MaxPathBytes) + "/" + filepath.Base(path)
			case "invalid-utf8-path":
				path = "/invalid-\xff/" + filepath.Base(path)
			case "nul-path":
				path = "/invalid-\x00/" + filepath.Base(path)
			case "oversize-root":
				if _, err := l.DB.Exec(`UPDATE sync_workspaces SET root_id=? WHERE key=?`, "arm-sync-root-v2:"+strings.Repeat("a", 1<<20), l.Key); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "oversize-root" {
				if _, err := l.DB.Exec(`UPDATE sync_workspaces SET path=? WHERE key=?`, path, l.Key); err != nil {
					t.Fatal(err)
				}
			}
			if records, err := l.Workspaces(t.Context()); records != nil || !errors.Is(err, ErrUnknown) {
				t.Fatalf("invalid native locator was accepted: %d %v", len(records), err)
			}
			if _, err := os.Stat(filepath.Join(w.Path(), ".arm-sync-owner.json")); err != nil {
				t.Fatal("refusal changed marked native evidence", err)
			}
		})
	}
}

func TestWorkspaceReviewCancelledRecordRetainsOriginalNativeEvidence(t *testing.T) {
	l, selected, parent, w := reviewWorkspaceLedger(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := l.RecordWorkspace(ctx, "receiver", selected, parent, w); err == nil {
		t.Fatal("cancelled record authorized staging")
	}
	records, err := l.Workspaces(t.Context())
	if err != nil || len(records) != 0 {
		t.Fatal("cancelled record changed locator set", records, err)
	}
	lookup, err := l.Lookup(t.Context())
	if err != nil || lookup.State != "reserved" || len(lookup.Effects) != 0 {
		t.Fatal("cancelled record changed original operation", lookup.State, err)
	}
	found, err := syncfs.FindWorkspaces(t.Context(), selected, parent, w.Reservation)
	if err != nil || len(found) != 1 {
		t.Fatal("unrecorded physical obligation lost", len(found), err)
	}
	for _, r := range found {
		r.Close()
	}
	for _, path := range []string{w.StagePath(), w.Quarantine.Path()} {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatal("cancelled record changed private staging", path, err)
		}
	}
}
