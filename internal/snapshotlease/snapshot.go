package snapshotlease

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func SchemaStatements() []string {
	return []string{
		`CREATE TABLE snapshot_copies (id TEXT PRIMARY KEY,request_key TEXT NOT NULL UNIQUE,work_lease TEXT NOT NULL UNIQUE,destination_lease TEXT NOT NULL UNIQUE,data TEXT NOT NULL)`,
		`CREATE TABLE snapshot_copy_parts (copy TEXT NOT NULL,part INTEGER NOT NULL,data BLOB NOT NULL,PRIMARY KEY(copy,part))`,
		`CREATE TABLE snapshot_copy_journal (sequence INTEGER PRIMARY KEY AUTOINCREMENT,data TEXT NOT NULL)`,
	}
}
func Initialize(ctx context.Context, tx *sql.Tx) error {
	for _, q := range SchemaStatements() {
		if _, e := tx.ExecContext(ctx, q); e != nil {
			return e
		}
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('snapshot_copy_schema','1')`)
	return e
}

type Snapshot struct {
	Enabled                    bool
	Copies                     map[string]Record
	Requests                   map[string]Request
	Raw                        map[string][]byte
	RequestRaw                 map[string][]byte
	Journal                    []Journal
	JournalBytes, RequestBytes int
	ReservedSlots              int
}

func empty() Snapshot {
	return Snapshot{Copies: map[string]Record{}, Requests: map[string]Request{}, Raw: map[string][]byte{}, RequestRaw: map[string][]byte{}}
}
func schema(ctx context.Context, tx *sql.Tx) (bool, error) {
	var marker string
	e := tx.QueryRowContext(ctx, `SELECT CASE WHEN typeof(value)='text' AND length(CAST(value AS BLOB))<=16 THEN value END FROM metadata WHERE key='snapshot_copy_schema'`).Scan(&marker)
	absent := errors.Is(e, sql.ErrNoRows)
	if e != nil && !absent {
		return false, e
	}
	var markerCount int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT key FROM metadata WHERE lower(key)='snapshot_copy_schema' LIMIT 3)`).Scan(&markerCount); e != nil {
		return false, e
	}
	if markerCount > 1 || absent && markerCount != 0 {
		return false, fmt.Errorf("aliased snapshot schema marker")
	}
	want := map[string]string{}
	for i, n := range []string{"snapshot_copies", "snapshot_copy_parts", "snapshot_copy_journal"} {
		want[n] = SchemaStatements()[i]
	}
	auto := map[string]bool{"sqlite_autoindex_snapshot_copies_1": false, "sqlite_autoindex_snapshot_copies_2": false, "sqlite_autoindex_snapshot_copies_3": false, "sqlite_autoindex_snapshot_copies_4": false, "sqlite_autoindex_snapshot_copy_parts_1": false}
	rows, e := tx.QueryContext(ctx, `SELECT CASE WHEN length(CAST(type AS BLOB))<=16 THEN type END,CASE WHEN length(CAST(name AS BLOB))<=128 THEN name END,CASE WHEN length(CAST(tbl_name AS BLOB))<=128 THEN tbl_name END,CASE WHEN length(CAST(sql AS BLOB))<=4096 THEN sql END FROM sqlite_schema WHERE lower(name) GLOB 'snapshot_*' OR lower(tbl_name) GLOB 'snapshot_*' ORDER BY name LIMIT 10`)
	if e != nil {
		return false, e
	}
	seen := 0
	for rows.Next() {
		var kind, n, table string
		var raw sql.NullString
		if e = rows.Scan(&kind, &n, &table, &raw); e != nil {
			break
		}
		seen++
		if absent || marker != "1" {
			e = fmt.Errorf("snapshot optional schema marker differs")
			break
		}
		if kind == "table" && n == table && raw.Valid && want[n] == raw.String {
			delete(want, n)
			continue
		}
		if kind == "index" && !raw.Valid {
			if v, ok := auto[n]; ok && !v && ((table == "snapshot_copies" && len(n) > len("sqlite_autoindex_snapshot_copies_")) || (table == "snapshot_copy_parts" && n == "sqlite_autoindex_snapshot_copy_parts_1")) {
				auto[n] = true
				continue
			}
		}
		e = fmt.Errorf("unknown or changed snapshot catalog object")
		break
	}
	e = errors.Join(e, rows.Err(), rows.Close())
	if e != nil {
		return false, e
	}
	if absent && seen == 0 {
		return false, nil
	}
	if marker != "1" || len(want) != 0 || seen != 8 {
		return false, fmt.Errorf("partial snapshot optional catalog")
	}
	for _, v := range auto {
		if !v {
			return false, fmt.Errorf("missing snapshot uniqueness index")
		}
	}
	return true, nil
}

// Load grants no native authority. It uses one bounded, consistent SQL read;
// complete validation is mandatory before generic recovery sees internal leases.
func Load(ctx context.Context, db *sql.DB, authority, owner, home string) (out Snapshot, resultErr error) {
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return empty(), e
	}
	defer func() {
		e := tx.Rollback()
		if e != nil && !errors.Is(e, sql.ErrTxDone) {
			resultErr = errors.Join(resultErr, e)
		}
	}()
	return ReadTransaction(ctx, tx, authority, owner, home)
}

func ReadTransaction(ctx context.Context, tx *sql.Tx, authority, owner, home string) (out Snapshot, resultErr error) {
	out = empty()
	var e error
	out.Enabled, e = schema(ctx, tx)
	if e != nil {
		return out, e
	}
	if !out.Enabled {
		return out, ValidateLeases(ctx, tx, out, home)
	}
	rows, e := tx.QueryContext(ctx, `SELECT CASE WHEN typeof(id)='text' AND length(CAST(id AS BLOB))=32 THEN id END,CASE WHEN typeof(request_key)='text' AND length(CAST(request_key AS BLOB)) BETWEEN 1 AND 128 THEN request_key END,CASE WHEN typeof(work_lease)='text' AND length(CAST(work_lease AS BLOB))=32 THEN work_lease END,CASE WHEN typeof(destination_lease)='text' AND length(CAST(destination_lease AS BLOB))=32 THEN destination_lease END,CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB)) BETWEEN 1 AND 16384 THEN data END FROM snapshot_copies ORDER BY id LIMIT 17`)
	if e != nil {
		return out, e
	}
	keys := map[string]bool{}
	leases := map[string]bool{}
	for rows.Next() {
		var id, key, work, destination string
		var raw []byte
		if e = rows.Scan(&id, &key, &work, &destination, &raw); e != nil {
			break
		}
		if len(out.Copies) >= MaxCopies {
			e = fmt.Errorf("snapshot retained history full")
			break
		}
		r, err := Decode[Record](raw, MaxRecordBytes)
		if err != nil {
			e = err
			break
		}
		if e = r.Validate(); e != nil {
			break
		}
		if r.ID != id || r.Key != key || r.WorkLease != work || r.DestinationLease != destination || r.Authority != authority || r.Owner != owner || keys[key] || leases[work] || leases[destination] {
			e = fmt.Errorf("snapshot SQL/authority/lease identity differs")
			break
		}
		keys[key] = true
		leases[work] = true
		leases[destination] = true
		out.Copies[id] = r
		out.Raw[id] = append([]byte(nil), raw...)
		out.ReservedSlots += r.FinalSlots
	}
	e = errors.Join(e, rows.Err(), rows.Close())
	if e != nil {
		return out, e
	}
	rows, e = tx.QueryContext(ctx, `SELECT CASE WHEN typeof(copy)='text' AND length(CAST(copy AS BLOB))=32 THEN copy END,CASE WHEN typeof(part)='integer' AND part BETWEEN 0 AND 127 THEN part END,CASE WHEN typeof(data)='blob' AND length(data) BETWEEN 1 AND 8192 THEN data END FROM snapshot_copy_parts ORDER BY copy,part LIMIT 2049`)
	if e != nil {
		return out, e
	}
	parts := map[string]int{}
	count := 0
	for rows.Next() {
		var id string
		var index int
		var data []byte
		if e = rows.Scan(&id, &index, &data); e != nil {
			break
		}
		count++
		r, ok := out.Copies[id]
		if !ok || count > MaxCopies*MaxParts || index != parts[id] || index >= r.RequestParts || len(data) == 0 || len(data) > PartBytes || index < r.RequestParts-1 && len(data) != PartBytes {
			e = fmt.Errorf("snapshot request part domain differs")
			break
		}
		out.RequestBytes += len(data)
		if out.RequestBytes > MaxRequestTotal {
			e = fmt.Errorf("snapshot aggregate request bytes exceeded")
			break
		}
		out.RequestRaw[id] = append(out.RequestRaw[id], data...)
		parts[id]++
	}
	e = errors.Join(e, rows.Err(), rows.Close())
	if e != nil {
		return out, e
	}
	for id, r := range out.Copies {
		raw := out.RequestRaw[id]
		q, err := Decode[Request](raw, MaxRequestBytes)
		if err != nil {
			return out, err
		}
		if err = q.Validate(); err != nil {
			return out, err
		}
		if parts[id] != r.RequestParts || len(raw) != r.RequestBytes || Hash(raw) != r.RequestSHA256 || q.Key != r.Key || q.Source != r.Source || q.SourceRevision != r.SourceRevision || !r.WorkExpires.Equal(r.Created.Add(q.WorkTTL)) || !r.DestinationExpires.Equal(r.Created.Add(q.Retention)) {
			return out, fmt.Errorf("snapshot complete request binding differs")
		}
		out.Requests[id] = q
	}
	rows, e = tx.QueryContext(ctx, `SELECT sequence,CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB)) BETWEEN 1 AND 8192 THEN data END FROM snapshot_copy_journal ORDER BY sequence LIMIT 257`)
	if e != nil {
		return out, e
	}
	previous := map[string]string{}
	revisions := map[string]int64{}
	var last int64
	for rows.Next() {
		var sequence int64
		var raw []byte
		if e = rows.Scan(&sequence, &raw); e != nil {
			break
		}
		j, err := Decode[Journal](raw, MaxJournalRecord)
		if err != nil {
			e = err
			break
		}
		r, ok := out.Copies[j.Copy]
		if !ok || j.Schema != 1 || sequence <= last || j.Revision != revisions[j.Copy]+1 || j.Before != previous[j.Copy] || !Digest(j.After) || j.Revision > r.Revision || j.Revision == 1 && j.Transition != "reserved-no-work" || j.Revision == 2 && j.Transition != "cancelled-no-work" || j.Revision > 2 {
			e = fmt.Errorf("snapshot journal chain differs")
			break
		}
		last = sequence
		previous[j.Copy] = j.After
		revisions[j.Copy] = j.Revision
		out.Journal = append(out.Journal, j)
		out.JournalBytes += len(raw)
		if len(out.Journal)+out.ReservedSlots > MaxJournalRows || out.JournalBytes+out.ReservedSlots*MaxJournalRecord > MaxJournalBytes {
			e = fmt.Errorf("snapshot journal capacity exceeded")
			break
		}
	}
	e = errors.Join(e, rows.Err(), rows.Close())
	if e != nil {
		return out, e
	}
	for id, r := range out.Copies {
		if revisions[id] != r.Revision || previous[id] != Hash(out.Raw[id]) {
			return out, fmt.Errorf("snapshot record lacks complete journal provenance")
		}
		initial := r
		initial.Phase = "reserved-no-work"
		initial.Revision = 1
		initial.FinalSlots = 1
		initial.Decision = ""
		initial.Updated = initial.Created
		initialRaw, err := Canonical(initial, MaxRecordBytes)
		if err != nil {
			return out, err
		}
		for _, j := range out.Journal {
			if j.Copy == id && j.Revision == 1 && j.After != Hash(initialRaw) {
				return out, fmt.Errorf("snapshot initial admission journal differs")
			}
		}
	}
	if len(out.Copies) == 0 {
		return out, fmt.Errorf("empty initialized snapshot namespace")
	}
	return out, ValidateLeases(ctx, tx, out, home)
}

func WriteAdmission(ctx context.Context, tx *sql.Tx, r Record, rawRequest []byte) error {
	if e := r.Validate(); e != nil {
		return e
	}
	q, e := Decode[Request](rawRequest, MaxRequestBytes)
	if e != nil || q.Validate() != nil || q.Key != r.Key || q.Source != r.Source || q.SourceRevision != r.SourceRevision || !r.WorkExpires.Equal(r.Created.Add(q.WorkTTL)) || !r.DestinationExpires.Equal(r.Created.Add(q.Retention)) || len(rawRequest) != r.RequestBytes || Hash(rawRequest) != r.RequestSHA256 {
		return fmt.Errorf("invalid immutable snapshot request")
	}
	raw, e := Canonical(r, MaxRecordBytes)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO snapshot_copies(id,request_key,work_lease,destination_lease,data) VALUES(?,?,?,?,?)`, r.ID, r.Key, r.WorkLease, r.DestinationLease, string(raw)); e != nil {
		return e
	}
	for index, start := 0, 0; start < len(rawRequest); index, start = index+1, start+PartBytes {
		part := rawRequest[start:min(start+PartBytes, len(rawRequest))]
		if _, e = tx.ExecContext(ctx, `INSERT INTO snapshot_copy_parts(copy,part,data) VALUES(?,?,?)`, r.ID, index, part); e != nil {
			return e
		}
	}
	return writeJournal(ctx, tx, Journal{Schema: 1, Copy: r.ID, Revision: 1, Transition: r.Phase, After: Hash(raw)})
}
func WriteCancel(ctx context.Context, tx *sql.Tx, old, next Record) error {
	if old.Validate() != nil || next.Validate() != nil || old.Phase != "reserved-no-work" || next.Phase != "cancelled-no-work" {
		return fmt.Errorf("invalid snapshot cancellation")
	}
	a, b := old, next
	b.Phase = a.Phase
	b.Revision = a.Revision
	b.FinalSlots = a.FinalSlots
	b.Decision = a.Decision
	b.Updated = a.Updated
	x, _ := Canonical(a, MaxRecordBytes)
	y, _ := Canonical(b, MaxRecordBytes)
	if !bytes.Equal(x, y) {
		return fmt.Errorf("snapshot cancellation changed immutable scope")
	}
	raw, e := Canonical(next, MaxRecordBytes)
	if e != nil {
		return e
	}
	result, e := tx.ExecContext(ctx, `UPDATE snapshot_copies SET data=? WHERE id=? AND data=?`, string(raw), old.ID, string(x))
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil || n != 1 {
		return fmt.Errorf("snapshot cancellation CAS failed")
	}
	return writeJournal(ctx, tx, Journal{Schema: 1, Copy: old.ID, Revision: 2, Transition: next.Phase, Before: Hash(x), After: Hash(raw)})
}
func writeJournal(ctx context.Context, tx *sql.Tx, j Journal) error {
	raw, e := Canonical(j, MaxJournalRecord)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO snapshot_copy_journal(data) VALUES(?)`, string(raw))
	return e
}
