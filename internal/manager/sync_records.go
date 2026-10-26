package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/syncservice"
)

const syncLeaseBindingSchema = `CREATE TABLE sync_lease_bindings (lease TEXT PRIMARY KEY,key TEXT NOT NULL,side TEXT NOT NULL,volume TEXT NOT NULL,disk_bytes INTEGER NOT NULL)`

// initSyncRecords is a startup proof, never a lookup-time repair. The four
// tables, generation index, and marker appear in one durable transaction.
func (m *Manager) initSyncRecords(ctx context.Context) error {
	want := map[string]struct{ kind, table, ddl string }{
		"sync_operations":      {"table", "sync_operations", syncservice.OperationsSchema},
		"sync_open_generation": {"index", "sync_operations", syncservice.OpenGenerationSchema},
		"sync_effects":         {"table", "sync_effects", syncservice.EffectsSchema},
		"sync_baselines":       {"table", "sync_baselines", syncservice.BaselinesSchema},
		"sync_lease_bindings":  {"table", "sync_lease_bindings", syncLeaseBindingSchema},
	}
	auto := map[string]string{
		"sqlite_autoindex_sync_operations_1":     "sync_operations",
		"sqlite_autoindex_sync_effects_1":        "sync_effects",
		"sqlite_autoindex_sync_baselines_1":      "sync_baselines",
		"sqlite_autoindex_sync_lease_bindings_1": "sync_lease_bindings",
	}
	rows, err := m.db.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name GLOB 'sync_*' OR name GLOB 'sqlite_autoindex_sync_*' ORDER BY name LIMIT 11`)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var kind, name, table string
		var ddl sql.NullString
		if err = rows.Scan(&kind, &name, &table, &ddl); err != nil {
			break
		}
		if seen[name] {
			err = errors.New("duplicate sync schema object")
			break
		}
		if expected, ok := want[name]; ok {
			if kind != expected.kind || table != expected.table || !ddl.Valid || ddl.String != expected.ddl {
				err = errors.New("sync schema object differs")
				break
			}
		} else if expected, ok := auto[name]; ok {
			if kind != "index" || table != expected || ddl.Valid {
				err = errors.New("sync constraint index differs")
				break
			}
		} else {
			err = fmt.Errorf("unknown sync schema object %q", name)
			break
		}
		seen[name] = true
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	var marker, used string
	markerErr := m.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='sync_schema'`).Scan(&marker)
	usedErr := m.db.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='sync_used'`).Scan(&used)
	if usedErr != nil && !errors.Is(usedErr, sql.ErrNoRows) {
		return usedErr
	}
	if usedErr == nil && used != "1" {
		return model.Err("sync_evidence_unavailable", "unknown sync use marker")
	}
	if len(seen) == 0 && errors.Is(markerErr, sql.ErrNoRows) && errors.Is(usedErr, sql.ErrNoRows) {
		tx, e := m.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if e = syncservice.InstallSchemaTx(ctx, tx); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, syncLeaseBindingSchema); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('sync_schema','1')`); e != nil {
			return e
		}
		return tx.Commit()
	}
	if markerErr != nil || marker != "1" || len(seen) != len(want)+len(auto) {
		return model.Err("sync_evidence_unavailable", "sync schema namespace or marker is partial")
	}
	for name := range want {
		if !seen[name] {
			return model.Err("sync_evidence_unavailable", "sync schema table or index missing")
		}
	}
	for name := range auto {
		if !seen[name] {
			return model.Err("sync_evidence_unavailable", "sync constraint index missing")
		}
	}
	if usedErr == nil {
		var count int
		if e := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sync_operations`).Scan(&count); e != nil {
			return e
		}
		if count == 0 {
			return model.Err("sync_evidence_unavailable", "used sync namespace lost its operation evidence")
		}
	}
	return nil
}
