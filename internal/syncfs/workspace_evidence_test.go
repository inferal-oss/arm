package syncfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"getarm.org/internal/syncreconcile"
)

func testWorkspaceEvidence(t *testing.T) (*Root, *Root, *Root, *Workspace, Reservation) {
	t.Helper()
	_, source, selected, parent, _ := testDirs(t)
	v := Reservation{Key: "workspace-evidence-123", PlanDigest: digestWorkspaceTest("plan"), Generation: 1, Side: syncreconcile.Remote, DiskBytes: 8 << 20}
	w, err := CreateWorkspace(selected, parent, v)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return source, selected, parent, w, v
}

func digestWorkspaceTest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestWorkspaceEvidenceReopensOnlyOriginalRootAndReservation(t *testing.T) {
	source, selected, parent, w, reservation := testWorkspaceEvidence(t)
	reopened, err := InspectWorkspace(selected, parent, w.Path(), reservation)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Stage.Path() != w.Stage.Path() || reopened.Quarantine.Path() != w.Quarantine.Path() {
		t.Fatal("reopened workspace differs from original private directories")
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectWorkspace(source, parent, w.Path(), reservation); !errors.Is(err, ErrChanged) {
		t.Fatalf("different selected root adopted workspace: %v", err)
	}
	wrong := reservation
	wrong.Key = "different-workspace-key"
	if _, err := InspectWorkspace(selected, parent, w.Path(), wrong); !errors.Is(err, ErrChanged) {
		t.Fatalf("different reservation adopted workspace: %v", err)
	}
	if err := os.Rename(w.StagePath(), w.StagePath()+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(w.StagePath(), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectWorkspace(selected, parent, w.Path(), reservation); !errors.Is(err, ErrChanged) {
		t.Fatalf("replacement private stage adopted: %v", err)
	}
}

func TestWorkspaceEvidenceRejectsMissingMarkerAndNamedReplacement(t *testing.T) {
	_, selected, parent, w, reservation := testWorkspaceEvidence(t)
	markerPath := filepath.Join(w.Path(), workspaceMarkerName)
	raw, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectWorkspace(selected, parent, w.Path(), reservation); err == nil {
		t.Fatal("workspace without durable marker adopted")
	}
	sentinel := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(sentinel, []byte("outside data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, markerPath); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectWorkspace(selected, parent, w.Path(), reservation); err == nil {
		t.Fatal("symlinked workspace marker adopted")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "outside data" {
		t.Fatalf("outside sentinel changed: %q %v", data, err)
	}
	if err := os.Remove(markerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	oldPath := w.Path() + ".moved"
	if err := os.Rename(w.Path(), oldPath); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{w.Path(), filepath.Join(w.Path(), "stage"), filepath.Join(w.Path(), "quarantine")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(markerPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectWorkspace(selected, parent, w.Path(), reservation); !errors.Is(err, ErrChanged) {
		t.Fatalf("replacement directory with copied marker adopted: %v", err)
	}
}
