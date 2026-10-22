package workspace

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SchemaSQL is the complete optional group. It is NOT installed by Manager.Open
// or a public command in this checkpoint; Install runs only inside an explicitly
// requested ledger mutation transaction after the store has found no group.
var schemaSQL = []string{
	`CREATE TABLE workspace_instances(id TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE workspace_directory_members(workspace TEXT NOT NULL,host TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,host))`,
	`CREATE TABLE workspace_trusted_members(workspace TEXT NOT NULL,host TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,host))`,
	`CREATE TABLE workspace_observations(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE workspace_operations(workspace TEXT NOT NULL,principal TEXT NOT NULL,key TEXT NOT NULL,request TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,principal,key))`,
	`CREATE TABLE workspace_changes(sequence INTEGER PRIMARY KEY AUTOINCREMENT,workspace TEXT NOT NULL,data TEXT NOT NULL)`,
	`CREATE TABLE workspace_denies(workspace TEXT NOT NULL,host TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,host))`,
	`CREATE TABLE workspace_clocks(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
}
var tables = []string{"workspace_instances", "workspace_directory_members", "workspace_trusted_members", "workspace_observations", "workspace_operations", "workspace_changes", "workspace_denies", "workspace_clocks"}

func schema(ctx context.Context, tx *sql.Tx) (bool, error) {
	rows, e := tx.QueryContext(ctx, `SELECT name,sql FROM sqlite_schema WHERE name LIKE 'workspace_%' AND type='table' ORDER BY name LIMIT 10`)
	if e != nil {
		return false, e
	}
	defer rows.Close()
	seen := map[string]string{}
	for rows.Next() {
		var n, q string
		if e = rows.Scan(&n, &q); e != nil {
			return false, e
		}
		seen[n] = q
	}
	if e = rows.Err(); e != nil {
		return false, e
	}
	rows.Close()
	var marker string
	e = tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='workspace_schema'`).Scan(&marker)
	if e != nil && e != sql.ErrNoRows {
		return false, e
	}
	if len(seen) == 0 && e == sql.ErrNoRows {
		return false, nil
	}
	if e != nil || marker != "1" || len(seen) != len(tables) {
		return false, fmt.Errorf("incomplete/unknown workspace schema group")
	}
	for i, name := range tables {
		expected := schemaSQL[i]
		actual := seen[name]
		if strings.ToLower(strings.Join(strings.Fields(actual), " ")) != strings.ToLower(strings.Join(strings.Fields(expected), " ")) {
			return false, fmt.Errorf("workspace schema differs for %s", name)
		}
	}
	// No triggers/views or caller-added indexes may smuggle writes into this
	// security ledger. SQLite autoindexes have NULL SQL and remain expected.
	var extra int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE (tbl_name LIKE 'workspace_%' OR name LIKE 'workspace_%') AND type<>'table' AND sql IS NOT NULL`).Scan(&extra); e != nil {
		return false, e
	}
	if extra != 0 {
		return false, fmt.Errorf("unknown workspace schema object")
	}
	return true, nil
}
func install(ctx context.Context, tx *sql.Tx) error {
	for _, q := range schemaSQL {
		if _, e := tx.ExecContext(ctx, q); e != nil {
			return e
		}
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('workspace_schema','1')`)
	return e
}

// SchemaStatements returns a defensive copy for a separately reviewed proof
// catalog. Recognition alone does not establish retirement/settlement.
func SchemaStatements() []string { return append([]string(nil), schemaSQL...) }
