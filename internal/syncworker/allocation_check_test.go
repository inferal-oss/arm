package syncworker

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/syncfs"
	"github.com/inferal-oss/arm/internal/syncreconcile"
)

func TestWorkerAllocationCheckpointAggregatesMarkedWorkspaces(t *testing.T) {
	p := setupPair(t)
	reservation := syncfs.Reservation{
		Key: "allocation-check-123", PlanDigest: hash("reviewed plan"),
		Generation: 1, Side: syncreconcile.Remote, DiskBytes: 8 << 20,
	}
	var workspaces []*syncfs.Workspace
	var largest, total int64
	for range 2 {
		work, err := syncfs.CreateWorkspace(p.remote, p.remoteWork, reservation)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = work.Close() })
		workspaces = append(workspaces, work)
		payload := make([]byte, 512<<10)
		if _, err := rand.Read(payload); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work.StagePath(), "payload"), payload, 0600); err != nil {
			t.Fatal(err)
		}
		observed, err := syncfs.SurveyWorkspace(t.Context(), p.remote, p.remoteWork, work.Path(), reservation)
		if err != nil {
			t.Fatal(err)
		}
		if observed.AllocatedBytes <= 0 {
			t.Fatal("native survey reported no allocation for dense payload")
		}
		total += observed.AllocatedBytes
		largest = max(largest, observed.AllocatedBytes)
	}
	if largest >= total {
		t.Fatal("test workspaces did not produce distinct reported allocation")
	}
	hold := &testHold{disk: total}
	e := &endpoint{Root: p.remote, WorkParent: p.remoteWork, Hold: hold}
	if err := e.checkWorkspaceAllocation(t.Context(), workspaces); err != nil {
		t.Fatalf("exact reported allocation should fit: %v", err)
	}
	hold.disk = largest
	if err := e.checkWorkspaceAllocation(t.Context(), workspaces); err == nil || !strings.Contains(err.Error(), "reported allocation exceeds") {
		t.Fatalf("aggregate overrun was accepted: %v", err)
	}
	for _, work := range workspaces {
		if _, err := os.Stat(filepath.Join(work.StagePath(), "payload")); err != nil {
			t.Fatalf("checkpoint changed retained evidence: %v", err)
		}
	}
}
