package manager

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// This cut admits the exact original PRE-WORKER shape only. Actual controller
// transitions and their nonempty row/lease/native bindings must extend this
// validator before any initial worker activation/public ingress is introduced.
// No runtime controller/readiness is inferred from these fresh rows.
func (o *ResidentWorkerManagerPending) checkOriginalWorkspace(ctx context.Context, tx *sql.Tx) (resultErr error) {
	if o == nil || o.self != o || ctx == nil || tx == nil {
		return errResidentWorkerManagerOriginal
	}
	o.mu.Lock()
	valid := o.manager != nil && o.manager.residentWorker == o && o.manager.resident == nil && o.manager.residentService == nil && o.database != nil && o.binding != nil && o.claim != nil && o.schemaCommitTried && o.schemaCommitError == nil && o.firstError == nil
	original := o.original
	o.mu.Unlock()
	if !valid {
		return errResidentWorkerManagerOriginal
	}
	o.mu.Lock()
	index := -1
	if o.active != nil && o.active.owner == o {
		switch o.active.kind {
		case "open":
			index = 0
		case "validate_prepared":
			index = 1
		}
	}
	if index < 0 || o.freshFormatCalls[index] != nil {
		o.mu.Unlock()
		return errResidentWorkerManagerOriginal
	}
	c := &residentWorkerFreshFormatCall{owner: o, op: o.active, ctx: o.rootContext, tx: tx}
	o.freshFormatCalls[index] = c
	o.mu.Unlock()
	normal := false
	defer func() { o.mu.Lock(); c.normalReturned = normal; c.resultError = resultErr; o.mu.Unlock() }()
	c.nativeAttempted = true
	c.native, c.nativeError = o.claim.ValidateOriginalContext(c.ctx)
	c.nativeReturned = true
	resultErr = c.nativeError
	if resultErr == nil && c.native != original {
		resultErr = errResidentWorkerManagerOriginal
	}
	if resultErr == nil {
		resultErr = readWorkerFreshWorkspace(c, original.Generation)
	}
	normal = true
	return resultErr
}
func readWorkerFreshWorkspace(c *residentWorkerFreshFormatCall, generation string) error {
	if c == nil || c.ctx == nil || c.tx == nil {
		return errResidentWorkerManagerOriginal
	}
	ctx := c.ctx
	refuse := func() error { return fmt.Errorf("fresh worker schema/marker/original generation differs; preserve it") }
	if len(generation) != 32 || strings.ToLower(generation) != generation {
		return refuse()
	}
	if _, err := hex.DecodeString(generation); err != nil {
		return refuse()
	}
	q, err := c.query(0, `SELECT typeof(key),length(CAST(key AS BLOB)),substr(CAST(key AS BLOB),1,17),typeof(value),length(CAST(value AS BLOB)),substr(CAST(value AS BLOB),1,2) FROM metadata WHERE replace(replace(lower(CAST(key AS TEXT)),'ſ','s'),'K','k')='workspace_format' LIMIT 2`)
	if err != nil {
		return err
	}
	count := 0
	for q.next() {
		var kt, vt string
		var kl, vl sql.NullInt64
		var key, value []byte
		if err = q.scan(&kt, &kl, &key, &vt, &vl, &value); err != nil {
			break
		}
		count++
		if count != 1 || kt != "text" || !kl.Valid || kl.Int64 != 16 || string(key) != "workspace_format" || vt != "text" || !vl.Valid || vl.Int64 != 1 || string(value) != "5" {
			err = refuse()
			break
		}
	}
	err = q.finish(err)
	if err != nil {
		return err
	}
	if count != 1 {
		return refuse()
	}
	q, err = c.query(1, `SELECT substr(type,1,8),typeof(name),length(CAST(name AS BLOB)),substr(CAST(name AS BLOB),1,129),typeof(tbl_name),length(CAST(tbl_name AS BLOB)),substr(CAST(tbl_name AS BLOB),1,129),typeof(sql),length(CAST(sql AS BLOB)),substr(CAST(sql AS BLOB),1,4097) FROM sqlite_schema WHERE replace(replace(lower(name),'ſ','s'),'K','k') GLOB 'resident_worker*' OR replace(replace(lower(tbl_name),'ſ','s'),'K','k') GLOB 'resident_worker*' LIMIT 13`)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for q.next() {
		var kind, nt, tt, st string
		var ns, ts, ss sql.NullInt64
		var name, table, ddl []byte
		if err = q.scan(&kind, &nt, &ns, &name, &tt, &ts, &table, &st, &ss, &ddl); err != nil {
			break
		}
		expected, ok := workerReservedObjectsData[string(name)]
		if !ok || seen[string(name)] || nt != "text" || tt != "text" || !ns.Valid || ns.Int64 != int64(len(name)) || !ts.Valid || ts.Int64 != int64(len(table)) || kind != expected.kind || string(table) != expected.table {
			err = refuse()
			break
		}
		if expected.nullSQL {
			if st != "null" || ss.Valid || ddl != nil {
				err = refuse()
				break
			}
		} else if st != "text" || !ss.Valid || ss.Int64 > 4096 || ss.Int64 != int64(len(ddl)) || string(ddl) != expected.sql {
			err = refuse()
			break
		}
		seen[string(name)] = true
	}
	err = q.finish(err)
	if err != nil {
		return err
	}
	if len(seen) != 12 {
		return refuse()
	}
	q, err = c.query(2, `SELECT typeof(singleton),singleton,typeof(generation),length(CAST(generation AS BLOB)),substr(CAST(generation AS BLOB),1,33),typeof(revision),revision,typeof(phase),length(CAST(phase AS BLOB)),substr(CAST(phase AS BLOB),1,11),typeof(active_epoch),typeof(activation_slot) FROM resident_worker_core_state LIMIT 2`)
	if err != nil {
		return err
	}
	count = 0
	for q.next() {
		var it, gt, rt, pt, at, ast string
		var id, gs, rev, ps sql.NullInt64
		var gen, phase []byte
		if err = q.scan(&it, &id, &gt, &gs, &gen, &rt, &rev, &pt, &ps, &phase, &at, &ast); err != nil {
			break
		}
		count++
		if count != 1 || it != "integer" || !id.Valid || id.Int64 != 1 || gt != "text" || !gs.Valid || gs.Int64 != 32 || string(gen) != generation || rt != "integer" || !rev.Valid || rev.Int64 != 0 || pt != "text" || !ps.Valid || ps.Int64 != 9 || string(phase) != "preparing" || at != "null" || ast != "null" {
			err = refuse()
			break
		}
	}
	err = q.finish(err)
	if err != nil {
		return err
	}
	if count != 1 {
		return refuse()
	}
	for i, table := range []string{"resident_worker_packages", "resident_worker_operations", "resident_worker_attempts", "resident_worker_activations", "resident_worker_asset_sessions"} {
		q, err := c.query(3+i, `SELECT EXISTS(SELECT 1 FROM `+table+` LIMIT 1)`)
		if err != nil {
			return err
		}
		count := 0
		present := false
		for q.next() {
			count++
			if count != 1 {
				err = refuse()
				break
			}
			if err = q.scan(&present); err != nil {
				break
			}
		}
		if err = q.finish(err); err != nil {
			return err
		}
		if count != 1 || present {
			return refuse()
		}
	}
	return ctx.Err()
}
