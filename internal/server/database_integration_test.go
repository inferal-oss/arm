package server_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/server"
)

func TestNativeDatabaseMaintenanceUsesSeparateReader(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "state")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("database fixture backend did not stop")
		}
	})
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err = os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var status model.Status
	if err = client.Call(t.Context(), home, "status", nil, &status); err != nil || !slices.Contains(status.Capabilities, "database-inspection-backup") {
		t.Fatal(status, err)
	}
	var stats database.Stats
	if err = client.Call(t.Context(), home, "database_stats", nil, &stats); err != nil || stats.Host != status.Host || stats.Scope == "stopped consistent copy" {
		t.Fatal(stats, err)
	}
	var check database.Check
	if err = client.Call(t.Context(), home, "database_check", map[string]bool{"full": true}, &check); err != nil || !check.OK || check.ForeignKeys != "none declared in schema" {
		t.Fatal(check, err)
	}
	var backup database.Backup
	if err = client.Call(t.Context(), home, "database_backup", map[string]string{"destination": filepath.Join(home, "snapshot.db")}, &backup); err != nil || backup.Host != status.Host || backup.Size == 0 {
		t.Fatal(backup, err)
	}
	if err = client.Call(t.Context(), home, "database_stats", map[string]bool{"full": true}, nil); err == nil {
		t.Fatal("invalid native params accepted")
	}
}
