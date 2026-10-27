package syncfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWorkspaceReviewMarkerFIFOChild(t *testing.T) {
	if os.Getenv("ARM_WORKSPACE_REVIEW_FIFO_CHILD") != "1" {
		return
	}
	path := os.Getenv("ARM_WORKSPACE_REVIEW_FIFO_PATH")
	if !workspaceName(path, filepath.Dir(path)) {
		t.Fatal("exact private workspace path required")
	}
	base, err := OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	ready := os.NewFile(3, "workspace-marker-ready")
	if ready == nil {
		t.Fatal("original readiness pipe required")
	}
	if _, err := ready.Write([]byte{'R'}); err != nil {
		t.Fatal(err)
	}
	ready.Close()
	if _, err := readWorkspaceMarker(base); err == nil {
		t.Fatal("FIFO acquired marker evidence")
	}
}

func TestWorkspaceReviewMarkerFIFORefusesAndJoins(t *testing.T) {
	_, selected, parent, workspace, reservation := testWorkspaceEvidence(t)
	path := filepath.Join(workspace.Path(), workspaceMarkerName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	readyRead, readyWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyRead.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkspaceReviewMarkerFIFOChild$")
	cmd.Env = append(os.Environ(), "ARM_WORKSPACE_REVIEW_FIFO_CHILD=1", "ARM_WORKSPACE_REVIEW_FIFO_PATH="+workspace.Path())
	cmd.ExtraFiles = []*os.File{readyWrite}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		readyWrite.Close()
		t.Fatal(err)
	}
	readyWrite.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	joined := false
	defer func() {
		if !joined {
			_ = cmd.Process.Kill() // original unreaped fixture only
			<-done
		}
	}()
	if err := readyRead.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [1]byte
	if _, err := io.ReadFull(readyRead, ready[:]); err != nil || ready[0] != 'R' {
		t.Fatal("original marker reader did not reach its native cut", err)
	}
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatalf("original marker reader failed: %v\n%s", err, output.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FIFO blocked marker inspection before type check and ignored bounded recovery")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if got, err := FindWorkspaces(ctx, selected, parent, reservation); got != nil || !errors.Is(err, ErrUnknown) {
		t.Fatal("FIFO search produced complete evidence", got, err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatal("inspection changed the retained FIFO evidence", err)
	}
}

func TestWorkspaceReviewSearchEntryBoundPreservesMarkerAndPayload(t *testing.T) {
	_, selected, parent, workspace, reservation := testWorkspaceEvidence(t)
	markerPath := filepath.Join(workspace.Path(), workspaceMarkerName)
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	payloadPath := filepath.Join(workspace.StagePath(), "retained-payload")
	payload := []byte("private retained stage data")
	if err := os.WriteFile(payloadPath, payload, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxWorkspaceSearchEntries-1; i++ {
		if err := os.WriteFile(filepath.Join(parent.Path(), fmt.Sprintf("unrelated-%05d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	found, err := FindWorkspaces(t.Context(), selected, parent, reservation)
	if err != nil || len(found) != 1 {
		t.Fatal("exact bounded directory inventory refused", err, len(found))
	}
	for _, candidate := range found {
		if err := candidate.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(parent.Path(), "over-search-limit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if found, err := FindWorkspaces(t.Context(), selected, parent, reservation); found != nil || !errors.Is(err, ErrUnknown) || !strings.Contains(err.Error(), "exceeded") {
		t.Fatal("over-limit inventory returned partial success", err, len(found))
	}
	for path, want := range map[string][]byte{markerPath: marker, payloadPath: payload} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("read-only bounded lookup changed private evidence", path, err)
		}
	}
}

func TestWorkspaceReviewMalformedSiblingKeepsResultUnknown(t *testing.T) {
	_, selected, parent, original, reservation := testWorkspaceEvidence(t)
	sibling, err := CreateWorkspace(selected, parent, reservation)
	if err != nil {
		t.Fatal(err)
	}
	defer sibling.Close()
	marker, err := os.ReadFile(filepath.Join(sibling.Path(), workspaceMarkerName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling.StagePath(), "retained-payload"), []byte("private sibling payload"), 0600); err != nil {
		t.Fatal(err)
	}
	moved := sibling.Path() + ".moved"
	if err := os.Rename(sibling.Path(), moved); err != nil {
		t.Fatal(err)
	}
	found, err := FindWorkspaces(t.Context(), selected, parent, reservation)
	for _, candidate := range found {
		_ = candidate.Close()
	}
	if found != nil || !errors.Is(err, ErrUnknown) {
		t.Fatal("malformed reserved sibling produced a complete-looking partial set", len(found), err)
	}
	for path, want := range map[string][]byte{
		filepath.Join(moved, workspaceMarkerName):         marker,
		filepath.Join(moved, "stage", "retained-payload"): []byte("private sibling payload"),
	} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("unknown lookup changed retained sibling evidence", path, err)
		}
	}
	if err := original.Base.Check(); err != nil {
		t.Fatal("unknown lookup altered the still-named original workspace", err)
	}
}
