package database_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/sshservice"
)

func TestSSHResetArchivesCompleteNamespaceAndStartsFreshAuthority(t *testing.T) {
	ctx := context.Background()
	// This exercises only durable namespace/reset metadata, with no native SSH
	// execution. Use the fixture's counted permit instead of an incidental CPU
	// reservation whose availability depends on unrelated host workloads.
	home, binding, pin := sshProofStateInHome(t, proofFixtureResources(t, false, model.Resources{}))
	intent := sshProofIntent(t, home, binding, pin)
	before := snapshotTree(t, home)
	pending, err := database.ReviewReset(ctx, home, "refuse pending SSH even with retirement", true)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Plan.Proof.Settled {
		t.Fatal("pending source intent declared settled")
	}
	_, err = pending.Apply(ctx)
	pending.Close()
	if err == nil || !reflect.DeepEqual(before, snapshotTree(t, home)) {
		t.Fatal("pending reset was accepted or changed evidence", err)
	}
	if err = os.Remove(intent.Directory.Path); err != nil {
		t.Fatal(err)
	}
	if err = sshservice.SettleSource(ctx, home, binding, intent, proofSSHReceipt(intent)); err != nil {
		t.Fatal(err)
	}

	directory := filepath.Join(home, "mesh")
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{}
	identities := map[string]os.FileInfo{}
	for _, entry := range entries {
		name := entry.Name()
		contents[name], err = os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		identities[name], err = os.Lstat(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	review, err := database.ReviewReset(ctx, home, "archive settled SSH identity and exact receipts", true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := review.Apply(ctx)
	review.Close()
	if err != nil || !result.Completed || result.OldHost != binding.Host || result.NewHost == binding.Host {
		t.Fatalf("reset result %+v: %v", result, err)
	}
	archive := filepath.Join(filepath.Dir(result.Receipt), "mesh")
	archivedInfo, err := os.Lstat(archive)
	if err != nil || !os.SameFile(directoryInfo, archivedInfo) {
		t.Fatal("reset did not retain the exact mesh directory", err)
	}
	archivedEntries, err := os.ReadDir(archive)
	if err != nil || len(archivedEntries) != len(entries) {
		t.Fatal("archive membership changed", err)
	}
	for _, entry := range archivedEntries {
		name := entry.Name()
		data, readErr := os.ReadFile(filepath.Join(archive, name))
		info, statErr := os.Lstat(filepath.Join(archive, name))
		original, ok := identities[name]
		if !ok || readErr != nil || statErr != nil || !bytes.Equal(contents[name], data) || !os.SameFile(original, info) || info.Mode() != original.Mode() {
			t.Fatal("archive changed original identity, mode or bytes", name, readErr, statErr)
		}
	}
	if _, err = os.Lstat(directory); !os.IsNotExist(err) {
		t.Fatal("retired SSH namespace remains active", err)
	}

	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = database.InstallResetSeed(ctx, home, lock); err != nil {
		t.Fatal(err)
	}
	m, err := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	status, err := m.Status()
	if err != nil || status.Host != result.NewHost || len(status.Leases) != 0 {
		t.Fatal("fresh manager inherited old authority or leases", err)
	}
	store, err := mesh.Open(home)
	if store != nil {
		store.Close()
	}
	if !os.IsNotExist(err) {
		t.Fatal("fresh authority implicitly inherited mesh/SSH keys", err)
	}
}
