package routing

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/inferal-oss/arm/internal/model"
)

var statements = []string{
	`CREATE TABLE routing_roots(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE routing_outbox(workspace TEXT NOT NULL,endpoint TEXT NOT NULL,key TEXT NOT NULL,message TEXT NOT NULL,request TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,endpoint,key),UNIQUE(workspace,message))`,
	`CREATE TABLE routing_inbox(workspace TEXT NOT NULL,origin TEXT NOT NULL,endpoint TEXT NOT NULL,key TEXT NOT NULL,message TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,origin,endpoint,key),UNIQUE(workspace,message))`,
	`CREATE TABLE routing_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,workspace TEXT NOT NULL,data TEXT NOT NULL)`,
}
var names = []string{"routing_roots", "routing_outbox", "routing_inbox", "routing_events"}

func invalid(message string) error { return model.Err("routing_state_invalid", message) }
func schema(ctx context.Context, tx *sql.Tx, initialize bool) (bool, error) {
	rows, e := tx.QueryContext(ctx, `SELECT name,sql FROM sqlite_schema WHERE name LIKE 'routing_%' AND type='table' ORDER BY name LIMIT 8`)
	if e != nil {
		return false, e
	}
	seen := map[string]string{}
	for rows.Next() {
		var name, ddl string
		if e = rows.Scan(&name, &ddl); e != nil {
			rows.Close()
			return false, e
		}
		seen[name] = ddl
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	var marker string
	e = tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='routing_schema'`).Scan(&marker)
	if e != nil && e != sql.ErrNoRows {
		return false, e
	}
	if len(seen) == 0 && e == sql.ErrNoRows {
		var extra int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name LIKE 'routing_%' OR tbl_name LIKE 'routing_%'`).Scan(&extra); e != nil {
			return false, e
		}
		if extra > 0 {
			return false, invalid("unknown routing schema object without complete group")
		}
		if !initialize {
			return false, nil
		}
		for _, ddl := range statements {
			if _, e = tx.ExecContext(ctx, ddl); e != nil {
				return false, e
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('routing_schema','1')`); e != nil {
			return false, e
		}
		return true, nil
	}
	wantNames, wantStatements := names, statements
	if marker == "2" {
		wantNames = append(append([]string{}, names...), graphNames...)
		wantStatements = append(append([]string{}, statements...), graphStatements...)
	}
	if e != nil || marker != "1" && marker != "2" || len(seen) != len(wantNames) {
		return false, invalid("incomplete or unsupported routing ledger")
	}
	canonical := func(v string) string { return strings.ToLower(strings.Join(strings.Fields(v), " ")) }
	for i, name := range wantNames {
		if canonical(seen[name]) != canonical(wantStatements[i]) {
			return false, invalid(fmt.Sprintf("routing schema differs for %s", name))
		}
	}
	var extra int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE (name LIKE 'routing_%' OR tbl_name LIKE 'routing_%') AND type<>'table' AND sql IS NOT NULL`).Scan(&extra); e != nil {
		return false, e
	}
	if extra > 0 {
		return false, invalid("unexpected routing schema trigger/view/index")
	}
	return true, nil
}
func SchemaStatements() []string { return append([]string{}, statements...) }
