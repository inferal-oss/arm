package database_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/model"
	"golang.org/x/sys/unix"
)

func TestReviewForgetFreshAuthorityCannotAdoptPreservedStorage(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	capacity := model.Resources{Disk: 64 << 20}
	m, err := manager.Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	old, err := m.Acquire(model.Request{Key: "review-original-storage", Kind: "storage", Reason: "original disposable storage obligation", TTL: time.Hour, Resources: model.Resources{Disk: 1 << 20}}, nil)
	if err != nil || old.State != "active" || old.ManagedDir == "" {
		m.Close()
		t.Fatal("original storage fixture did not acquire native ownership", old, err)
	}
	marker := []byte("preserved artifact remains outside ledger forgetting")
	path := filepath.Join(old.ManagedDir, "artifact-sentinel")
	if err := os.WriteFile(path, marker, 0600); err != nil {
		m.Close()
		t.Fatal(err)
	}
	status, err := m.Status()
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	storageBefore := snapshotTree(t, filepath.Join(home, "storage"))
	review, err := database.ReviewForget(t.Context(), home, "explicitly forget the ledger while retaining native artifacts", false)
	if err != nil {
		t.Fatal(err)
	}
	if review.Plan.StorageRoot != nil || review.Plan.Proof.Complete || review.Plan.Proof.Settled {
		review.Close()
		t.Fatal("forget borrowed old cleanup or settlement authority")
	}
	result, err := review.Apply(t.Context())
	if closeErr := review.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || !result.Completed {
		t.Fatal(result, err)
	}
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
			t.Error(err)
		}
	}()
	if err := database.InstallResetSeed(t.Context(), home, lock); err != nil {
		t.Fatal(err)
	}
	fresh, err := manager.Open(home, capacity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fresh.Close(); err != nil {
			t.Error(err)
		}
	}()
	current, err := fresh.Status()
	if err != nil || current.Host != result.NewHost || current.Host == status.Host {
		t.Fatal("forget did not install the exact independent fresh authority", current.Host, result.NewHost, err)
	}
	if _, err := fresh.Inspect(old.ID); err == nil {
		t.Fatal("fresh authority inherited an abandoned original lease")
	}
	adopted, err := fresh.Acquire(model.Request{Key: "review-fresh-storage", Kind: "storage", Reason: "must not adopt the preserved old root", TTL: time.Hour, Resources: model.Resources{Disk: 1 << 20}}, nil)
	if err != nil || adopted.State != "needs_attention" || !strings.Contains(adopted.Failure, "file exists") || adopted.ManagedDir != "" || adopted.Artifact != nil {
		t.Fatal("fresh storage did not refuse the preserved root at native creation", adopted, err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, marker) {
		t.Fatal("forget or fresh startup changed the preserved artifact", err)
	}
	if after := snapshotTree(t, filepath.Join(home, "storage")); !reflect.DeepEqual(storageBefore, after) {
		t.Fatal("fresh authority changed preserved storage names, bytes or modes")
	}
}
