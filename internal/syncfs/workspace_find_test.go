package syncfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceFindRequiresOneExactMarkedCandidate(t *testing.T) {
	_, selected, parent, original, reservation := testWorkspaceEvidence(t)
	other := reservation
	other.Key = "other-workspace-key-123"
	unrelated, err := CreateWorkspace(selected, parent, other)
	if err != nil {
		t.Fatal(err)
	}
	defer unrelated.Close()
	got, err := FindWorkspace(context.Background(), selected, parent, reservation)
	if err != nil {
		t.Fatal(err)
	}
	if got.Path() != original.Path() || got.StagePath() != original.StagePath() {
		t.Fatal("search returned a different workspace")
	}
	if err := got.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := FindWorkspace(context.Background(), selected, parent, Reservation{Key: "absent-workspace-key-123", PlanDigest: reservation.PlanDigest, Generation: reservation.Generation, Side: reservation.Side, DiskBytes: reservation.DiskBytes}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("absent workspace was treated as known: %v", err)
	}
}

func TestWorkspaceFindRetainsAmbiguousAndIncompleteEvidence(t *testing.T) {
	_, selected, parent, original, reservation := testWorkspaceEvidence(t)
	duplicate, err := CreateWorkspace(selected, parent, reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer duplicate.Close()
	all, err := FindWorkspaces(context.Background(), selected, parent, reservation)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, workspace := range all {
		paths[workspace.Path()] = true
		if err := workspace.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if len(all) != 2 || !paths[original.Path()] || !paths[duplicate.Path()] {
		t.Fatalf("multi-workspace recovery omitted a matching private tree: %v", paths)
	}
	if _, err := FindWorkspace(context.Background(), selected, parent, reservation); !errors.Is(err, ErrUnknown) {
		t.Fatalf("duplicate reservation was treated as unique: %v", err)
	}
	if err := os.Remove(filepath.Join(duplicate.Path(), workspaceMarkerName)); err != nil {
		t.Fatal(err)
	}
	if _, err := FindWorkspace(context.Background(), selected, parent, reservation); !errors.Is(err, ErrUnknown) {
		t.Fatalf("incomplete sibling allowed recovery of %q: %v", original.Path(), err)
	}
	if _, err := FindWorkspaces(context.Background(), selected, parent, reservation); !errors.Is(err, ErrUnknown) {
		t.Fatalf("multi-workspace search skipped incomplete sibling: %v", err)
	}
}

func TestWorkspaceFindCancelledBeforeSearch(t *testing.T) {
	_, selected, parent, _, reservation := testWorkspaceEvidence(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := FindWorkspace(ctx, selected, parent, reservation); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled search continued: %v", err)
	}
}

func TestWorkspaceFindRejectsParentThatLostPrivacy(t *testing.T) {
	_, selected, parent, original, reservation := testWorkspaceEvidence(t)
	if err := os.Chmod(parent.Path(), 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent.Path(), 0700) })
	if _, err := InspectWorkspace(selected, parent, original.Path(), reservation); !errors.Is(err, ErrChanged) {
		t.Fatalf("inspect accepted non-private parent: %v", err)
	}
	if _, err := FindWorkspace(context.Background(), selected, parent, reservation); !errors.Is(err, ErrChanged) {
		t.Fatalf("search accepted non-private parent: %v", err)
	}
}
