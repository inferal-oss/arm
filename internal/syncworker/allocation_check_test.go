package syncworker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/syncfs"
	"github.com/inferal-oss/arm/internal/syncreconcile"
	"github.com/inferal-oss/arm/internal/syncservice"
)

func allocationReviewEndpoint(t *testing.T) (*endpoint, syncreconcile.Plan) {
	t.Helper()
	p := setupPair(t)
	write(t, p.local, "file", "reviewed payload")
	req := requestPair(t, p, syncreconcile.Push, syncreconcile.Additive, nil, nil)
	plan, err := syncreconcile.Reconcile(req)
	if err != nil {
		t.Fatal(err)
	}
	res := model.Resources{CPU: 1, Memory: 64 << 20, Disk: 8 << 20}
	intent, rows, err := syncservice.NewIntent("allocation-review-original-key", FixedCommandSHA256(), hash("reviewed SSH control"), "allocation checkpoint fixture", syncreconcile.Local, req, plan, res, res, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	reserveTest(t, p.localDB, intent, rows)
	a, b := net.Pipe()
	t.Cleanup(func() { a.Close(); b.Close() })
	session, err := newSingleStreamSession(a, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	hold := &testHold{intent: intent, root: req.Binding.Local.RootID, disk: res.Disk}
	e := &endpoint{Side: syncreconcile.Local, Root: p.local, WorkParent: p.localWork, Names: p.names, Metadata: p.metadata, Ledger: &syncservice.Ledger{DB: p.localDB, Key: intent.Key, Side: syncreconcile.Local}, Hold: hold, Session: session}
	if err := e.check(t.Context(), intent, plan); err != nil {
		t.Fatal(err)
	}
	need, err := forecast(intent, e.Side)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.bindAllocation(t.Context(), intent, need); err != nil {
		t.Fatal(err)
	}
	return e, plan
}
func allocationReviewWorkspace(t *testing.T, e *endpoint, plan syncreconcile.Plan, role string) *syncfs.Workspace {
	t.Helper()
	w, err := e.workspace(t.Context(), plan, e.allocation.reservation, role)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}
func TestWorkerAllocationCheckpointAggregatesMarkedWorkspaces(t *testing.T) {
	e, plan := allocationReviewEndpoint(t)
	first := allocationReviewWorkspace(t, e, plan, "receiver")
	second := allocationReviewWorkspace(t, e, plan, "source-local-to-remote")
	work := []*syncfs.Workspace{first, second}
	if err := e.checkWorkspaceAllocation(t.Context(), work); err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 5<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	for _, w := range work {
		if err := os.WriteFile(filepath.Join(w.StagePath(), "payload"), payload, 0600); err != nil {
			t.Fatal(err)
		}
		observed, err := syncfs.SurveyWorkspace(t.Context(), e.Root, e.WorkParent, w.Path(), w.Reservation)
		if err != nil || observed.AllocatedBytes >= e.Hold.DiskBytes() {
			t.Fatal("individual workspace did not fit", err)
		}
	}
	if err := e.checkWorkspaceAllocation(t.Context(), work); err == nil || !strings.Contains(err.Error(), "reported allocation exceeds") {
		t.Fatalf("aggregate overrun accepted: %v", err)
	}
	for _, w := range work {
		raw, err := os.ReadFile(filepath.Join(w.StagePath(), "payload"))
		if err != nil || string(raw) != string(payload) {
			t.Fatal("retained stage changed", err)
		}
	}
	rows, err := e.Ledger.Workspaces(t.Context())
	if err != nil || len(rows) != 2 {
		t.Fatal("durable obligation lost", err)
	}
}
func TestWorkerAllocationCheckpointRejectsChangedOriginalBindings(t *testing.T) {
	for _, kind := range []string{"empty", "duplicate", "omitted", "reservation", "ledger", "session", "locator", "operation", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			e, plan := allocationReviewEndpoint(t)
			w := allocationReviewWorkspace(t, e, plan, "receiver")
			work := []*syncfs.Workspace{w}
			ctx := t.Context()
			switch kind {
			case "empty":
				work = nil
			case "duplicate":
				work = append(work, w)
			case "omitted":
				_ = allocationReviewWorkspace(t, e, plan, "source-local-to-remote")
			case "reservation":
				w.Reservation.Generation++
			case "ledger":
				e.Ledger = &syncservice.Ledger{DB: e.Ledger.DB, Key: e.Ledger.Key, Side: e.Ledger.Side}
			case "session":
				a, b := net.Pipe()
				defer a.Close()
				defer b.Close()
				e.Session, _ = newSingleStreamSession(a, func() error { return nil })
			case "locator":
				if _, err := e.Ledger.DB.Exec(`UPDATE sync_workspaces SET disk_bytes=disk_bytes+1`); err != nil {
					t.Fatal(err)
				}
			case "operation":
				if _, err := e.Ledger.DB.Exec(`UPDATE sync_operations SET record=CAST(record AS TEXT)||' '`); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := e.checkWorkspaceAllocation(ctx, work); err == nil {
				t.Fatal("changed original checkpoint accepted")
			}
			if _, err := os.Stat(w.Path()); err != nil {
				t.Fatal("checkpoint removed retained workspace", err)
			}
		})
	}
}
func TestWorkerAllocationCheckpointRejectsValidNamedReplacement(t *testing.T) {
	e, plan := allocationReviewEndpoint(t)
	w := allocationReviewWorkspace(t, e, plan, "receiver")
	retained := w.Path() + "-retained"
	if err := os.WriteFile(filepath.Join(w.StagePath(), "retained"), []byte("original charged evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(w.Path(), retained); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{w.Path(), filepath.Join(w.Path(), "stage"), filepath.Join(w.Path(), "quarantine")} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	base, err := syncfs.OpenRoot(w.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	stage, err := syncfs.OpenRoot(filepath.Join(w.Path(), "stage"))
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	quarantine, err := syncfs.OpenRoot(filepath.Join(w.Path(), "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	defer quarantine.Close()
	id := func(root *syncfs.Root) string {
		v, err := root.NativeID()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	marker := struct {
		Version        int                `json:"version"`
		Reservation    syncfs.Reservation `json:"reservation"`
		SelectedRootID string             `json:"selected_root_id"`
		ParentRootID   string             `json:"parent_root_id"`
		BaseRootID     string             `json:"base_root_id"`
		StageRootID    string             `json:"stage_root_id"`
		QuarantineID   string             `json:"quarantine_root_id"`
	}{1, w.Reservation, id(e.Root), id(e.WorkParent), id(base), id(stage), id(quarantine)}
	raw, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path(), ".arm-sync-owner.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := syncfs.SurveyWorkspace(t.Context(), e.Root, e.WorkParent, w.Path(), w.Reservation); err != nil {
		t.Fatal("replacement should be independently valid named evidence", err)
	}
	if err := e.checkWorkspaceAllocation(t.Context(), []*syncfs.Workspace{w}); !errors.Is(err, syncfs.ErrChanged) {
		t.Fatal("adopted valid successor over original held binding", err)
	}
	original, err := os.ReadFile(filepath.Join(retained, "stage", "retained"))
	if err != nil || string(original) != "original charged evidence" {
		t.Fatal("original evidence changed", err)
	}
	replacement, err := os.ReadFile(filepath.Join(w.Path(), ".arm-sync-owner.json"))
	if err != nil || string(replacement) != string(raw) {
		t.Fatal("replacement evidence changed", err)
	}
}
