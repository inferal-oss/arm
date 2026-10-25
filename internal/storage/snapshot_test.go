package storage

// Native filesystem component tests.
// Synthetic callbacks stand for explicit authority/durable admission inputs;
// they do not validate Manager's future SQL transaction, grants or publication.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

const snapshotTestDestination = "1234567890abcdef1234567890abcdef"

func snapshotTestFixture(t *testing.T) (*Provider, model.Artifact, []byte, SnapshotPlan) {
	t.Helper()
	p, source := fixture(t)
	content := append(bytes.Repeat([]byte("snapshot-test-data"), SnapshotChunkBytes/18+1), []byte("tail")...)
	if err := os.WriteFile(filepath.Join(source.Path, "payload"), content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source.Path, "empty"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := p.PlanSnapshot(deadline(t), source, []model.ExportSelection{{Source: "payload", Path: "tree/data"}, {Source: "empty", Path: "empty"}}, MaxExportBytes, SnapshotIdleNative, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal("real snapshot plan", err)
	}
	return p, source, content, plan
}

func snapshotTestIntent(plan SnapshotPlan) SnapshotCopyIntent {
	return SnapshotCopyIntent{Key: "private-native-copy", Destination: snapshotTestDestination, Plan: plan, DiskPromise: plan.Bytes + snapshotMetadataBytes, CopyResources: model.Resources{CPU: 1, Memory: SnapshotCopyMemoryFloor}, CopyFDs: 8}
}

// The test journals real intent/inode/prepared records with exclusive create,
// fsync and parent fsync. No payload may precede the actual Owned checkpoint.
func snapshotTestCallbacks(t *testing.T, p *Provider, phases *[]string) SnapshotCallbacks {
	t.Helper()
	dir := t.TempDir()
	commit := func(name string, value any) error {
		file, err := os.OpenFile(filepath.Join(dir, name+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(value)
		if err == nil {
			_, err = file.Write(raw)
		}
		err = errors.Join(err, file.Sync(), file.Close())
		if err != nil {
			return err
		}
		parent, err := os.Open(dir)
		if err != nil {
			return err
		}
		err = errors.Join(parent.Sync(), parent.Close())
		if err == nil {
			*phases = append(*phases, name)
		}
		return err
	}
	return SnapshotCallbacks{
		Current: func(ctx context.Context) error { return ctx.Err() },
		Intent: func(ctx context.Context, intent SnapshotCopyIntent) error {
			if _, err := os.Lstat(p.Path(intent.Destination)); !os.IsNotExist(err) {
				return fmt.Errorf("intent preceded by an existing allocation: %v", err)
			}
			return commit("intent", intent)
		},
		Owned: func(ctx context.Context, intent SnapshotCopyIntent, artifact model.Artifact) error {
			if artifact.Identity == nil || !artifact.Identity.BirthKnown || artifact.Name != intent.Destination {
				return fmt.Errorf("actual new inode checkpoint missing")
			}
			if err := p.Check(artifact); err != nil {
				return err
			}
			entries, err := os.ReadDir(artifact.Path)
			if err != nil || len(entries) != 0 {
				return fmt.Errorf("payload preceded inode checkpoint: %v", err)
			}
			return commit("owned", artifact)
		},
		Prepared: func(ctx context.Context, intent SnapshotCopyIntent, result SnapshotCopyResult) error {
			if !result.FilesClosed || result.UnknownClose || result.Phase != "prepared" || result.Native.validateClosed() != nil {
				return fmt.Errorf("prepared before original descriptor joins")
			}
			if err := result.Generation.validate(); err != nil {
				return err
			}
			return commit("prepared", result)
		},
	}
}

func TestSnapshotCopiesNewOwnedGenerationAndDecouplesOriginal(t *testing.T) {
	p, source, content, plan := snapshotTestFixture(t)
	phases := []string{}
	result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), snapshotTestCallbacks(t, p, &phases))
	if err != nil || result.Phase != "prepared" || !result.FilesClosed || result.UnknownClose || strings.Join(phases, ",") != "intent,owned,prepared" {
		t.Fatal(result, phases, err)
	}
	if result.Artifact == nil || result.Artifact.Path != p.Path(snapshotTestDestination) {
		t.Fatal("destination not independently managed", result)
	}
	providerSynced, allocationSynced := false, false
	for _, descriptor := range result.Native.Descriptors {
		if descriptor.Role == "provider" && descriptor.Sync == "synced" {
			providerSynced = true
		}
		if descriptor.Role == "allocation-root" && descriptor.Sync == "synced" {
			allocationSynced = true
		}
	}
	if !providerSynced || !allocationSynced {
		t.Fatal("successful allocation did not retain both actual Sync receipts", result.Native)
	}
	for _, file := range result.Generation.Files {
		if file.Source.Version.Identity == file.Output.Identity || file.Output.Mode != 0400 || file.Output.Links != 1 {
			t.Fatal("shared/executable/writable output", file)
		}
	}
	// Emulate a separately committed S publication's authority input. The core
	// does not implement or claim that commit. All reads are real destination FDs.
	allowed := func(ctx context.Context) error { return ctx.Err() }
	first, err := p.ReadSnapshotChunk(deadline(t), result.Generation, "tree/data", 0, allowed)
	if err != nil || !bytes.Equal(first.Data, content[:SnapshotChunkBytes]) {
		t.Fatal("initial whole chunk", err)
	}
	first.Data[0] ^= 0xff // returned bytes are exclusively owned, not a shared cache.
	if err := os.WriteFile(filepath.Join(source.Path, "payload"), []byte("new source contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source.Path, source.Path+"-renamed"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source.Path+"-renamed", 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(source.Path+"-renamed", 0700) })
	again, err := p.ReadSnapshotChunk(deadline(t), result.Generation, "tree/data", 0, allowed)
	if err != nil || !bytes.Equal(again.Data, content[:SnapshotChunkBytes]) {
		t.Fatal("published generation followed mutable original or borrowed buffer", err)
	}
	tail, err := p.ReadSnapshotChunk(deadline(t), result.Generation, "tree/data", 1, allowed)
	if err != nil || !bytes.Equal(tail.Data, content[SnapshotChunkBytes:]) {
		t.Fatal("canonical final chunk", err)
	}
}

func TestSnapshotCheckpointFailuresPreserveExactPartialAllocation(t *testing.T) {
	for _, phase := range []string{"intent", "owned", "prepared"} {
		t.Run(phase, func(t *testing.T) {
			p, _, _, plan := snapshotTestFixture(t)
			phases := []string{}
			callbacks := snapshotTestCallbacks(t, p, &phases)
			failure := errors.New("private durable checkpoint failure")
			switch phase {
			case "intent":
				callbacks.Intent = func(context.Context, SnapshotCopyIntent) error { return failure }
			case "owned":
				callbacks.Owned = func(context.Context, SnapshotCopyIntent, model.Artifact) error { return failure }
			case "prepared":
				callbacks.Prepared = func(context.Context, SnapshotCopyIntent, SnapshotCopyResult) error { return failure }
			}
			result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), callbacks)
			if !errors.Is(err, failure) {
				t.Fatal("original failure lost", result, err)
			}
			if !result.FilesClosed || result.UnknownClose || !result.Native.Closed {
				t.Fatal("clean failure/cancel discarded actual original close facts", result.Native)
			}
			entries, statErr := os.ReadDir(p.Path(snapshotTestDestination))
			if phase == "intent" {
				if !os.IsNotExist(statErr) || result.Artifact != nil {
					t.Fatal("allocation before durable admission", result, statErr)
				}
			} else {
				if statErr != nil || result.Artifact == nil || p.Check(*result.Artifact) != nil {
					t.Fatal("partial owned evidence disappeared", result, statErr)
				}
				if phase == "owned" && len(entries) != 0 {
					t.Fatal("payload before identity checkpoint")
				}
				if phase == "prepared" && (!result.FilesClosed || result.Phase != "prepared-unknown") {
					t.Fatal("lost final reply allowed recopy or lost join facts", result)
				}
			}
		})
	}
}

func TestSnapshotRejectsChangedSourceOrRevokedAuthorityBeforePayload(t *testing.T) {
	for _, scenario := range []string{"source-content", "source-replaced", "source-active", "revoke-after-owned", "cancel-after-owned"} {
		t.Run(scenario, func(t *testing.T) {
			p, source, _, plan := snapshotTestFixture(t)
			phases := []string{}
			callbacks := snapshotTestCallbacks(t, p, &phases)
			var held *os.File
			ctx, cancel := context.WithCancel(deadline(t))
			defer cancel()
			switch scenario {
			case "source-content":
				if err := os.WriteFile(filepath.Join(source.Path, "payload"), []byte("different"), 0600); err != nil {
					t.Fatal(err)
				}
			case "source-replaced":
				if err := os.Rename(source.Path, source.Path+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(source.Path, 0700); err != nil {
					t.Fatal(err)
				}
			case "source-active":
				var err error
				held, err = os.Open(filepath.Join(source.Path, "payload"))
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
			case "revoke-after-owned", "cancel-after-owned":
				owned := callbacks.Owned
				callbacks.Owned = func(ctx context.Context, intent SnapshotCopyIntent, artifact model.Artifact) error {
					if err := owned(ctx, intent, artifact); err != nil {
						return err
					}
					if scenario == "cancel-after-owned" {
						cancel()
					}
					return nil
				}
				// The original callback object remains fixed; this explicit source
				// policy flag models a revoke, not an authority method substitution.
				if scenario == "revoke-after-owned" {
					revoked := false
					callbacks.Current = func(ctx context.Context) error {
						if revoked {
							return errors.New("source entitlement revoked")
						}
						return ctx.Err()
					}
					callbacks.Owned = func(ctx context.Context, intent SnapshotCopyIntent, artifact model.Artifact) error {
						if err := owned(ctx, intent, artifact); err != nil {
							return err
						}
						revoked = true
						return nil
					}
				}
			}
			result, err := p.CopySnapshot(ctx, snapshotTestIntent(plan), callbacks)
			if err == nil || result.Phase == "prepared" {
				t.Fatal("invalid source produced prepared generation", result, err)
			}
			if result.Artifact != nil {
				entries, err := os.ReadDir(result.Artifact.Path)
				if err != nil || len(entries) != 0 {
					t.Fatal("payload after cancellation/revoke", result, err)
				}
			}
		})
	}
}

func TestSnapshotRejectsRealMidCopyMutationAndRetainsOwnedPartialData(t *testing.T) {
	p, source, content, plan := snapshotTestFixture(t)
	phases := []string{}
	callbacks := snapshotTestCallbacks(t, p, &phases)
	mutated := false
	callbacks.Current = func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Stat(filepath.Join(p.Path(snapshotTestDestination), "tree", "data"))
		if !mutated && err == nil && info.Size() >= SnapshotChunkBytes {
			changed := append([]byte(nil), content...)
			changed[len(changed)-1] ^= 0xff
			if err := os.WriteFile(filepath.Join(source.Path, "payload"), changed, 0600); err != nil {
				return err
			}
			mutated = true
		}
		return nil
	}
	result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), callbacks)
	if !mutated || err == nil || result.Artifact == nil || result.Phase == "prepared" || strings.Contains(strings.Join(phases, ","), "prepared") {
		t.Fatal("real copy cut not exercised/refused", result, phases, mutated, err)
	}
	if err := p.Check(*result.Artifact); err != nil {
		t.Fatal("uncertain copied allocation was erased/adopted", err)
	}
	if info, err := os.Stat(filepath.Join(result.Artifact.Path, "tree", "data")); err != nil || info.Size() < SnapshotChunkBytes {
		t.Fatal("actual partial payload not retained", err)
	}
}

func TestSnapshotFinalReadAuthorityAndTamperChecks(t *testing.T) {
	for _, scenario := range []string{"bytes", "replace", "hardlink", "root-replaced", "final-revoke", "cancel", "wrong-path", "wrong-chunk", "changed-manifest"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, _, plan := snapshotTestFixture(t)
			phases := []string{}
			result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), snapshotTestCallbacks(t, p, &phases))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(result.Artifact.Path, "tree", "data")
			current := func(ctx context.Context) error { return ctx.Err() }
			name, chunk := "tree/data", 0
			ctx, cancel := context.WithCancel(deadline(t))
			defer cancel()
			switch scenario {
			case "bytes":
				if err = os.Chmod(path, 0600); err == nil {
					err = os.WriteFile(path, []byte("tamper"), 0400)
				}
			case "replace":
				if err = os.Remove(path); err == nil {
					err = os.WriteFile(path, []byte("new inode"), 0400)
				}
			case "hardlink":
				err = os.Link(path, filepath.Join(t.TempDir(), "outside"))
			case "root-replaced":
				if err = os.Rename(result.Artifact.Path, result.Artifact.Path+"-old"); err == nil {
					err = os.Mkdir(result.Artifact.Path, 0700)
				}
			case "final-revoke":
				calls := 0
				current = func(context.Context) error {
					calls++
					if calls > 1 {
						return errors.New("generation revoked at final cut")
					}
					return nil
				}
			case "cancel":
				cancel()
			case "wrong-path":
				name = "../payload"
			case "wrong-chunk":
				chunk = 2
			case "changed-manifest":
				result.Generation.Files[0].Output.Size++
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := p.ReadSnapshotChunk(ctx, result.Generation, name, chunk, current)
			if err == nil || data.Data != nil {
				t.Fatal("unauthorized or changed bytes escaped", len(data.Data), err)
			}
		})
	}
}

func TestSnapshotNeverAdoptsExistingAllocationOrBypassesFullCopyPromise(t *testing.T) {
	for _, scenario := range []string{"existing", "source-destination", "short-promise", "missing-checkpoint", "missing-current", "plan-mutation", "zero-cpu", "short-memory", "missing-fd-budget"} {
		t.Run(scenario, func(t *testing.T) {
			p, source, _, plan := snapshotTestFixture(t)
			intent := snapshotTestIntent(plan)
			phases := []string{}
			callbacks := snapshotTestCallbacks(t, p, &phases)
			marker := filepath.Join(p.Path(snapshotTestDestination), "preserve")
			switch scenario {
			case "existing":
				if _, err := p.Create(snapshotTestDestination); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(marker, []byte("unrelated allocation"), 0600); err != nil {
					t.Fatal(err)
				}
				callbacks.Intent = func(context.Context, SnapshotCopyIntent) error { return nil }
			case "source-destination":
				intent.Destination = source.Name
			case "short-promise":
				intent.DiskPromise = plan.Bytes
			case "missing-checkpoint":
				callbacks.Owned = nil
			case "missing-current":
				callbacks.Current = nil
			case "plan-mutation":
				intent.Plan.Files[0].Version.Size++
			case "zero-cpu":
				intent.CopyResources.CPU = 0
			case "short-memory":
				intent.CopyResources.Memory = SnapshotCopyMemoryFloor - 1
			case "missing-fd-budget":
				intent.CopyFDs = 0
			}
			result, err := p.CopySnapshot(deadline(t), intent, callbacks)
			if err == nil || result.Phase == "prepared" {
				t.Fatal("unsafe copy admitted", result, err)
			}
			if scenario == "existing" {
				data, err := os.ReadFile(marker)
				if err != nil || string(data) != "unrelated allocation" {
					t.Fatal("adopted/changed existing destination", err)
				}
			}
		})
	}
}

func TestSnapshotTwoOwnedCopySlotsRemainOccupiedUntilOriginalCallsJoin(t *testing.T) {
	p, _, _, firstPlan := snapshotTestFixture(t)
	secondName := strings.Repeat("2", 32)
	id, err := p.Create(secondName)
	if err != nil {
		t.Fatal(err)
	}
	secondSource := model.Artifact{Name: secondName, Path: p.Path(secondName), Identity: &id, Phase: "owned"}
	if err = os.WriteFile(filepath.Join(secondSource.Path, "data"), []byte("second-source"), 0600); err != nil {
		t.Fatal(err)
	}
	secondPlan, err := p.PlanSnapshot(deadline(t), secondSource, []model.ExportSelection{{Source: "data"}}, MaxExportBytes, SnapshotIdleNative, func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	done := []chan struct{}{make(chan struct{}), make(chan struct{})}
	results := make(chan error, 2)
	owned := []chan struct{}{make(chan struct{}), make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		for _, original := range done {
			select {
			case <-original:
			case <-time.After(10 * time.Second):
				t.Error("original native copy call did not join")
			}
		}
	})
	for i, plan := range []SnapshotPlan{firstPlan, secondPlan} {
		intent := snapshotTestIntent(plan)
		if i == 1 {
			intent.Key = "second-private-copy"
			intent.Destination = strings.Repeat("3", 32)
		}
		phases := []string{}
		callbacks := snapshotTestCallbacks(t, p, &phases)
		checkpoint := callbacks.Owned
		callbacks.Owned = func(ctx context.Context, intent SnapshotCopyIntent, artifact model.Artifact) error {
			if err := checkpoint(ctx, intent, artifact); err != nil {
				return err
			}
			close(owned[i])
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		go func() { defer close(done[i]); _, err := p.CopySnapshot(deadline(t), intent, callbacks); results <- err }()
	}
	for _, checkpoint := range owned {
		select {
		case <-checkpoint:
		case err := <-results:
			t.Fatal("copy failed before actual held cut", err)
		case <-time.After(10 * time.Second):
			t.Fatal("copy did not reach actual inode checkpoint")
		}
	}
	third := snapshotTestIntent(firstPlan)
	third.Key = "third-private-copy"
	third.Destination = strings.Repeat("4", 32)
	phases := []string{}
	if _, err := p.CopySnapshot(deadline(t), third, snapshotTestCallbacks(t, p, &phases)); err == nil || len(phases) != 0 {
		t.Fatal("occupied native copy slots were reused", phases, err)
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal("original copy failed", err)
		}
	}
	if len(p.snapshotSlots) != 0 {
		t.Fatal("clean original joins failed to release owned slots")
	}
}

func TestSnapshotCallbacksAndReadNeverBorrowMutableReviewEvidence(t *testing.T) {
	p, _, content, plan := snapshotTestFixture(t)
	phases := []string{}
	callbacks := snapshotTestCallbacks(t, p, &phases)
	owned, prepared := callbacks.Owned, callbacks.Prepared
	callbacks.Owned = func(ctx context.Context, intent SnapshotCopyIntent, artifact model.Artifact) error {
		if err := owned(ctx, intent, artifact); err != nil {
			return err
		}
		intent.Plan.Files[0].Version.Source = "../unrelated"
		artifact.Identity.Inode++
		return nil
	}
	callbacks.Prepared = func(ctx context.Context, intent SnapshotCopyIntent, result SnapshotCopyResult) error {
		if err := prepared(ctx, intent, result); err != nil {
			return err
		}
		result.Generation.SHA256 = "untrusted-callback-mutation"
		result.Native.Descriptors[0].State = "unknown"
		return nil
	}
	result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), callbacks)
	if err != nil || result.Generation.validate() != nil || result.Native.validateClosed() != nil {
		t.Fatal("callback mutated retained canonical/native evidence", result, err)
	}
	generation := result.Generation
	changed := false
	data, err := p.ReadSnapshotChunk(deadline(t), generation, "tree/data", 0, func(ctx context.Context) error {
		if !changed {
			for i := range generation.Files {
				if generation.Files[i].Output.Path == "tree/data" {
					generation.Files[i].Source.Chunks[0] = strings.Repeat("0", 64)
				}
			}
			generation.Artifact.Identity.Inode++
			changed = true
		}
		return ctx.Err()
	})
	if err != nil || !changed || !bytes.Equal(data.Data, content[:SnapshotChunkBytes]) || data.Native.validateClosed() != nil {
		t.Fatal("read borrowed mutable manifest review", data.Native, err)
	}
}

func TestSnapshotCurrentCallbacksCannotMutateFrozenCallerInputs(t *testing.T) {
	for _, scenario := range []string{"plan-artifact", "copy-intent", "read-generation"} {
		t.Run(scenario, func(t *testing.T) {
			p, source, content, plan := snapshotTestFixture(t)
			switch scenario {
			case "plan-artifact":
				input := source
				input.Retention = &model.StorageRetention{Reason: "reviewed-retention"}
				expected := *input.Identity
				changed := false
				frozen, err := p.PlanSnapshot(deadline(t), input, []model.ExportSelection{{Source: "payload", Path: "tree/data"}}, MaxExportBytes, SnapshotIdleNative, func(ctx context.Context) error {
					if !changed {
						input.Identity.Inode++
						input.Retention.Reason = "caller-mutated-retention"
						changed = true
					}
					return ctx.Err()
				})
				if err != nil || !changed || frozen.Source.Identity == nil || *frozen.Source.Identity != expected || frozen.Source.Retention == nil || frozen.Source.Retention.Reason != "reviewed-retention" || frozen.Native.validateClosed() != nil || frozen.validate() != nil {
					t.Fatal("Current changed retained plan source facts", frozen, changed, err)
				}
			case "copy-intent":
				intent := snapshotTestIntent(plan)
				if len(intent.Plan.Files) != 2 || intent.Plan.Files[1].Version.Path != "tree/data" {
					t.Fatal("complete canonical fixture required")
				}
				expected := *intent.Plan.Source.Identity
				reviewedSHA := intent.Plan.SHA256
				changed := false
				phases := []string{}
				callbacks := snapshotTestCallbacks(t, p, &phases)
				callbacks.Current = func(ctx context.Context) error {
					if !changed {
						intent.Plan.Source.Identity.Inode++
						intent.Plan.Files[1].Version.Path = "caller-mutated-output"
						intent.Plan.Files[1].Chunks[0] = strings.Repeat("0", 64)
						intent.Plan.Native.Descriptors[0].Identity.Inode++
						changed = true
					}
					return ctx.Err()
				}
				originalIntent := callbacks.Intent
				callbacks.Intent = func(ctx context.Context, q SnapshotCopyIntent) error {
					if q.Plan.Source.Identity == nil || *q.Plan.Source.Identity != expected || q.Plan.SHA256 != reviewedSHA || q.Plan.Files[1].Version.Path != "tree/data" {
						return errors.New("caller mutated durable intent checkpoint")
					}
					return originalIntent(ctx, q)
				}
				result, err := p.CopySnapshot(deadline(t), intent, callbacks)
				if err != nil || !changed || result.Phase != "prepared" || result.Generation.SourceSHA != reviewedSHA || result.Native.validateClosed() != nil {
					t.Fatal("Current changed frozen copy intent", result, changed, err)
				}
				data, err := p.ReadSnapshotChunk(deadline(t), result.Generation, "tree/data", 0, func(ctx context.Context) error { return ctx.Err() })
				if err != nil || !bytes.Equal(data.Data, content[:SnapshotChunkBytes]) {
					t.Fatal("copy followed mutated caller slices", err)
				}
			case "read-generation":
				phases := []string{}
				result, err := p.CopySnapshot(deadline(t), snapshotTestIntent(plan), snapshotTestCallbacks(t, p, &phases))
				if err != nil {
					t.Fatal(err)
				}
				generation := result.Generation
				if len(generation.Files) != 2 || generation.Files[1].Output.Path != "tree/data" {
					t.Fatal("complete canonical fixture required")
				}
				changed := false
				data, err := p.ReadSnapshotChunk(deadline(t), generation, "tree/data", 0, func(ctx context.Context) error {
					if !changed {
						generation.Artifact.Identity.Inode++
						generation.Files[1].Output.Identity.Inode++
						generation.Files[1].Output.Path = "caller-mutated-output"
						generation.Files[1].Source.Chunks[0] = strings.Repeat("0", 64)
						changed = true
					}
					return ctx.Err()
				})
				if err != nil || !changed || !bytes.Equal(data.Data, content[:SnapshotChunkBytes]) || data.Native.validateClosed() != nil {
					t.Fatal("Current changed frozen read generation", data.Native, changed, err)
				}
			}
		})
	}
}
