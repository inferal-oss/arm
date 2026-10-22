package routing

import (
	"context"
	"database/sql"
)

var graphNames = []string{"routing_graph_issuance", "routing_graph_reports", "routing_graph_observations"}
var graphStatements = []string{
	`CREATE TABLE routing_graph_issuance(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE routing_graph_reports(workspace TEXT NOT NULL,source TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,source))`,
	`CREATE TABLE routing_graph_observations(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
}

// graphSchema upgrades ONLY inside an explicitly authorized graph mutation.
// Ordinary schema/Read checks never create tables or upgrade a legacy group.
func graphSchema(ctx context.Context, tx *sql.Tx) error {
	if _, e := schema(ctx, tx, true); e != nil {
		return e
	}
	var marker string
	if e := tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='routing_schema'`).Scan(&marker); e != nil {
		return e
	}
	if marker == "2" {
		return nil
	}
	for _, ddl := range graphStatements {
		if _, e := tx.ExecContext(ctx, ddl); e != nil {
			return e
		}
	}
	_, e := tx.ExecContext(ctx, `UPDATE metadata SET value='2' WHERE key='routing_schema' AND value='1'`)
	return e
}
