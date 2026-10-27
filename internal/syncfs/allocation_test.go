package syncfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWorkspaceAllocationSurveysPrivateTreeWithoutFollowingLinks(t *testing.T) {
	_, selected, parent, work, reservation := testWorkspaceEvidence(t)
	nested := filepath.Join(work.StagePath(), "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "data"), []byte(strings.Repeat("survey-data", 1024)), 0600); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(sentinel, []byte("outside data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, filepath.Join(work.StagePath(), "outside-link")); err != nil {
		t.Fatal(err)
	}
	usage, err := SurveyWorkspace(context.Background(), selected, parent, work.Path(), reservation)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Entries < 7 || usage.AllocatedBytes <= 0 {
		t.Fatalf("private allocation survey omitted entries or blocks: %+v", usage)
	}
	if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "outside data" {
		t.Fatalf("outside sentinel changed: %q %v", raw, err)
	}
}

func TestWorkspaceAllocationRefusesSpecialEntryWithoutBlocking(t *testing.T) {
	_, selected, parent, work, reservation := testWorkspaceEvidence(t)
	if err := unix.Mkfifo(filepath.Join(work.StagePath(), "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := SurveyWorkspace(ctx, selected, parent, work.Path(), reservation); !errors.Is(err, ErrChanged) {
		t.Fatalf("special entry was surveyed or blocked: %v", err)
	}
}

func TestWorkspaceAllocationRefusesMissingMarker(t *testing.T) {
	_, selected, parent, work, reservation := testWorkspaceEvidence(t)
	if err := os.Remove(filepath.Join(work.Path(), workspaceMarkerName)); err != nil {
		t.Fatal(err)
	}
	if _, err := SurveyWorkspace(context.Background(), selected, parent, work.Path(), reservation); err == nil {
		t.Fatal("unmarked workspace was surveyed")
	}
}

func TestWorkspaceAllocationRespectsCancellation(t *testing.T) {
	_, selected, parent, work, reservation := testWorkspaceEvidence(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := SurveyWorkspace(ctx, selected, parent, work.Path(), reservation); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled survey proceeded: %v", err)
	}
}
