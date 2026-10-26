package database

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

type retiredMountReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// RefuseRetiredMountStateAtStartup permits only the pristine installed reset
// seed to precede ordinary schema creation. Reset deliberately installs a
// metadata-only database; absence of leases in any other established database
// is not evidence of an empty obligation ledger. The destructive proof reader
// continues to use the stricter RefuseRetiredMountState below.
func RefuseRetiredMountStateAtStartup(ctx context.Context, home string, db *sql.DB) error {
	var leasesPresent bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE lower(name)='leases')`).Scan(&leasesPresent); err != nil {
		return fmt.Errorf("retired mounting startup schema check failed: %w", err)
	}
	if leasesPresent {
		return RefuseRetiredMountState(ctx, db)
	}
	if err := pristineInstalledResetSeed(ctx, home); err != nil {
		return fmt.Errorf("established database lacks leases and is not the exact installed fresh reset seed; no recovery or schema changes permitted: %w", err)
	}
	return nil
}

func pristineInstalledResetSeed(ctx context.Context, home string) error {
	d, err := OpenDirectory(home)
	if err != nil {
		return err
	}
	defer d.Close()
	seed, _, err := readResetJSON[ResetSeed](d, ResetSeedName)
	if err != nil {
		return err
	}
	if !seed.Installed {
		return fmt.Errorf("reset seed has not been installed under the daemon lock")
	}
	s, err := openResetStore(d.Path, seed.Operation, false)
	if err != nil {
		return err
	}
	defer s.close()
	receipt, _, err := s.readReceipt()
	if err != nil {
		return err
	}
	if err = validSeedReceipt(seed, receipt); err != nil {
		return err
	}
	for _, name := range []string{"state.db-wal", "state.db-shm", "state.db-journal"} {
		companion, err := d.Observe(name, false)
		if err != nil || companion.Present {
			return fmt.Errorf("pristine reset seed has unexpected SQLite companion %s", name)
		}
	}
	expected := seed.File
	expected.Name = "state.db"
	if _, err = exactResetFile(ctx, d, expected, false); err != nil {
		return err
	}
	return validateInstalledSeed(ctx, filepath.Join(d.Path, "state.db"), seed)
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
