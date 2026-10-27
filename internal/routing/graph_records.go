package routing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/inferal-oss/arm/internal/workspace"
)

func readGraph(ctx context.Context, tx *sql.Tx, out *Records) error {
	var marker string
	if e := tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='routing_schema'`).Scan(&marker); e != nil {
		return e
	}
	if marker != "2" {
		return nil
	}
	out.Graph = &GraphRecords{Issuance: []GraphIssuance{}, Reports: []GraphReport{}, Observations: []GraphObservation{}}
	for i, table := range graphNames {
		columns, limit, maxBytes := "substr(workspace,1,33)", workspace.MaxWorkspaces, MaxGraphRecordBytes
		if i == 1 {
			columns += ",substr(source,1,33)"
			limit *= workspace.MaxMembers
			maxBytes = MaxRecordBytes
		}
		rows, e := tx.QueryContext(ctx, fmt.Sprintf(`SELECT %s,substr(CAST(data AS BLOB),1,%d) FROM %s LIMIT %d`, columns, maxBytes+1, table, limit+1))
		if e != nil {
			return e
		}
		count := 0
		for rows.Next() {
			count++
			if count > limit {
				rows.Close()
				return invalid("graph ledger row bound exceeded")
			}
			var ws, source string
			var raw []byte
			if i == 1 {
				e = rows.Scan(&ws, &source, &raw)
			} else {
				e = rows.Scan(&ws, &raw)
			}
			if e != nil {
				rows.Close()
				return e
			}
			out.rawBytes += len(raw)
			if len(raw) > maxBytes || out.rawBytes > MaxLedgerBytes {
				rows.Close()
				return invalid("complete graph/routing byte bound exceeded")
			}
			switch i {
			case 0:
				var v GraphIssuance
				e = workspace.Decode(raw, &v)
				if e == nil && v.Workspace != ws {
					e = invalid("graph issuance key differs")
				}
				if e == nil {
					var parentRaw []byte
					e = tx.QueryRowContext(ctx, `SELECT substr(CAST(data AS BLOB),1,1048577) FROM workspace_instances WHERE id=?`, ws).Scan(&parentRaw)
					if e == nil {
						var parent workspace.Instance
						e = workspace.Decode(parentRaw, &parent)
						if e == nil && (parent.Role != v.Role || parent.Generation != v.Generation || parent.Authority != v.Authority || parent.Self.Fingerprint != v.LocalPin) {
							e = invalid("graph role/native parent identity differs")
						}
					}
				}
				out.Graph.Issuance = append(out.Graph.Issuance, v)
			case 1:
				var v GraphReport
				e = workspace.Decode(raw, &v)
				if e == nil && (v.Workspace != ws || v.Report.Source != source) {
					e = invalid("graph report key differs")
				}
				out.Graph.Reports = append(out.Graph.Reports, v)
			case 2:
				var v GraphObservation
				e = workspace.Decode(raw, &v)
				if e == nil && v.Workspace != ws {
					e = invalid("graph observation key differs")
				}
				out.Graph.Observations = append(out.Graph.Observations, v)
			}
			if e != nil {
				rows.Close()
				return invalid(table + ": " + e.Error())
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	return nil
}

func graphPut(ctx context.Context, tx *sql.Tx, query string, value any, args ...any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	if len(raw) > MaxGraphRecordBytes {
		return invalid("complete graph record exceeds1MiB")
	}
	args = append(args, string(raw))
	_, e = tx.ExecContext(ctx, query, args...)
	return e
}
func writeGraphIssuer(ctx context.Context, tx *sql.Tx, g GraphIssuance) error {
	return graphPut(ctx, tx, `INSERT INTO routing_graph_issuance(workspace,data) VALUES(?,?) ON CONFLICT(workspace) DO UPDATE SET data=excluded.data`, g, g.Workspace)
}
