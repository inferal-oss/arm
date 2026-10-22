package manager

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/model"
)

func maintenanceManager(t *testing.T) (*Manager, func()) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := Open(filepath.Join(base, "arm"), model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	close := func() {
		once.Do(func() {
			if err := m.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(close)
	return m, close
}
func TestDatabaseReaderDoesNotBlockExpiryOrWriterPool(t *testing.T) {
	m, _ := maintenanceManager(t)
	m.Start()
	if m.db.Stats().MaxOpenConnections != 1 {
		t.Fatal("writer pool changed")
	}
	lease, err := m.Acquire(model.Request{Resources: model.Resources{CPU: 1}, Reason: "expiry while maintenance is held", TTL: 200 * time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := m.withDatabaseReader(context.Background(), time.Second*5, "stats", func(ctx context.Context, s *database.Source) (any, error) {
			tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
			var n int
			if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM leases").Scan(&n); err != nil {
				return nil, err
			}
			close(started)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return n, nil
			}
		})
		finished <- err
	}()
	select {
	case <-started:
	case err := <-finished:
		t.Fatal("reader failed before starting", err)
	case <-time.After(3 * time.Second):
		t.Fatal("real SQLite reader did not start")
	}
	if _, err = m.DatabaseMaintenance(context.Background(), "stats", "", false); errorCode(err) != "database_busy" {
		t.Fatal("second maintenance not bounded", err)
	}
	eventually(t, func() bool { l, err := m.Inspect(lease.ID); return err == nil && l.State == "terminal" })
	if m.db.Stats().MaxOpenConnections != 1 {
		t.Fatal("maintenance broadened writer pool")
	}
	close(release)
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	result, err := m.DatabaseMaintenance(context.Background(), "stats", "", false)
	if err != nil || result.(database.Stats).Tables["leases"] != 1 {
		t.Fatal(result, err)
	}
}
func TestDatabaseReaderShutdownCancelsAndJoins(t *testing.T) {
	m, closeManager := maintenanceManager(t)
	started, finished := make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := m.withDatabaseReader(context.Background(), time.Minute, "check", func(ctx context.Context, s *database.Source) (any, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		finished <- err
	}()
	select {
	case <-started:
	case err := <-finished:
		t.Fatal("reader failed before starting", err)
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not start")
	}
	closeManager()
	select {
	case err := <-finished:
		var detail *model.Error
		if !errors.As(err, &detail) || detail.Code != "database_interrupted" {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("reader escaped manager lifetime")
	}
	if _, err := m.DatabaseMaintenance(context.Background(), "stats", "", false); errorCode(err) != "backend_stopping" {
		t.Fatal(err)
	}
}
func TestNewDatabasePrivateWithoutRepairingExistingMode(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(base, "arm")
	m, err := Open(home, model.Resources{CPU: 1, Disk: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	info, err := os.Stat(filepath.Join(home, "state.db"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	os.Chmod(filepath.Join(home, "state.db"), 0644)
	m, err = Open(home, model.Resources{CPU: 1, Disk: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	info, _ = os.Stat(filepath.Join(home, "state.db"))
	if info.Mode().Perm() != 0644 {
		t.Fatal("existing permissions repaired")
	}
}
