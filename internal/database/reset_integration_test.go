package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

func TestResetReviewIsReadOnlyAndRefusesActiveWork(t *testing.T) {
	for _, active := range []bool{false, true} {
		home := proofFixture(t, active)
		before := snapshotTree(t, home)
		r, err := database.ReviewReset(context.Background(), home, "read-only preview", false)
		if err != nil {
			t.Fatal(err)
		}
		if r.Plan.Proof.Settled == active {
			t.Fatalf("wrong obligation proof %+v", r.Plan.Proof)
		}
		if active {
			if _, err = r.Apply(context.Background()); err == nil {
				t.Fatal("active work erased")
			}
		}
		r.Close()
		if after := snapshotTree(t, home); !reflect.DeepEqual(before, after) {
			t.Fatal("preview/refusal changed user-state bytes/modes/names")
		}
	}
}
func TestResetActualLedgerFreshAuthorityPreservesStorageChildren(t *testing.T) {
	home := proofFixture(t, false)
	m, err := manager.Open(home, model.Resources{CPU: 2, Memory: 4096, Disk: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	storage, err := m.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1024}, TTL: time.Minute, Reason: "retained artifact fixture", Key: "old-storage"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(storage.ManagedDir, "keep.txt")
	if err = os.WriteFile(sentinel, []byte("operator artifact remains"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.StorageHandoff(storage.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := database.ReviewReset(context.Background(), home, "discard only settled private test ledger", false)
	if err != nil {
		t.Fatal(err)
	}
	if r.Plan.StorageRoot == nil {
		t.Fatal("missing verified infrastructure identity")
	}
	oldHost, newHost := r.Plan.OldHost, r.Plan.NewHost
	result, err := r.Apply(context.Background())
	r.Close()
	if err != nil || !result.Completed {
		t.Fatalf("reset %+v %v", result, err)
	}
	if _, err = os.Lstat(filepath.Join(home, "state.db")); !os.IsNotExist(err) {
		t.Fatal("old DB not erased", err)
	}
	lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = database.InstallResetSeed(context.Background(), home, lock); err != nil {
		t.Fatal(err)
	}
	m, err = manager.Open(home, model.Resources{CPU: 2, Memory: 4096, Disk: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	status, err := m.Status()
	if err != nil || status.Host != newHost || status.Host == oldHost {
		t.Fatalf("authority %+v %v", status, err)
	}
	if len(status.Leases) != 0 {
		t.Fatal("old lease/idempotency namespace was adopted")
	}
	fresh, err := m.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1024}, TTL: time.Minute, Reason: "new exclusive allocation", Key: "old-storage"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ManagedDir == storage.ManagedDir || fresh.ID == storage.ID {
		t.Fatal("old artifact path or old keyed lease adopted")
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "operator artifact remains" {
		t.Fatal("old artifact changed", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var oldLeaseCount int
	if err = db.QueryRow(`SELECT count(*) FROM leases WHERE id=?`, storage.ID).Scan(&oldLeaseCount); err != nil || oldLeaseCount != 0 {
		t.Fatal("old ledger metadata retained", err)
	}
	if err = local.CheckStateFence(home); err != nil {
		t.Fatal(err)
	}
}
