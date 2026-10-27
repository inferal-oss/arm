package database

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const QuickTimeout = 10 * time.Second
const FullTimeout = 2 * time.Minute
const BackupTimeout = 2 * time.Minute

type Stats struct {
	Scope           string           `json:"scope"`
	Observed        time.Time        `json:"observed_at"`
	Host            string           `json:"host"`
	Files           []File           `json:"files"`
	PageSize        int64            `json:"page_size_bytes"`
	Pages           int64            `json:"page_count"`
	FreePages       int64            `json:"freelist_count"`
	Logical         int64            `json:"logical_bytes"`
	Reusable        int64            `json:"reusable_bytes"`
	Tables          map[string]int64 `json:"table_counts"`
	Leases          map[string]int64 `json:"lease_states"`
	Obligations     map[string]int64 `json:"known_obligation_counts"`
	ObligationScope string           `json:"obligation_scope"`
	Detail          string           `json:"detail"`
}

func ReadStats(ctx context.Context, s *Source) (Stats, error) {
	result := Stats{Scope: "stopped consistent copy", Observed: SnapshotTime(), Files: s.Files, Tables: map[string]int64{}, Leases: map[string]int64{}, Obligations: map[string]int64{}, ObligationScope: "Known state counts only; not a complete settled-obligation proof. Unknown/malformed records remain unknown, never safe by inference.", Detail: "Allocated blocks are observations, not exclusive reclaimable space; APFS clones/snapshots may share blocks. No compaction or repair was performed."}
	if s.live {
		result.Scope = "online SQLite read transaction; file sizes are separately timed observations"
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT substr(value,1,33) FROM metadata WHERE key='host'").Scan(&result.Host); err != nil {
		return result, err
	}
	if !validHost(result.Host) {
		return result, fmt.Errorf("invalid database host identity; no authority was inferred")
	}
	for query, out := range map[string]*int64{"PRAGMA page_size": &result.PageSize, "PRAGMA page_count": &result.Pages, "PRAGMA freelist_count": &result.FreePages} {
		if err = tx.QueryRowContext(ctx, query).Scan(out); err != nil {
			return result, err
		}
	}
	if result.PageSize <= 0 || result.Pages < 0 || result.FreePages < 0 || result.FreePages > result.Pages || result.Pages > MaxDatabaseBytes/result.PageSize {
		return result, fmt.Errorf("SQLite page counts exceed maintenance bounds")
	}
	result.Logical, result.Reusable = result.Pages*result.PageSize, result.FreePages*result.PageSize
	rows, err := tx.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name LIMIT 129")
	if err != nil {
		return result, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return result, err
		}
		if len(name) > 128 {
			rows.Close()
			return result, fmt.Errorf("table name exceeds bound")
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	if len(names) > 128 {
		return result, fmt.Errorf("table catalog exceeds128-table inspection bound")
	}
	for _, name := range names {
		var count int64
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`).Scan(&count); err != nil {
			return result, err
		}
		result.Tables[name] = count
	}
	if _, exists := result.Tables["leases"]; exists {
		rows, err = tx.QueryContext(ctx, `SELECT CASE WHEN json_valid(data) AND json_extract(data,'$.state') IN ('queued','provisioning','active','ending','needs_attention','reclaiming','terminal') THEN json_extract(data,'$.state') ELSE 'unknown' END,COUNT(*) FROM leases GROUP BY 1`)
		if err != nil {
			return result, err
		}
		for rows.Next() {
			var state string
			var n int64
			if err = rows.Scan(&state, &n); err != nil {
				rows.Close()
				return result, err
			}
			result.Leases[state] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
	}
	for label, spec := range map[string]struct{ table, query string }{
		"leases_awaiting_settlement":     {"leases", `SELECT COUNT(*) FROM leases WHERE NOT json_valid(data) OR CASE WHEN json_valid(data) THEN COALESCE(json_extract(data,'$.state'),'unknown')<>'terminal' ELSE 1 END`},
		"exports_awaiting_settlement":    {"storage_exports", `SELECT COUNT(*) FROM storage_exports WHERE COALESCE(state,'unknown') NOT IN ('published','handed_off')`},
		"cargo_unsettled":                {"cargo_workloads", `SELECT COUNT(*) FROM cargo_workloads WHERE NOT json_valid(data) OR CASE WHEN json_valid(data) THEN COALESCE(json_extract(data,'$.phase'),'unknown')<>'finished' OR COALESCE(json_extract(data,'$.uncertain'),1)<>0 OR COALESCE(json_extract(data,'$.group_absent'),0)<>1 OR COALESCE(json_extract(data,'$.worker'),'')<>'' ELSE 1 END`},
		"gradle_unsettled":               {"gradle_workloads", `SELECT COUNT(*) FROM gradle_workloads WHERE NOT json_valid(data) OR CASE WHEN json_valid(data) THEN COALESCE(json_extract(data,'$.phase'),'unknown')<>'finished' OR COALESCE(json_extract(data,'$.uncertain'),1)<>0 ELSE 1 END`},
		"mesh_intents_without_result":    {"mesh_operations", `SELECT COUNT(*) FROM mesh_operations WHERE result IS NULL`},
		"policy_outbox_unsettled":        {"policy_outbox", `SELECT COUNT(*) FROM policy_outbox WHERE COALESCE(state,'unknown') NOT IN ('done','skipped')`},
		"host_pressure_outbox_unsettled": {"host_pressure_outbox", `SELECT COUNT(*) FROM host_pressure_outbox WHERE COALESCE(state,'unknown') NOT IN ('done','skipped')`},
		"deliveries_pending_or_unknown":  {"messages", `SELECT COUNT(*) FROM messages WHERE CASE WHEN json_valid(data) AND json_type(data,'$.deliveries')='object' THEN EXISTS(SELECT 1 FROM json_each(data,'$.deliveries') AS d WHERE CASE WHEN json_valid(d.value) THEN COALESCE(json_extract(d.value,'$.state'),'unknown') NOT IN ('available','queued','responded','failed','superseded') ELSE 1 END) ELSE 1 END`},
	} {
		if _, exists := result.Tables[spec.table]; exists {
			var n int64
			if err = tx.QueryRowContext(ctx, spec.query).Scan(&n); err != nil {
				return result, err
			}
			result.Obligations[label] = n
		}
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, s.Validate(ctx, !s.live)
}

type Check struct {
	CustomNames  *CustomNamesInspection `json:"custom_names,omitempty"`
	Scope        string                 `json:"scope"`
	Mode         string                 `json:"mode"`
	Findings     []string               `json:"findings"`
	Limit        int                    `json:"finding_limit"`
	LimitReached bool                   `json:"limit_reached"`
	Complete     bool                   `json:"complete"`
	OK           bool                   `json:"ok"`
	ForeignKeys  string                 `json:"foreign_keys"`
	Started      time.Time              `json:"started_at"`
	Finished     time.Time              `json:"finished_at"`
	Detail       string                 `json:"detail"`
}

func CheckDatabase(ctx context.Context, s *Source, full bool) (result Check, err error) {
	result = Check{Scope: "stopped consistent copy", Mode: "quick", Limit: 100, Findings: []string{}, ForeignKeys: "not checked in quick mode", Started: SnapshotTime(), Detail: "SQLite structural check only; provider obligations, process liveness and application-policy correctness were not certified. No repair was attempted."}
	defer func() { result.Finished = SnapshotTime() }()
	query := "PRAGMA quick_check(100)"
	if full {
		result.Mode = "full integrity"
		query = "PRAGMA integrity_check(100)"
	}
	if s.live {
		result.Scope = "online SQLite read transaction"
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var finding string
		if err = rows.Scan(&finding); err != nil {
			rows.Close()
			return result, err
		}
		if len(finding) > 4096 {
			finding = finding[:4096] + " [finding text truncated]"
		}
		result.Findings = append(result.Findings, finding)
		if len(result.Findings) >= result.Limit {
			result.LimitReached = true
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	structuralOK := len(result.Findings) == 1 && result.Findings[0] == "ok"
	if full && !result.LimitReached {
		rows, err = tx.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' LIMIT 129")
		if err != nil {
			return result, err
		}
		var names []string
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				rows.Close()
				return result, err
			}
			if len(name) > 128 {
				rows.Close()
				return result, fmt.Errorf("table name exceeds bound")
			}
			names = append(names, name)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
		if len(names) > 128 {
			return result, fmt.Errorf("table catalog exceeds inspection bound")
		}
		declared := false
		for _, name := range names {
			rows, err = tx.QueryContext(ctx, `PRAGMA foreign_key_list("`+strings.ReplaceAll(name, `"`, `""`)+`")`)
			if err != nil {
				return result, err
			}
			if rows.Next() {
				declared = true
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return result, err
			}
		}
		result.ForeignKeys = "none declared in schema"
		if declared {
			result.ForeignKeys = "declared constraints checked"
			rows, err = tx.QueryContext(ctx, "PRAGMA foreign_key_check")
			if err != nil {
				return result, err
			}
			for rows.Next() {
				var table, parent string
				var row sql.NullInt64
				var key int64
				if err = rows.Scan(&table, &row, &parent, &key); err != nil {
					rows.Close()
					return result, err
				}
				if len(table) > 128 || len(parent) > 128 {
					rows.Close()
					return result, fmt.Errorf("foreign-key finding exceeds name bound")
				}
				result.Findings = append(result.Findings, fmt.Sprintf("foreign key: table=%s row=%v parent=%s key=%d", table, row, parent, key))
				if len(result.Findings) >= result.Limit {
					result.LimitReached = true
					break
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return result, err
			}
		}
	}
	var namesHost string
	if err = tx.QueryRowContext(ctx, "SELECT substr(value,1,33) FROM metadata WHERE key='host'").Scan(&namesHost); err != nil {
		return result, err
	}
	names, namesErr := InspectCustomNames(ctx, tx, strconv.Itoa(os.Geteuid()), namesHost)
	if namesErr != nil {
		return result, namesErr
	}
	result.CustomNames = &names
	if err = tx.Commit(); err != nil {
		return result, err
	}
	if err = s.Validate(ctx, !s.live); err != nil {
		return result, err
	}
	result.Complete = !result.LimitReached
	result.OK = result.Complete && structuralOK && len(result.Findings) == 1
	return result, nil
}

func validHost(host string) bool {
	if len(host) != 32 || strings.ToLower(host) != host {
		return false
	}
	_, err := hex.DecodeString(host)
	return err == nil
}
