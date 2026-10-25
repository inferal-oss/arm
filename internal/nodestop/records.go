package nodestop

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const StateSchema = "arm.node-stop.state/v1"
const RecordSchema = "arm.node-stop.record/v1"

// SQLite INTEGER is signed64. JSON uint64 alone must never enlarge that domain.
const MaxRevision uint64 = 1<<63 - 1
const MaxRecords = 128

// Optional schema: created in the first admission transaction only. A partial,
// altered or unknown schema is refusal, never a legacy running intent.
var Tables = []string{
	`CREATE TABLE node_stop_schema (singleton INTEGER PRIMARY KEY CHECK(singleton=1), version INTEGER NOT NULL CHECK(version=1))`,
	`CREATE TABLE node_stop_state (singleton INTEGER PRIMARY KEY CHECK(singleton=1), data TEXT NOT NULL)`,
	`CREATE TABLE node_stop_operations (key TEXT PRIMARY KEY, data TEXT NOT NULL)`,
}

type State struct {
	Schema   string `json:"schema"`
	Revision uint64 `json:"revision"`
	Running  bool   `json:"desired_running"`
	Target   Target `json:"target"`
	Key      string `json:"stop_key"`
	Digest   string `json:"stop_sha256"`
}

type Record struct {
	Schema          string    `json:"schema"`
	Plan            Plan      `json:"plan"`
	Digest          string    `json:"review_sha256"`
	Shutdown        string    `json:"shutdown_id"`
	DesiredRevision uint64    `json:"desired_state_revision"`
	Phase           string    `json:"phase"`
	Created         time.Time `json:"created_at"`
	Updated         time.Time `json:"updated_at"`
}

func (r Record) Validate() error {
	hash, err := r.Plan.Digest()
	if err != nil || r.Schema != RecordSchema || r.Digest != hash || !hashText(r.Shutdown, 16) || r.Plan.DesiredRevision >= MaxRevision || r.DesiredRevision != r.Plan.DesiredRevision+1 || r.Created.IsZero() || r.Updated.Before(r.Created) {
		return fmt.Errorf("invalid immutable node-stop operation")
	}
	switch r.Phase {
	case "accepted", "drain-ready", "interrupted":
		return nil
	default:
		return fmt.Errorf("unknown node-stop phase; transport or database progress does not prove stopped")
	}
}

func (s State) Validate(r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if s.Revision > MaxRevision || s.Schema != StateSchema || s.Key != r.Plan.Key || s.Digest != r.Digest || s.Target != r.Plan.Target || s.Revision < r.DesiredRevision || (!s.Running && s.Revision != r.DesiredRevision) || (s.Running && (r.DesiredRevision >= MaxRevision || s.Revision != r.DesiredRevision+1)) {
		return fmt.Errorf("node-stop desired state lacks exact original operation")
	}
	return nil
}

type Queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// ReadState is read-only and works on an already held writer or stopped RO
// connection. It never creates a schema, supplies an assumed empty record or
// clears a stopped intent. Existing unknown schemas remain reset blockers too.
func ReadState(db Queryer) (State, bool, error) {
	var state State
	rows, err := db.Query(`SELECT substr(type,1,33),substr(name,1,129),substr(tbl_name,1,129),substr(sql,1,4097) FROM sqlite_schema WHERE name GLOB 'node_stop_*' OR tbl_name GLOB 'node_stop_*' ORDER BY name LIMIT 9`)
	if err != nil {
		return state, false, err
	}
	want := map[string]string{"node_stop_schema": Tables[0], "node_stop_state": Tables[1], "node_stop_operations": Tables[2]}
	seen := map[string]bool{}
	for rows.Next() {
		var kind, name, table string
		var sqlText sql.NullString
		if err = rows.Scan(&kind, &name, &table, &sqlText); err != nil {
			break
		}
		if kind == "index" && name == "sqlite_autoindex_node_stop_operations_1" && table == "node_stop_operations" && !sqlText.Valid {
			continue
		}
		query := sqlText.String
		if kind != "table" || name != table || !sqlText.Valid {
			err = fmt.Errorf("unknown node-stop schema object")
			break
		}
		if seen[name] || want[name] == "" || query != want[name] {
			err = fmt.Errorf("unknown/altered node-stop schema")
			break
		}
		seen[name] = true
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return state, false, err
	}
	if len(seen) == 0 {
		return state, false, nil
	}
	if len(seen) != len(want) {
		return state, false, fmt.Errorf("partial node-stop schema")
	}
	var count, version int
	if err = db.QueryRow(`SELECT count(*),coalesce(min(version),0) FROM node_stop_schema`).Scan(&count, &version); err != nil || count != 1 || version != 1 {
		return state, false, fmt.Errorf("unknown node-stop schema marker: %w", err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM node_stop_state`).Scan(&count); err != nil || count != 1 {
		return state, false, fmt.Errorf("missing/duplicate desired state: %w", err)
	}
	raw, err := boundedText(db.QueryRow(`SELECT typeof(data),length(CAST(data AS BLOB)),substr(CAST(data AS BLOB),1,65537) FROM node_stop_state WHERE singleton=1`))
	if err != nil {
		return state, false, err
	}
	if err = Decode([]byte(raw), &state); err != nil {
		return state, false, err
	}
	record, err := Lookup(db, state.Key)
	if err != nil {
		return state, false, err
	}
	if err = state.Validate(record); err != nil {
		return state, false, err
	}
	// Reviewed restart changes accepted -> interrupted in the same transaction
	// as Running=true. Crash does not rewrite the durable operation phase.
	// Generic State.Validate still permits intermediate construction in that
	// transaction; a completed durable read must never expose either mixture.
	if state.Running && record.Phase == "accepted" || !state.Running && record.Phase == "interrupted" {
		return state, false, fmt.Errorf("node-stop durable state has an impossible restart phase")
	}
	all, err := ReadRecords(db)
	if err != nil {
		return state, false, err
	}
	if len(all) == 0 || all[len(all)-1].Plan.Key != state.Key {
		return state, false, fmt.Errorf("desired state is not the latest complete stop history")
	}
	for _, older := range all {
		if older.Plan.Target.Host != state.Target.Host || older.Plan.Target.Owner != state.Target.Owner || older.Plan.Target.Home != state.Target.Home || older.Plan.Target.Directory != state.Target.Directory {
			return state, false, fmt.Errorf("stop history crosses authority namespaces")
		}
	}
	return state, true, nil
}

func ReadRecords(db Queryer) ([]Record, error) {
	rows, err := db.Query(`SELECT substr(CAST(key AS BLOB),1,129),typeof(data),length(CAST(data AS BLOB)),substr(CAST(data AS BLOB),1,65537) FROM node_stop_operations ORDER BY key LIMIT 129`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	all := []Record{}
	for rows.Next() {
		var keyBytes, rawBytes []byte
		var kind string
		var size int64
		if err = rows.Scan(&keyBytes, &kind, &size, &rawBytes); err != nil {
			return nil, err
		}
		key := string(keyBytes)
		if !keyText(key) || kind != "text" || size < 1 || size > 64<<10 || int64(len(rawBytes)) != size {
			return nil, fmt.Errorf("stop history row exceeds exact key/text frame bounds")
		}
		var r Record
		if err = Decode(rawBytes, &r); err != nil {
			return nil, err
		}
		if err = r.Validate(); err != nil {
			return nil, err
		}
		if r.Plan.Key != key {
			return nil, fmt.Errorf("stop history row key mismatch")
		}
		all = append(all, r)
		if len(all) > MaxRecords {
			return nil, fmt.Errorf("complete stop history exceeds bound; no rows ignored")
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].DesiredRevision < all[j].DesiredRevision })
	for i, r := range all {
		if r.DesiredRevision != 2+uint64(i)*2 {
			return nil, fmt.Errorf("missing, duplicate or unrepresentable stop revision history")
		}
	}
	return all, nil
}

func Lookup(db Queryer, key string) (Record, error) {
	var r Record
	if !keyText(key) {
		return r, fmt.Errorf("exact original key required")
	}
	raw, err := boundedText(db.QueryRow(`SELECT typeof(data),length(CAST(data AS BLOB)),substr(CAST(data AS BLOB),1,65537) FROM node_stop_operations WHERE key=?`, key))
	if err != nil {
		return r, err
	}
	if err := Decode([]byte(raw), &r); err != nil {
		return r, err
	}
	if err := r.Validate(); err != nil {
		return r, err
	}
	if r.Plan.Key != key {
		return r, fmt.Errorf("node-stop row key differs from original plan")
	}
	return r, nil
}

func (s State) StateDigest() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// BLOB substr limits bytes before driver allocation; a TEXT character limit or
// a size check after Scan would still allocate an oversized corrupt record.
func boundedText(row *sql.Row) (string, error) {
	var kind string
	var size int64
	var raw []byte
	if err := row.Scan(&kind, &size, &raw); err != nil {
		return "", err
	}
	if kind != "text" || size < 1 || size > 64<<10 || int64(len(raw)) != size {
		return "", fmt.Errorf("exact bounded TEXT stop record required")
	}
	return string(raw), nil
}
