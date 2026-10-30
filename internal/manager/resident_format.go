package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"getarm.org/internal/local"
	"getarm.org/internal/syncservice"
)

const residentManagerWorkspaceFormat = "4"
const residentManagerCoreSchema = `CREATE TABLE resident_worker_core_state (singleton INTEGER PRIMARY KEY CHECK(singleton=1), generation TEXT NOT NULL)`

// The canonical state.db DIRECTORY fence must already exist before this method
// or any SQL open. This transaction installs schema; marker4 is not that fence,
// an authorization grant, a native identity or a pool/borrower Closed witness.
func (o *ResidentManagerPending) installFreshOriginalSchema(ctx context.Context) error {
	db := o.manager.db
	for _, q := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA synchronous=FULL`, `PRAGMA busy_timeout=5000`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	if err := syncservice.VerifyDurableSQLite(ctx, db); err != nil {
		return err
	}
	o.phase("original_fresh_schema_transaction_begin_pending")
	tx, err := db.BeginTx(ctx, nil)
	o.mu.Lock()
	o.schemaTx = tx
	o.mu.Unlock()
	if err != nil {
		return err
	}
	// A failed original transaction stays retained. database/sql can roll back on
	// context cancellation; no such task/callback is represented as native Closed.
	// There is no retry, seed deletion, schema repair, old-ledger migration or
	// cleanup-close path in this constructor.
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("original fresh resident database already contains schema; preserve it")
	}
	for _, ddl := range residentManagerBaseSchema {
		if _, err = tx.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, residentManagerCoreSchema); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('host',?),('workspace_format','4')`, local.ID()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO resident_worker_core_state(singleton,generation) VALUES(1,?)`, o.original.Generation); err != nil {
		return err
	}
	o.phase("original_fresh_schema_commit_pending")
	o.mu.Lock()
	o.schemaCommitTried = true
	o.mu.Unlock()
	err = tx.Commit()
	o.mu.Lock()
	o.schemaCommitError = err
	o.mu.Unlock()
	return err
}

// Only Manager-owned transactions from its actual original pool call this
// private method. It is not a generic format4 acceptance API or caller-supplied
// reader adapter. Baseline database.CheckWorkspaceFormat remains unchanged and
// refuses BOTH format4 and every reserved resident namespace, including when a
// marker is removed/forged. The actual pending owner and binding stay private.
func (o *ResidentManagerPending) checkOriginalWorkspace(ctx context.Context, tx *sql.Tx) error {
	if o == nil || o.self != o || tx == nil {
		return errResidentManagerOriginal
	}
	o.mu.Lock()
	valid := o.manager != nil && o.manager.resident == o && o.binding != nil && o.database != nil && o.claim != nil && o.schemaCommitTried && o.schemaCommitError == nil && o.firstError == nil
	generation := o.original.Generation
	claim := o.claim
	o.mu.Unlock()
	if !valid || generation == "" {
		return errResidentManagerOriginal
	}
	// This actual native validation runs without the owner observation mutex.
	confirmed, err := claim.ValidateOriginal()
	if err != nil {
		return err
	}
	if confirmed.Generation != generation {
		return errResidentManagerOriginal
	}
	refuse := func() error {
		return fmt.Errorf("original combined resident workspace schema/marker differs; preserve it")
	}
	rows, err := tx.QueryContext(ctx, `SELECT typeof(key),substr(CAST(key AS BLOB),1,17),typeof(value),length(CAST(value AS BLOB)),substr(CAST(value AS BLOB),1,2) FROM metadata WHERE replace(replace(lower(CAST(key AS TEXT)),'ſ','s'),'K','k')='workspace_format' LIMIT 2`)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var kt, vt string
		var key, value []byte
		var size sql.NullInt64
		if err = rows.Scan(&kt, &key, &vt, &size, &value); err != nil {
			break
		}
		count++
		if count != 1 || kt != "text" || string(key) != "workspace_format" || vt != "text" || !size.Valid || size.Int64 != 1 || string(value) != residentManagerWorkspaceFormat {
			err = refuse()
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if count != 1 {
		return refuse()
	}
	rows, err = tx.QueryContext(ctx, `SELECT type,length(CAST(name AS BLOB)),substr(CAST(name AS BLOB),1,65),length(CAST(tbl_name AS BLOB)),substr(CAST(tbl_name AS BLOB),1,65),length(CAST(sql AS BLOB)),substr(CAST(sql AS BLOB),1,256) FROM sqlite_schema WHERE replace(replace(lower(name),'ſ','s'),'K','k') GLOB 'resident_worker*' OR replace(replace(lower(tbl_name),'ſ','s'),'K','k') GLOB 'resident_worker*' LIMIT 2`)
	if err != nil {
		return err
	}
	count = 0
	for rows.Next() {
		var kind string
		var name, table, ddl []byte
		var ns, ts, ds sql.NullInt64
		if err = rows.Scan(&kind, &ns, &name, &ts, &table, &ds, &ddl); err != nil {
			break
		}
		count++
		if count != 1 || kind != "table" || !ns.Valid || ns.Int64 != int64(len("resident_worker_core_state")) || string(name) != "resident_worker_core_state" || !ts.Valid || ts.Int64 != ns.Int64 || string(table) != string(name) || !ds.Valid || ds.Int64 != int64(len(residentManagerCoreSchema)) || string(ddl) != residentManagerCoreSchema {
			err = refuse()
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if count != 1 {
		return refuse()
	}
	rows, err = tx.QueryContext(ctx, `SELECT typeof(singleton),singleton,typeof(generation),length(CAST(generation AS BLOB)),substr(CAST(generation AS BLOB),1,33) FROM resident_worker_core_state LIMIT 2`)
	if err != nil {
		return err
	}
	count = 0
	for rows.Next() {
		var it, gt string
		var id, size sql.NullInt64
		var got []byte
		if err = rows.Scan(&it, &id, &gt, &size, &got); err != nil {
			break
		}
		count++
		if count != 1 || it != "integer" || !id.Valid || id.Int64 != 1 || gt != "text" || !size.Valid || size.Int64 != int64(len(generation)) || string(got) != generation {
			err = refuse()
			break
		}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	if count != 1 {
		return refuse()
	}
	// No metadata aliases, optional provider-preparation namespace or unreviewed
	// worker schema/lease extension is silently enabled by this combined format.
	for _, query := range []string{
		`SELECT EXISTS(SELECT 1 FROM metadata WHERE replace(replace(lower(CAST(key AS TEXT)),'ſ','s'),'K','k') GLOB 'resident_worker*' OR lower(CAST(key AS TEXT))='provider_preparation_schema')`,
		`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE lower(name) GLOB 'provider_preparation*' OR lower(tbl_name) GLOB 'provider_preparation*')`,
		`SELECT EXISTS(SELECT 1 FROM leases WHERE typeof(data)<>'text' OR NOT json_valid(data))`,
		`SELECT EXISTS(SELECT 1 FROM leases AS l JOIN json_each(CASE WHEN typeof(l.data)='text' AND json_valid(l.data) THEN l.data ELSE '{}' END) AS field LEFT JOIN json_each(CASE WHEN replace(lower(field.key),'ſ','s')='request' AND field.type='object' THEN field.value ELSE '{}' END) AS request WHERE lower(field.key)='provider_preparation' OR replace(replace(lower(field.key),'ſ','s'),'K','k')='resident_worker' OR (replace(lower(field.key),'ſ','s')='request' AND replace(lower(request.key),'K','k')='kind' AND request.type='text' AND request.atom IN ('provider-prepare-worker','resident-worker','resident-worker-prepare')))`,
	} {
		var present bool
		if err = tx.QueryRowContext(ctx, query).Scan(&present); err != nil {
			return err
		}
		if present {
			return refuse()
		}
	}
	var namedCount, canonicalCount int
	if err = tx.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(CASE WHEN typeof(key)='text' AND key='storage_pools' AND typeof(value)='text' AND length(CAST(value AS BLOB))<=32768 THEN CASE WHEN json_valid(value) THEN json_type(value)='array' ELSE 0 END ELSE 0 END),0) FROM metadata WHERE lower(CAST(key AS TEXT))='storage_pools'`).Scan(&namedCount, &canonicalCount); err != nil {
		return err
	}
	if namedCount > 1 || namedCount != canonicalCount {
		return refuse()
	}
	if namedCount == 0 {
		var named bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM leases AS l JOIN json_each(l.data) AS field LEFT JOIN json_each(CASE WHEN replace(lower(field.key),'ſ','s')='request' AND field.type='object' THEN field.value ELSE '{}' END) AS request WHERE replace(lower(field.key),'ſ','s')='storage_binding' OR (replace(lower(field.key),'ſ','s')='request' AND replace(lower(request.key),'ſ','s')='storage_pool' AND (request.type<>'text' OR request.atom<>'')))`).Scan(&named); err != nil {
			return err
		}
		if named {
			return refuse()
		}
	}
	return nil
}
