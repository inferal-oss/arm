package manager

import (
	"context"
	"errors"
	"time"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/model"
)

// DatabaseMaintenance reserves a separate read-only SQLite connection. It does
// not hold the lifecycle mutex or borrow the writer pool during inspection.
func (m *Manager) DatabaseMaintenance(ctx context.Context, operation, destination string, full bool) (result any, err error) {
	limit := database.QuickTimeout
	switch operation {
	case "stats":
		if full || destination != "" {
			return nil, model.Err("invalid_request", "stats accepts no destination or full check")
		}
	case "check":
		if destination != "" {
			return nil, model.Err("invalid_request", "check accepts no destination")
		}
		if full {
			limit = database.FullTimeout
		}
	case "backup":
		if full || destination == "" || len(destination) > 4096 {
			return nil, model.Err("invalid_request", "backup requires one bounded absolute destination")
		}
		limit = database.BackupTimeout
	default:
		return nil, model.Err("invalid_request", "unknown database maintenance operation")
	}
	return m.withDatabaseReader(ctx, limit, operation, func(ctx context.Context, source *database.Source) (any, error) {
		return inspectDatabase(ctx, source, operation, destination, full)
	})
}
func prepareDatabaseFile(home string) error { return database.PrepareNewFile(home) }
func (m *Manager) withDatabaseReader(ctx context.Context, limit time.Duration, operation string, read func(context.Context, *database.Source) (any, error)) (result any, err error) {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, model.Err("backend_stopping", "database maintenance is unavailable during shutdown")
	}
	if m.databaseSlot == nil {
		m.databaseSlot = make(chan struct{}, 1)
	}
	select {
	case m.databaseSlot <- struct{}{}:
	default:
		m.mu.Unlock()
		return nil, model.Err("database_busy", "another database inspection/backup is active; retry explicitly")
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	defer func() { <-m.databaseSlot }()
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-m.stop:
			cancel()
		case <-done:
		}
	}()
	defer func() { err = database.OperationError(ctx, operation, err) }()
	source, err := database.Live(ctx, m.home)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	var host string
	if err = source.DB.QueryRowContext(ctx, "SELECT substr(value,1,33) FROM metadata WHERE key='host'").Scan(&host); err != nil {
		return nil, err
	}
	if host != m.host {
		return nil, model.Err("database_authority_changed", "database file no longer belongs to the running authority; maintenance refused")
	}
	return read(ctx, source)
}
func inspectDatabase(ctx context.Context, source *database.Source, operation, destination string, full bool) (any, error) {
	switch operation {
	case "stats":
		return database.ReadStats(ctx, source)
	case "check":
		return database.CheckDatabase(ctx, source, full)
	case "backup":
		return database.BackupDatabase(ctx, source, destination)
	}
	return nil, model.Err("invalid_request", "unknown database operation")
}
