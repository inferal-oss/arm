package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"getarm.org/internal/database"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

// This gate concerns schema/pool evidence and reset authority, not live CPU
// availability. Every fixture still obtains its real counted pool claim using
// the normal admission API, without altering host observations or resource floors.
func TestWorkspaceReviewSchemaTwoResetLifecycle(t *testing.T) {
	t.Run("active-counted-pool-refuses-readonly", func(t *testing.T) {
		home := proofFixtureResources(t, true, model.Resources{})
		before := snapshotTree(t, home)
		review, err := database.ReviewReset(t.Context(), home, "active schema two fixture", false)
		if err != nil {
			t.Fatal(err)
		}
		closed := false
		defer func() {
			if !closed {
				_ = review.Close()
			}
		}()
		if review.Plan.Proof.Settled {
			t.Fatal("active counted pool claim appeared settled")
		}
		if _, err := review.Apply(t.Context()); err == nil {
			t.Fatal("active pool evidence erased")
		}
		if err := review.Close(); err != nil {
			t.Fatal(err)
		}
		closed = true
		if after := snapshotTree(t, home); !reflect.DeepEqual(before, after) {
			t.Fatal("read-only refusal changed original state")
		}
	})
	t.Run("settled-reset-seed-and-retained-artifact", func(t *testing.T) {
		home := proofFixtureResources(t, false, model.Resources{})
		capacity := model.Resources{CPU: 2, Memory: 4096, Disk: 1 << 20}
		m, err := manager.Open(home, capacity)
		if err != nil {
			t.Fatal(err)
		}
		closed := false
		t.Cleanup(func() {
			if !closed {
				m.Close()
			}
		})
		old, err := m.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1024}, TTL: time.Minute, Reason: "retained fixture artifact", Key: "workspace-reset-storage"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(old.ManagedDir, "retained")
		if err := os.WriteFile(sentinel, []byte("private retained artifact"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.StorageHandoff(old.ID); err != nil {
			t.Fatal(err)
		}
		if err := m.Close(); err != nil {
			t.Fatal(err)
		}
		closed = true
		review, err := database.ReviewReset(context.Background(), home, "settled schema two fixture", false)
		if err != nil {
			t.Fatal(err)
		}
		if !review.Plan.Proof.Settled || review.Plan.Proof.Tables["sync_workspaces"] != 0 {
			review.Close()
			t.Fatal("fresh workspace schema did not prove empty")
		}
		oldHost, newHost := review.Plan.OldHost, review.Plan.NewHost
		result, err := review.Apply(t.Context())
		closeErr := review.Close()
		if err != nil || closeErr != nil || !result.Completed {
			t.Fatal("settled reset failed", result, err, closeErr)
		}
		lock, err := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		if err := database.InstallResetSeed(t.Context(), home, lock); err != nil {
			t.Fatal(err)
		}
		fresh, err := manager.Open(home, capacity)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		status, err := fresh.Status()
		if err != nil || status.Host != newHost || status.Host == oldHost || len(status.Leases) != 0 {
			t.Fatal("old authority or leases adopted", err)
		}
		replacement, err := fresh.Acquire(model.Request{Kind: "storage", Resources: model.Resources{Disk: 1024}, TTL: time.Minute, Reason: "fresh fixture allocation", Key: "workspace-reset-storage"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if replacement.ID == old.ID || replacement.ManagedDir == old.ManagedDir {
			t.Fatal("retained artifact adopted under fresh authority")
		}
		if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "private retained artifact" {
			t.Fatal("reset changed retained artifact", err)
		}
		db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var marker string
		var locators, oldLeases int
		if err := db.QueryRow(`SELECT value FROM metadata WHERE key='sync_schema'`).Scan(&marker); err != nil || marker != "2" {
			t.Fatal("fresh schema version differs", marker, err)
		}
		if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM sync_workspaces),(SELECT COUNT(*) FROM leases WHERE id=?)`, old.ID).Scan(&locators, &oldLeases); err != nil || locators != 0 || oldLeases != 0 {
			t.Fatal("old ledger evidence adopted", locators, oldLeases, err)
		}
	})
}
