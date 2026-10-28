package manager

import (
	"database/sql"

	"getarm.org/internal/model"
)

// This schema is a pre-bind retention dependency. No production path inserts
// a pair yet. It must be installed before startup recovery reads old leases:
// SQLite enforces the hold even if an older Manager ignores the new table.
const forwardUnixPairTable = `CREATE TABLE IF NOT EXISTS forward_unix_pairs (
	source_id TEXT PRIMARY KEY,
	storage_id TEXT NOT NULL UNIQUE,
	phase TEXT NOT NULL CHECK (phase IN ('planned','observed','bound','active','closing','uncertain','terminal')),
	source_key TEXT NOT NULL,
	storage_key TEXT NOT NULL,
	socket_name TEXT NOT NULL,
	disk_bytes INTEGER NOT NULL CHECK (disk_bytes > 0),
	data TEXT NOT NULL CHECK (json_valid(data))
)`

const forwardUnixSourceUpdateGuard = `CREATE TRIGGER IF NOT EXISTS forward_unix_source_update_guard
BEFORE UPDATE OF data ON leases
WHEN CASE WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.source_id=OLD.id AND p.phase IS NOT 'terminal')
THEN (json_extract(NEW.data,'$.state') IS 'terminal'
     OR json_extract(NEW.data,'$.request.storage_lease') IS NOT (SELECT storage_id FROM forward_unix_pairs WHERE source_id=OLD.id)
     OR json_extract(NEW.data,'$.request') IS NOT json_extract(OLD.data,'$.request')
     OR json_extract(NEW.data,'$.ports') IS NOT json_extract(OLD.data,'$.ports')) ELSE 0 END
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix source pair retains charge'); END`

const forwardUnixStorageUpdateGuard = `CREATE TRIGGER IF NOT EXISTS forward_unix_storage_update_guard
BEFORE UPDATE OF data ON leases
WHEN CASE WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.storage_id=OLD.id AND p.phase IS NOT 'terminal')
THEN (json_extract(NEW.data,'$.state') IS NOT 'active'
     AND json_extract(NEW.data,'$.state') IS NOT 'needs_attention'
     OR json_extract(NEW.data,'$.request') IS NOT json_extract(OLD.data,'$.request')
     OR json_extract(NEW.data,'$.artifact.identity') IS NOT json_extract(OLD.data,'$.artifact.identity')) ELSE 0 END
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix storage pair retains charge'); END`

const forwardUnixSourceDeleteGuard = `CREATE TRIGGER IF NOT EXISTS forward_unix_source_delete_guard
BEFORE DELETE ON leases
WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.source_id=OLD.id AND p.phase IS NOT 'terminal')
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix source pair retains charge'); END`

const forwardUnixStorageDeleteGuard = `CREATE TRIGGER IF NOT EXISTS forward_unix_storage_delete_guard
BEFORE DELETE ON leases
WHEN EXISTS (SELECT 1 FROM forward_unix_pairs p WHERE p.storage_id=OLD.id AND p.phase IS NOT 'terminal')
BEGIN SELECT RAISE(ABORT,'unfinished forward Unix storage pair retains charge'); END`

// initForwardUnixPairGuard deliberately has no issuer. Its future issuer must
// atomically bind the source's StorageLease and insert an exact original pair,
// then positively acknowledge that transaction before any Unix pathname bind.
func initForwardUnixPairGuard(db *sql.DB) error {
	for _, ddl := range []string{forwardUnixPairTable, forwardUnixSourceUpdateGuard, forwardUnixStorageUpdateGuard, forwardUnixSourceDeleteGuard, forwardUnixStorageDeleteGuard} {
		if _, err := db.Exec(ddl); err != nil {
			return err
		}
	}
	var present int
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM forward_unix_pairs)`).Scan(&present); err != nil {
		return err
	}
	if present != 0 {
		// This cut has no trusted issuer or reader for a pair. A future reader
		// must validate every complete tuple before allowing startup.
		return model.Err("forward_unix_pair_unavailable", "forward Unix pair record requires a compatible Manager")
	}
	return nil
}
