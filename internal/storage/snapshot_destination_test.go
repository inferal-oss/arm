package storage

// Native controls for destination ownership and pre-write checks. The error
// reported after real Sync is explicitly an injection, not an OS failure.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

func TestSnapshotRejectsUnsafeExistingParentBeforePayloadFileCreation(t *testing.T) {
	for _, scenario := range []string{"writable-parent", "writable-nested-parent", "symlink-parent"} {
		t.Run(scenario, func(t *testing.T) {
			p, source, _, plan := snapshotTestFixture(t)
			path := "tree/data"
			if scenario == "writable-nested-parent" {
				path = "tree/nested/data"
				var err error
				plan, err = p.PlanSnapshot(deadline(t), source, []model.ExportSelection{{Source: "payload", Path: path}}, MaxExportBytes, SnapshotIdleNative, func(ctx context.Context) error { return ctx.Err() })
				if err != nil {
					t.Fatal(err)
				}
			}
			phases := []string{}
			callbacks := snapshotTestCallbacks(t, p, &phases)
			owned := callbacks.Owned
			outside := t.TempDir()
			callbacks.Owned = func(ctx context.Context, intent SnapshotCopyIntent, artifact model.Artifact) error {
				if err := owned(ctx, intent, artifact); err != nil {
					return err
				}
				parent := filepath.Join(artifact.Path, filepath.Dir(path))
				if scenario == "symlink-parent" {
					return os.Symlink(outside, filepath.Join(artifact.Path, "tree"))
				}
				if err := os.MkdirAll(parent, 0700); err != nil {
					return err
				}
				return os.Chmod(parent, 0777) // independent of the caller's umask
			}
			result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), callbacks)
			if err == nil || result.Artifact == nil || result.Phase == "prepared" || !result.FilesClosed || result.UnknownClose {
				t.Fatal("unsafe parent did not retain a clean refused copy", result, err)
			}
			if _, err = os.Lstat(filepath.Join(result.Artifact.Path, path)); !os.IsNotExist(err) {
				t.Fatal("payload file was created through unsafe parent", err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("outside symlink destination changed", entries, err)
			}
			if strings.Contains(strings.Join(phases, ","), "prepared") {
				t.Fatal("unsafe namespace was prepared", phases)
			}
		})
	}
}

func TestSnapshotRevalidatesExactOutputPathAndFileBeforeFirstWrite(t *testing.T) {
	for _, scenario := range []string{"parent-replaced", "parent-made-writable", "file-made-writable", "file-hardlinked"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, _, plan := snapshotTestFixture(t)
			phases := []string{}
			callbacks := snapshotTestCallbacks(t, p, &phases)
			changed := false
			outside := filepath.Join(t.TempDir(), "linked-output")
			callbacks.Current = func(ctx context.Context) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				path := filepath.Join(p.Path(snapshotTestDestination), "tree", "data")
				info, err := os.Lstat(path)
				if changed || err != nil || info.Size() != 0 {
					return nil
				}
				changed = true
				switch scenario {
				case "parent-replaced":
					parent := filepath.Dir(path)
					if err = os.Rename(parent, parent+"-old"); err != nil {
						return err
					}
					if err = os.Mkdir(parent, 0700); err != nil {
						return err
					}
					return os.WriteFile(path, nil, 0600) // same size/mode, different native identity
				case "parent-made-writable":
					return os.Chmod(filepath.Dir(path), 0777)
				case "file-made-writable":
					return os.Chmod(path, 0777)
				case "file-hardlinked":
					return os.Link(path, outside)
				}
				return nil
			}
			result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), callbacks)
			if !changed || err == nil || result.Artifact == nil || result.Phase == "prepared" || !result.FilesClosed || result.UnknownClose {
				t.Fatal("real prewrite mutation cut missing/refusal lost", changed, result, err)
			}
			original := filepath.Join(result.Artifact.Path, "tree", "data")
			if scenario == "parent-replaced" {
				data, err := os.ReadFile(original)
				if err != nil || len(data) != 0 {
					t.Fatal("replacement file was written", err)
				}
				original = filepath.Join(result.Artifact.Path, "tree-old", "data")
			}
			if info, err := os.Stat(original); err != nil || info.Size() != 0 {
				t.Fatal("payload escaped before exact prewrite checks", info, err)
			}
			if scenario == "file-hardlinked" {
				if info, err := os.Stat(outside); err != nil || info.Size() != 0 {
					t.Fatal("payload leaked to outside hardlink", err)
				}
			}
		})
	}
}

func TestSnapshotAllocationReportedSyncErrorRetainsActualAttemptBeforeOwned(t *testing.T) {
	p, _, _, plan := snapshotTestFixture(t)
	phases := []string{}
	callbacks := snapshotTestCallbacks(t, p, &phases)
	owners := newSnapshotFDSet()
	failure := errors.New("explicit reported error after real allocation parent Sync")
	actualProviderSyncs := 0
	owners.syncFile = func(file *os.File) error {
		err := file.Sync()
		if owners.records[owners.files[file]].Role == "provider" {
			actualProviderSyncs++
			return errors.Join(err, failure)
		}
		return err
	}
	t.Log("control: the real Sync is attempted; a reported error is injected afterward, not claimed as an OS Sync failure")
	result, err := p.copySnapshot(deadline(t), snapshotTestIntent(plan), callbacks, owners)
	if !errors.Is(err, failure) || actualProviderSyncs != 1 || result.Phase != "allocation-unknown" || result.Artifact == nil || result.Artifact.Phase != "creating" || !result.FilesClosed || result.UnknownClose || strings.Join(phases, ",") != "intent" {
		t.Fatal("allocation sync failure advanced or lost its original facts", result, phases, actualProviderSyncs, err)
	}
	entries, readErr := os.ReadDir(result.Artifact.Path)
	if readErr != nil || len(entries) != 0 {
		t.Fatal("payload/checkpoint preceded allocation Sync", entries, readErr)
	}
	providerUnknown := false
	childSynced := false
	for _, descriptor := range result.Native.Descriptors {
		if descriptor.Role == "provider" && descriptor.Sync == "unknown" && descriptor.SyncError == failure.Error() && descriptor.State == "closed" {
			providerUnknown = true
		}
		if descriptor.Role == "allocation-root" && descriptor.Sync == "synced" && descriptor.State == "closed" {
			childSynced = true
		}
	}
	if !providerUnknown || !childSynced {
		t.Fatal("actual ordered Sync attempts were replaced with not-requested", result.Native)
	}
}

func TestSnapshotDestinationTypedGuardIncludesActualMountObservation(t *testing.T) {
	p, a := fixture(t)
	owners := newSnapshotFDSet()
	t.Cleanup(func() { _ = owners.closeAll() })
	provider, err := p.snapshotRetain(owners)
	if err != nil {
		t.Fatal(err)
	}
	root, err := provider.snapshotOpenArtifact(deadline(t), a, owners, "private-directory-control")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.snapshotCheckDirectory(root, owners); err != nil {
		t.Fatal("actual private same-provider positive control", err)
	}
	wrong := &Provider{path: provider.path, root: provider.root, identity: provider.identity, mount: provider.mount + "-different-expected-mount"}
	if _, err = wrong.snapshotCheckDirectory(root, owners); err == nil {
		t.Fatal("actual native mount observation was not compared")
	}
	t.Log("expected-mount corruption control only; no actual cross-mount/bind-mount isolation or privileged setup is claimed")
}
