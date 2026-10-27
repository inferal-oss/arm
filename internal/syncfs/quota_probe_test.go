package syncfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeQuotaProbeRefusesChangedHeldRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	selectedPath, parentPath := filepath.Join(base, "selected"), filepath.Join(base, "parent")
	for _, path := range []string{selectedPath, parentPath} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	selected, err := OpenRoot(selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	parent, err := OpenRoot(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := os.Rename(parentPath, filepath.Join(base, "old-parent")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parentPath, 0700); err != nil {
		t.Fatal(err)
	}
	if v, err := ProbeNativeQuota(t.Context(), selected, parent); !errors.Is(err, ErrChanged) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("replaced parent became quota evidence: %+v %v", v, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if v, err := ProbeNativeQuota(ctx, selected, parent); !errors.Is(err, context.Canceled) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("canceled probe produced quota evidence: %+v %v", v, err)
	}
}

func TestNativeQuotaProbeUsesExistingHostStatus(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	selectedPath, parentPath := filepath.Join(base, "selected"), filepath.Join(base, "parent")
	for _, path := range []string{selectedPath, parentPath} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	selected, err := OpenRoot(selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	parent, err := OpenRoot(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	evidence, err := ProbeNativeQuota(t.Context(), selected, parent)
	if errors.Is(err, ErrQuotaUnknown) {
		t.Skipf("native quota query unavailable: %v", err)
	}
	if errors.Is(err, ErrQuotaUnsupported) {
		if evidence != (NativeQuotaEvidence{}) {
			t.Fatalf("unsupported host returned evidence: %+v", evidence)
		}
		t.Log("existing host scope has no supported hard quota")
		return
	}
	if err != nil || evidence.Provider() == "" || evidence.Scope() == "" || evidence.HardBytes() == 0 || evidence.UsedBytes() > evidence.HardBytes() {
		t.Fatalf("invalid native quota evidence: %+v %v", evidence, err)
	}
}

func TestNativeQuotaProbeRechecksHeldRootsAfterNativeRead(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	selectedPath, parentPath := filepath.Join(base, "selected"), filepath.Join(base, "parent")
	for _, path := range []string{selectedPath, parentPath} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	selected, err := OpenRoot(selectedPath)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	parent, err := OpenRoot(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	stableRead := func(context.Context, *Root, *Root, string) (NativeQuotaEvidence, error) {
		return NativeQuotaEvidence{provider: "test", scope: "test-scope", hardBytes: 4096}, nil
	}
	bound, err := probeNativeQuota(t.Context(), selected, parent, stableRead)
	if err != nil || bound.SelectedRootID() == "" || bound.ParentRootID() == "" || bound.Mount() == "" {
		t.Fatalf("native evidence was not bound to held roots: %+v %v", bound, err)
	}
	read := func(context.Context, *Root, *Root, string) (NativeQuotaEvidence, error) {
		if err := os.Rename(parentPath, filepath.Join(base, "old-parent")); err != nil {
			return NativeQuotaEvidence{}, err
		}
		if err := os.Mkdir(parentPath, 0700); err != nil {
			return NativeQuotaEvidence{}, err
		}
		return NativeQuotaEvidence{provider: "test", scope: "test-scope", hardBytes: 4096}, nil
	}
	if v, err := probeNativeQuota(t.Context(), selected, parent, read); !errors.Is(err, ErrChanged) || v != (NativeQuotaEvidence{}) {
		t.Fatalf("native read adopted replaced root: %+v %v", v, err)
	}
}
