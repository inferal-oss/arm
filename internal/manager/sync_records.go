package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"getarm.org/internal/model"
	"getarm.org/internal/syncreconcile"
	"getarm.org/internal/syncservice"
)

const syncLeaseBindingSchema = `CREATE TABLE sync_lease_bindings (lease TEXT PRIMARY KEY,key TEXT NOT NULL,side TEXT NOT NULL,volume TEXT NOT NULL,disk_bytes INTEGER NOT NULL)`

// initSyncRecords is a startup proof, never a migration or lookup-time repair.
// The complete tables, indexes and version marker appear in one durable transaction.
func (m *Manager) initSyncRecords(ctx context.Context) error {
	want := map[string]struct{ kind, table, ddl string }{
		"sync_operations":      {"table", "sync_operations", syncservice.OperationsSchema},
		"sync_open_generation": {"index", "sync_operations", syncservice.OpenGenerationSchema},
		"sync_effects":         {"table", "sync_effects", syncservice.EffectsSchema},
		"sync_baselines":       {"table", "sync_baselines", syncservice.BaselinesSchema},
		"sync_workspaces":      {"table", "sync_workspaces", syncservice.WorkspacesSchema},
		"sync_lease_bindings":  {"table", "sync_lease_bindings", syncLeaseBindingSchema},
	}
	auto := map[string]string{
		"sqlite_autoindex_sync_operations_1":     "sync_operations",
		"sqlite_autoindex_sync_effects_1":        "sync_effects",
		"sqlite_autoindex_sync_baselines_1":      "sync_baselines",
		"sqlite_autoindex_sync_lease_bindings_1": "sync_lease_bindings",
		"sqlite_autoindex_sync_workspaces_1":     "sync_workspaces",
		"sqlite_autoindex_sync_workspaces_2":     "sync_workspaces",
	}
	if m.syncSchemaVersion == "3" {
		want["sync_charged_scopes"] = struct{ kind, table, ddl string }{"table", "sync_charged_scopes", syncservice.ChargedScopeSchema}
		want["sync_charged_scopes_preparation"] = struct{ kind, table, ddl string }{"index", "sync_charged_scopes", syncservice.ChargedScopePreparationIndex}
		want["sync_charged_scopes_project"] = struct{ kind, table, ddl string }{"index", "sync_charged_scopes", syncservice.ChargedScopeProjectIndex}
		want["sync_charged_scopes_final"] = struct{ kind, table, ddl string }{"index", "sync_charged_scopes", syncservice.ChargedScopeFinalIndex}
		auto["sqlite_autoindex_sync_charged_scopes_1"] = "sync_charged_scopes"
	} else if m.syncSchemaVersion != "2" {
		return model.Err("sync_evidence_unavailable", "unknown sync schema mode")
	}
	rows, err := m.db.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_schema WHERE name GLOB 'sync_*' OR name GLOB 'sqlite_autoindex_sync_*' ORDER BY name LIMIT 19`)
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
		if m.syncSchemaVersion == "3" {
			for _, ddl := range []string{syncservice.ChargedScopeSchema, syncservice.ChargedScopePreparationIndex, syncservice.ChargedScopeProjectIndex, syncservice.ChargedScopeFinalIndex} {
				if _, e = tx.ExecContext(ctx, ddl); e != nil {
					return e
				}
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('sync_schema',?)`, m.syncSchemaVersion); e != nil {
			return e
		}
		return tx.Commit()
	}
	if markerErr == nil && marker == "1" {
		return model.Err("sync_evidence_unavailable", "sync schema version 1 is incompatible with workspace ledger version 2; no migration or repair was attempted; preserve the state directory for read-only diagnosis, backup, or explicit database remove --forget review")
	}
	if markerErr != nil || marker != m.syncSchemaVersion || len(seen) != len(want)+len(auto) {
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
	if m.syncSchemaVersion == "3" {
		return m.verifyChargedSyncRecords(ctx, usedErr == nil)
	}
	return m.verifySyncRecords(ctx, usedErr == nil)
}

// Every admitted operation must retain exactly one charged lease binding.
// Historical effects remain lookup-only; startup never erases or repairs them.
func (m *Manager) verifySyncRecords(ctx context.Context, used bool) error {
	var operations, bindings, effects, baselines, workspaces, orphans, looseEffects, looseWorkspaces int
	if err := m.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM sync_operations),(SELECT COUNT(*) FROM sync_lease_bindings),(SELECT COUNT(*) FROM sync_effects),(SELECT COUNT(*) FROM sync_baselines),(SELECT COUNT(*) FROM sync_workspaces),(SELECT COUNT(*) FROM sync_lease_bindings b LEFT JOIN sync_operations o ON o.key=b.key AND o.side=b.side WHERE o.key IS NULL),(SELECT COUNT(*) FROM sync_effects e LEFT JOIN sync_operations o ON o.key=e.key AND o.side=e.owner_side WHERE o.key IS NULL),(SELECT COUNT(*) FROM sync_workspaces w LEFT JOIN sync_operations o ON o.key=w.key AND o.side=w.side WHERE o.key IS NULL)`).Scan(&operations, &bindings, &effects, &baselines, &workspaces, &orphans, &looseEffects, &looseWorkspaces); err != nil {
		return err
	}
	if operations > 4096 || effects > syncreconcile.MaxActions*2 || baselines > 4096 || workspaces > operations*3 || operations != bindings || orphans != 0 || looseEffects != 0 || looseWorkspaces != 0 || (operations > 0) != used || (!used && (effects != 0 || baselines != 0 || workspaces != 0)) {
		return model.Err("sync_evidence_unavailable", "sync operation/lease binding or use marker differs")
	}
	rows, err := m.db.QueryContext(ctx, `SELECT b.lease,b.key,b.side,b.volume,b.disk_bytes FROM sync_lease_bindings b ORDER BY b.lease LIMIT 4097`)
	if err != nil {
		return err
	}
	type binding struct {
		lease, key, side, volume string
		disk                     int64
	}
	var records []binding
	for rows.Next() {
		var b binding
		if err := rows.Scan(&b.lease, &b.key, &b.side, &b.volume, &b.disk); err != nil {
			rows.Close()
			return err
		}
		records = append(records, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, b := range records {
		if b.side != string(syncreconcile.Local) && b.side != string(syncreconcile.Remote) || b.volume == "" || b.disk <= 0 {
			return model.Err("sync_evidence_unavailable", "sync lease binding has invalid shape")
		}
		l, err := m.get(b.lease)
		if err != nil || l.State == "terminal" || l.State == "queued" || l.Request.Resources.Disk != b.disk || l.Request.OriginKey != b.key {
			return model.Err("sync_evidence_unavailable", "sync lease binding lost its charged obligation")
		}
		lookup, err := (&syncservice.Ledger{DB: m.db, Key: b.key, Side: syncreconcile.Side(b.side)}).Lookup(ctx)
		if err != nil {
			return model.Err("sync_evidence_unavailable", "sync operation evidence cannot be verified: "+err.Error())
		}
		want := lookup.Intent.LocalResources
		if b.side == string(syncreconcile.Remote) {
			want = lookup.Intent.RemoteResources
		}
		if l.Request.Resources != want {
			return model.Err("sync_evidence_unavailable", "sync lease resources differ from retained intent")
		}
		if _, err := (&syncservice.Ledger{DB: m.db, Key: b.key, Side: syncreconcile.Side(b.side)}).Workspaces(ctx); err != nil {
			return model.Err("sync_evidence_unavailable", "sync workspace locators cannot be verified: "+err.Error())
		}
	}
	return nil
}
