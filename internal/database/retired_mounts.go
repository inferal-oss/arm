package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type retiredMountReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// RefuseRetiredMountState runs before generic lease/storage recovery and before
// destructive settled proof. Retired tables (even empty or partial), markers,
// and orphan internal leases are unsupported evidence, never absence of duties.
// It neither migrates the database nor touches the associated native files.
func RefuseRetiredMountState(ctx context.Context, db retiredMountReader) error {
	checks := []string{
		`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE lower(name) GLOB 'share_*' OR lower(tbl_name) GLOB 'share_*' OR lower(name) GLOB 'snapshot_*' OR lower(tbl_name) GLOB 'snapshot_*' LIMIT 1)`,
		`SELECT EXISTS(SELECT 1 FROM metadata WHERE lower(key) GLOB 'share_*' OR lower(key) GLOB 'snapshot_*' LIMIT 1)`,
		`SELECT EXISTS(SELECT 1 FROM leases WHERE typeof(data)<>'text' OR NOT json_valid(data) LIMIT 1)`,
	}
	for _, query := range checks {
		var found bool
		if err := db.QueryRowContext(ctx, query).Scan(&found); err != nil {
			return fmt.Errorf("retired mounting obligation check failed; no recovery or cleanup permitted: %w", err)
		}
		if found {
			return fmt.Errorf("unsupported retired mounting/share/snapshot state; preserve this database and its files for explicit recovery; no automatic cleanup or conversion to sync is permitted")
		}
	}
	// Project keys and bounded scalar prefixes inside SQLite, rather than copying
	// potentially large ordinary Cargo/Gradle lease bodies into a new startup
	// parser. Go EqualFold matches recognized JSON field aliases, including the
	// Unicode aliases accepted by encoding/json. This introduces no row-size cap.
	rows, err := db.QueryContext(ctx, `SELECT
		CAST(substr(CAST(top.key AS BLOB),1,65) AS TEXT),top.type,
		CAST(substr(CAST(field.key AS BLOB),1,65) AS TEXT),field.type,
		CAST(substr(CAST(field.atom AS BLOB),1,128) AS TEXT)
		FROM leases AS lease JOIN json_each(lease.data) AS top
		LEFT JOIN json_each(CASE WHEN top.type='object' THEN top.value ELSE '{}' END) AS field`)
	if err != nil {
		return fmt.Errorf("retired mounting lease check failed: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var top, topType string
		var field, fieldType, value sql.NullString
		if err = rows.Scan(&top, &topType, &field, &fieldType, &value); err != nil {
			return err
		}
		request, artifact := strings.EqualFold(top, "request"), strings.EqualFold(top, "artifact")
		if !request && !artifact {
			continue
		}
		if topType != "object" && topType != "null" {
			return fmt.Errorf("unsupported retired mounting check: malformed lease authority; no recovery or cleanup permitted")
		}
		if !(request && strings.EqualFold(field.String, "kind") || artifact && strings.EqualFold(field.String, "policy")) {
			continue
		}
		if fieldType.String != "text" && fieldType.String != "null" {
			return fmt.Errorf("unsupported retired mounting check: malformed lease kind/policy; no recovery or cleanup permitted")
		}
		text := strings.ToLower(value.String)
		if strings.HasPrefix(text, "snapshot-") || request && strings.HasPrefix(text, "share-") {
			return fmt.Errorf("unsupported retired mounting/share/snapshot lease; preserve this database and its files for explicit recovery; no automatic cleanup or conversion to sync is permitted")
		}
	}
	return rows.Err()
}
