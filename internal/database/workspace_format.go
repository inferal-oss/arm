package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"getarm.org/internal/model"
)

// WorkspaceFormatKey describes ledger semantics, not a binary release number.
// Ordinary format 1 and opt-in named-storage format 3 are supported.
// The guard never activates a format; named-pool persistence does so atomically.
const WorkspaceFormatKey = "workspace_format"
const WritableWorkspaceFormat = "1"
const StoragePoolWorkspaceFormat = "3"

type workspaceFormatReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func unsupportedWorkspaceFormat() error {
	return model.Err("workspace_format_unsupported", "workspace format is unsupported or inconsistent; preserve this ledger and use a compatible writer; no automatic upgrade, downgrade or repair was performed")
}

// CheckWorkspaceFormat is a read-only baseline gate. Callers with a transaction
// pass that same snapshot before interpreting obligations or making any write.
// Absence remains legacy format 1 only when no paired-preparation evidence is
// present. Unknown values and even empty/partial provider namespaces refuse.
// Bounded projections avoid returning corrupt oversized marker values to Go.
func CheckWorkspaceFormat(ctx context.Context, q workspaceFormatReader) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := q.QueryContext(ctx, `SELECT typeof(key),substr(CAST(key AS BLOB),1,17),typeof(value),length(CAST(value AS BLOB)),substr(CAST(value AS BLOB),1,20) FROM metadata WHERE lower(CAST(key AS TEXT))='workspace_format' LIMIT 2`)
	if err != nil {
		return fmt.Errorf("workspace format could not be checked: %w", err)
	}
	count := 0
	format := WritableWorkspaceFormat
	for rows.Next() {
		var keyType, valueType string
		var key, value []byte
		var size sql.NullInt64
		err = rows.Scan(&keyType, &key, &valueType, &size, &value)
		count++
		if err != nil || count != 1 || keyType != "text" || string(key) != WorkspaceFormatKey || valueType != "text" || !size.Valid || size.Int64 != 1 || (string(value) != WritableWorkspaceFormat && string(value) != StoragePoolWorkspaceFormat) {
			rows.Close()
			return unsupportedWorkspaceFormat()
		}
		format = string(value)
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return fmt.Errorf("workspace format could not be checked: %w", err)
	}
	// A missing or forged ordinary marker cannot authorize an unsupported pair.
	// Neither this guard nor format 1 installs the inert optional schema.
	for _, query := range []string{
		`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE lower(name) GLOB 'provider_preparation*' OR lower(tbl_name) GLOB 'provider_preparation*')`,
		`SELECT EXISTS(SELECT 1 FROM metadata WHERE lower(CAST(key AS TEXT))='provider_preparation_schema')`,
	} {
		var present bool
		if err = q.QueryRowContext(ctx, query).Scan(&present); err != nil {
			return fmt.Errorf("workspace format namespace could not be checked: %w", err)
		}
		if present {
			return unsupportedWorkspaceFormat()
		}
	}

	// Named storage changes durable placement/accounting. Older format-1 writers
	// must refuse it rather than silently dropping selectors during recovery.
	var namedCount, canonicalCount int
	if err = q.QueryRowContext(ctx, `SELECT count(*),coalesce(sum(CASE WHEN typeof(key)='text' AND key='storage_pools' AND typeof(value)='text' AND length(CAST(value AS BLOB))<=32768 THEN CASE WHEN json_valid(value) THEN json_type(value)='array' ELSE 0 END ELSE 0 END),0) FROM metadata WHERE lower(CAST(key AS TEXT))='storage_pools'`).Scan(&namedCount, &canonicalCount); err != nil {
		return err
	}
	if format == StoragePoolWorkspaceFormat {
		if namedCount != 1 || canonicalCount != 1 {
			return unsupportedWorkspaceFormat()
		}
	} else if namedCount != 0 {
		return unsupportedWorkspaceFormat()
	}
	var leases bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE lower(name)='leases')`).Scan(&leases); err != nil {
		return fmt.Errorf("workspace format lease schema could not be checked: %w", err)
	}
	if !leases { // Pristine metadata-only reset seeds are checked by their owner.
		return nil
	}
	// Scan into []byte plus json.Unmarshal can accept valid JSON stored as BLOB.
	// Refuse noncanonical storage before the JSON projection can hide it behind
	// '{}'. This matches the existing ordinary Manager retired-state guard:
	// lease bodies must be SQLite TEXT containing valid JSON, with no conversion.
	var unreadable bool
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM leases WHERE typeof(data)<>'text' OR NOT json_valid(data))`).Scan(&unreadable); err != nil {
		return fmt.Errorf("workspace format lease storage could not be checked: %w", err)
	}
	if unreadable {
		return unsupportedWorkspaceFormat()
	}

	if format != StoragePoolWorkspaceFormat {
		var named bool
		if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM leases AS l
   JOIN json_each(l.data) AS field
   LEFT JOIN json_each(CASE WHEN replace(lower(field.key),'ſ','s')='request' AND field.type='object' THEN field.value ELSE '{}' END) AS request
   WHERE replace(lower(field.key),'ſ','s')='storage_binding'
   OR (replace(lower(field.key),'ſ','s')='request' AND replace(lower(request.key),'ſ','s')='storage_pool' AND (request.type<>'text' OR request.atom<>'')))`).Scan(&named); err != nil {
			return err
		}
		if named {
			return unsupportedWorkspaceFormat()
		}
	}
	var held bool
	// Go's JSON field matching also folds long-s in "request" and Kelvin-sign
	// in "kind". SQLite lower() alone does not cover those accepted aliases.
	if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM leases AS l
		JOIN json_each(CASE WHEN typeof(l.data)='text' AND json_valid(l.data) THEN l.data ELSE '{}' END) AS field
		LEFT JOIN json_each(CASE WHEN replace(lower(field.key),'ſ','s')='request' AND field.type='object' THEN field.value ELSE '{}' END) AS request
		WHERE lower(field.key)='provider_preparation'
		OR (replace(lower(field.key),'ſ','s')='request' AND replace(lower(request.key),'K','k')='kind' AND request.type='text' AND request.atom='provider-prepare-worker'))`).Scan(&held); err != nil {
		return fmt.Errorf("workspace format lease references could not be checked: %w", err)
	}
	if held {
		return unsupportedWorkspaceFormat()
	}
	return nil
}

// CheckWorkspaceFormatFile precedes a direct Manager's read/write SQLite open.
// Like other WAL-aware startup reads, SQLite may maintain private SHM/WAL
// bookkeeping; no logical row/schema mutation or rollback repair is requested.
// This is not a replacement for the daemon's retained authority lock.
func CheckWorkspaceFormatFile(ctx context.Context, home string) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db, err := openRO(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return CheckWorkspaceFormat(ctx, tx)
}
