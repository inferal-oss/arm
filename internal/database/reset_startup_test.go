package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/server"
)

func TestResetHooksPreserveOrdinaryFreshAndEstablishedStartup(t *testing.T) {
	// Intentionally retain t.TempDir's normal macOS /var parent alias. This is
	// ordinary startup, not permission to delete through an aliased namespace.
	home := filepath.Join(t.TempDir(), "state")
	var host string
	for i := 0; i < 2; i++ {
		m, err := manager.Open(home, model.Resources{CPU: 2, Memory: 4096, Disk: 4096})
		if err != nil {
			t.Fatal(err)
		}
		status, err := m.Status()
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			host = status.Host
		} else if host != status.Host {
			t.Fatal("ordinary restart regenerated authority")
		}
		if err = m.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestResetHooksEstablishedMissingAuthorityRefusesBeforeMigration(t *testing.T) {
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before := snapshotTree(t, home)
	if m, err := manager.Open(home, model.Resources{CPU: 2}); err == nil {
		m.Close()
		t.Fatal("established missing authority silently regenerated")
	}
	if after := snapshotTree(t, home); !reflect.DeepEqual(before, after) {
		t.Fatal("refused authority was migrated or repaired")
	}
}
func TestResetHooksActualServeFreshAndEstablishedRestart(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	var authority string
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 2, Memory: 4096, Disk: 4096})
		}()
		deadline := time.Now().Add(5 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			var status model.Status
			probe, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
			err := client.Call(probe, home, "status", map[string]any{}, &status)
			stop()
			if err == nil {
				if i == 0 {
					authority = status.Host
				} else if status.Host != authority {
					t.Fatal("server restart changed ordinary authority")
				}
				ready = true
				break
			}
			select {
			case err := <-done:
				cancel()
				t.Fatalf("server startup failed: %v", err)
			default:
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("server shutdown did not finish")
		}
		if !ready {
			t.Fatal("real backend failed to become available")
		}
		if _, err := local.ReadEndpoint(home); err == nil {
			t.Fatal("test backend endpoint retained after exit")
		}
	}
}
