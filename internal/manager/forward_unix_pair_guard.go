package manager

import (
	"context"
	"database/sql"
	"fmt"
)

// These five objects plus two SQLite constraint indexes are one atomic
// startup group. The private binder validates the complete tuple before bind.
const forwardUnixPairTable = `CREATE TABLE forward_unix_pairs (
	source_id TEXT PRIMARY KEY,
	storage_id TEXT NOT NULL UNIQUE,
	phase TEXT NOT NULL CHECK (phase IN ('planned','observed','bind_intent','bound','active','closing','uncertain','terminal')),
	source_key TEXT NOT NULL,
	storage_key TEXT NOT NULL,
	socket_name TEXT NOT NULL,
	disk_bytes INTEGER NOT NULL CHECK (disk_bytes > 0),
	data TEXT NOT NULL CHECK (json_valid(data))
)`

const forwardUnixSourceUpdateGuard = `CREATE TRIGGER forward_unix_source_update_guard
BEFORE UPDATE ON leases
WHEN CASE WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.source_id=OLD.id AND p.phase IS NOT 'terminal')
THEN (NEW.id IS NOT OLD.id
     OR NEW.request_key IS NOT OLD.request_key
     OR NEW.request_key IS NOT (SELECT source_key FROM forward_unix_pairs WHERE source_id=OLD.id)
     OR json_extract(NEW.data,'$.id') IS NOT OLD.id
     OR json_extract(NEW.data,'$.host') IS NULL
     OR json_extract(NEW.data,'$.owner') IS NULL
     OR json_extract(NEW.data,'$.host') IS NOT json_extract(OLD.data,'$.host')
     OR json_extract(NEW.data,'$.owner') IS NOT json_extract(OLD.data,'$.owner')
     OR (json_extract(NEW.data,'$.state') IS NOT 'active'
         AND json_extract(NEW.data,'$.state') IS NOT 'provisioning'
         AND json_extract(NEW.data,'$.state') IS NOT 'ending'
         AND json_extract(NEW.data,'$.state') IS NOT 'needs_attention')
     OR json_extract(NEW.data,'$.request.storage_lease') IS NOT (SELECT storage_id FROM forward_unix_pairs WHERE source_id=OLD.id)
     OR json_extract(NEW.data,'$.request') IS NOT json_extract(OLD.data,'$.request')
     OR json_extract(NEW.data,'$.ports') IS NOT json_extract(OLD.data,'$.ports')) ELSE 0 END
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix source pair retains charge'); END`

const forwardUnixStorageUpdateGuard = `CREATE TRIGGER forward_unix_storage_update_guard
BEFORE UPDATE ON leases
WHEN CASE WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.storage_id=OLD.id AND p.phase IS NOT 'terminal')
THEN (NEW.id IS NOT OLD.id
     OR NEW.request_key IS NOT OLD.request_key
     OR NEW.request_key IS NOT (SELECT storage_key FROM forward_unix_pairs WHERE storage_id=OLD.id)
     OR json_extract(NEW.data,'$.id') IS NOT OLD.id
     OR json_extract(NEW.data,'$.host') IS NULL
     OR json_extract(NEW.data,'$.owner') IS NULL
     OR json_extract(NEW.data,'$.host') IS NOT json_extract(OLD.data,'$.host')
     OR json_extract(NEW.data,'$.owner') IS NOT json_extract(OLD.data,'$.owner')
     OR (json_extract(NEW.data,'$.state') IS NOT 'active'
         AND json_extract(NEW.data,'$.state') IS NOT 'needs_attention')
     OR json_extract(NEW.data,'$.request') IS NOT json_extract(OLD.data,'$.request')
     OR json_extract(NEW.data,'$.artifact.identity') IS NOT json_extract(OLD.data,'$.artifact.identity')) ELSE 0 END
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix storage pair retains charge'); END`

const forwardUnixSourceDeleteGuard = `CREATE TRIGGER forward_unix_source_delete_guard
BEFORE DELETE ON leases
WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.source_id=OLD.id AND p.phase IS NOT 'terminal')
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix source pair retains charge'); END`

const forwardUnixStorageDeleteGuard = `CREATE TRIGGER forward_unix_storage_delete_guard
BEFORE DELETE ON leases
WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.storage_id=OLD.id AND p.phase IS NOT 'terminal')
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix storage pair retains charge'); END`

type forwardUnixGuardObject struct{ kind, name, table, ddl string }

var forwardUnixGuardObjects = [...]forwardUnixGuardObject{
	{"table", "forward_unix_pairs", "forward_unix_pairs", forwardUnixPairTable},
	{"trigger", "forward_unix_source_update_guard", "leases", forwardUnixSourceUpdateGuard},
	{"trigger", "forward_unix_storage_update_guard", "leases", forwardUnixStorageUpdateGuard},
	{"trigger", "forward_unix_source_delete_guard", "leases", forwardUnixSourceDeleteGuard},
	{"trigger", "forward_unix_storage_delete_guard", "leases", forwardUnixStorageDeleteGuard},
	{"index", "sqlite_autoindex_forward_unix_pairs_1", "forward_unix_pairs", ""},
	{"index", "sqlite_autoindex_forward_unix_pairs_2", "forward_unix_pairs", ""},
}

func readForwardUnixGuardObjects(ctx context.Context, tx *sql.Tx) (map[string]forwardUnixGuardObject, error) {
	rows, err := tx.QueryContext(ctx, `SELECT substr(type,1,33),substr(name,1,129),substr(tbl_name,1,129),substr(sql,1,4097) FROM sqlite_schema WHERE name GLOB 'forward_unix_*' OR name GLOB 'sqlite_autoindex_forward_unix_pairs_*' OR tbl_name='forward_unix_pairs' OR sql LIKE '%forward_unix_pairs%' ORDER BY name LIMIT 9`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	actual := map[string]forwardUnixGuardObject{}
	count := 0
	for rows.Next() {
		count++
		if count > len(forwardUnixGuardObjects) {
			return nil, fmt.Errorf("forward Unix guard schema has extra objects")
		}
		var kind, name, table string
		var ddl sql.NullString
		if err := rows.Scan(&kind, &name, &table, &ddl); err != nil {
			return nil, err
		}
		if kind == "index" && ddl.Valid || kind != "index" && !ddl.Valid {
			return nil, fmt.Errorf("forward Unix guard schema SQL presence differs")
		}
		if len(kind) > 32 || len(name) > 128 || len(table) > 128 || len(ddl.String) > 4096 || actual[name].name != "" {
			return nil, fmt.Errorf("forward Unix guard schema has duplicate or oversized object")
		}
		actual[name] = forwardUnixGuardObject{kind, name, table, ddl.String}
	}
	return actual, rows.Err()
}

func validateForwardUnixGuardObjects(actual map[string]forwardUnixGuardObject) error {
	if len(actual) != len(forwardUnixGuardObjects) {
		return fmt.Errorf("forward Unix guard schema is partial")
	}
	for _, expected := range forwardUnixGuardObjects {
		if actual[expected.name] != expected {
			return fmt.Errorf("forward Unix guard schema object %s differs", expected.name)
		}
	}
	return nil
}

// initForwardUnixPairGuard installs an entirely absent group in one durable
// transaction, or validates the exact existing group. It never repairs a
// partial/no-op group. A strict reader may atomically abort only an exact
// planned no-bind tuple before generic recovery. Bind intent and bound phases
// still refuse startup; the private binder has no restart cleanup authority.
func initForwardUnixPairGuard(db *sql.DB, allowPlanned bool) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	actual, err := readForwardUnixGuardObjects(ctx, tx)
	if err != nil {
		return err
	}
	if len(actual) == 0 {
		for _, object := range forwardUnixGuardObjects[:5] {
			if _, err = tx.ExecContext(ctx, object.ddl); err != nil {
				return err
			}
		}
		actual, err = readForwardUnixGuardObjects(ctx, tx)
		if err != nil {
			return err
		}
	}
	if err = validateForwardUnixGuardObjects(actual); err != nil {
		return err
	}
	if err = validateForwardUnixPairStartup(ctx, tx, allowPlanned); err != nil {
		return err
	}
	return tx.Commit()
}
