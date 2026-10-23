package sharelease

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxEvidenceBytes includes worst-case remaining mutable record growth. Together
// with manifest/journal bounds this limits startup/retained evidence; it is an
// additional ceiling, not a promise all4096 maximum-size operations will fit.
const MaxEvidenceBytes = 16 << 20

type OperationKey struct{ Principal, Key string }

func (q Control) Principal() string { return q.Source + ":" + q.SourceOwner }

type JournalRow struct {
	Sequence int64
	Record   Journal
	RawBytes int
}
type Snapshot struct {
	Enabled                                       bool
	Publications                                  map[string]Publication
	Consumers                                     map[OperationKey]Consumer
	Fences                                        map[OperationKey]Fence
	Journal                                       []JournalRow
	Capacity                                      JournalCapacity
	ManifestRaw, ManifestStored, EvidenceReserved int64
}

func newSnapshot() Snapshot {
	return Snapshot{Publications: map[string]Publication{}, Consumers: map[OperationKey]Consumer{}, Fences: map[OperationKey]Fence{}}
}

// Initialize is called only inside the manager's already admitted ordinary
// lease transaction. Reads never initialize a group or silently repair evidence.
func Initialize(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range SchemaStatements() {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('share_schema','2')`)
	return err
}

// Load uses one bounded consistent SQL snapshot. Call outside Manager.mu; startup
// must complete it BEFORE any provider recovery/cleanup. It grants no filesystem
// or process authority. Manager must separately bind exact current lease records.
func Load(ctx context.Context, db *sql.DB, authority, owner string) (Snapshot, error) {
	out := newSnapshot()
	if !id(authority) || !uid(owner) {
		return out, fmt.Errorf("exact share authority required")
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	present, err := checkSchema(ctx, tx)
	if err != nil || !present {
		return out, err
	}
	out.Enabled = true
	var loadedBytes int64
	read := func(table string, max int, accept func([]string, []byte) error) error {
		// All table/column selectors are fixed local literals, not caller SQL.
		var query string
		switch table {
		case "share_publications":
			query = `SELECT CASE WHEN length(CAST(id AS BLOB))<=32 THEN id END,data FROM share_publications ORDER BY id LIMIT ?`
		case "share_consumers":
			query = `SELECT CASE WHEN length(CAST(principal AS BLOB))<=43 THEN principal END,CASE WHEN length(CAST(key AS BLOB))<=128 THEN key END,CASE WHEN length(CAST(lease AS BLOB))<=32 THEN lease END,data FROM share_consumers ORDER BY principal,key LIMIT ?`
		case "share_key_fences":
			query = `SELECT CASE WHEN length(CAST(principal AS BLOB))<=43 THEN principal END,CASE WHEN length(CAST(key AS BLOB))<=128 THEN key END,CASE WHEN length(CAST(control_sha256 AS BLOB))<=64 THEN control_sha256 END,CASE WHEN length(CAST(state AS BLOB))<=16 THEN state END,CASE WHEN length(CAST(COALESCE(lease,'') AS BLOB))<=32 THEN COALESCE(lease,'') END,data FROM share_key_fences ORDER BY principal,key LIMIT ?`
		default:
			return fmt.Errorf("unknown local share table")
		}
		// Bound bytes BEFORE scanning potentially hostile TEXT. Casting to BLOB
		// counts actual bytes including NUL/multibyte text, unlike SQLite length(TEXT).
		var maxColumns int
		switch table {
		case "share_publications":
			maxColumns = 1
		case "share_consumers":
			maxColumns = 3
		case "share_key_fences":
			maxColumns = 5
		}
		bounds, err := tx.QueryContext(ctx, `SELECT length(CAST(data AS BLOB)) FROM `+table+` LIMIT ?`, max+1)
		if err != nil {
			return err
		}
		count := 0
		var bytes int64
		for bounds.Next() {
			var n int64
			if err = bounds.Scan(&n); err != nil {
				bounds.Close()
				return err
			}
			count++
			if count > max || n < 1 || n > MaxRecordBytes {
				bounds.Close()
				return fmt.Errorf("share record count/byte bound exceeded")
			}
			bytes += n
		}
		err = bounds.Err()
		bounds.Close()
		if err != nil {
			return err
		}
		loadedBytes += bytes
		if loadedBytes > MaxEvidenceBytes {
			return fmt.Errorf("share evidence byte bound exceeded")
		}
		rows, err := tx.QueryContext(ctx, query, max+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			keys := make([]string, maxColumns)
			dest := make([]any, maxColumns+1)
			for i := range keys {
				dest[i] = &keys[i]
			}
			var raw []byte
			dest[maxColumns] = &raw
			if err = rows.Scan(dest...); err != nil {
				return err
			}
			if len(raw) > MaxRecordBytes {
				return fmt.Errorf("share record changed bounds")
			}
			if err = accept(keys, raw); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	hashes := map[string]string{}
	revisions := map[string]int64{}
	updates := map[string]time.Time{}
	retain := func(entity, identity string, revision int64, at time.Time, raw []byte, reserve bool) {
		key := entity + ":" + identity
		h := sha256.Sum256(raw)
		hashes[key] = hex.EncodeToString(h[:])
		revisions[key] = revision
		updates[key] = at
		if reserve {
			out.EvidenceReserved += MaxRecordBytes
		} else {
			out.EvidenceReserved += int64(len(raw))
		}
	}
	err = read("share_publications", MaxPublications, func(keys []string, raw []byte) error {
		p, e := DecodePublication(raw)
		if e != nil {
			return e
		}
		if p.ID != keys[0] || p.Authority != authority || p.Owner != owner {
			return fmt.Errorf("publication SQL/current authority binding differs")
		}
		out.Publications[p.ID] = p
		out.Capacity.ReservedSlots += p.JournalSlots
		retain("publication", p.ID, p.Revision, p.Updated, raw, p.Retired == nil)
		r, s, e := ManifestReservation(p)
		if e != nil {
			return e
		}
		out.ManifestRaw += r
		out.ManifestStored += s
		return nil
	})
	if err != nil {
		return out, err
	}
	err = read("share_consumers", MaxConsumers, func(keys []string, raw []byte) error {
		c, e := DecodeConsumer(raw)
		if e != nil {
			return e
		}
		q := c.Admission.Control
		if keys[0] != q.Principal() || keys[1] != q.Key || keys[2] != c.Admission.Lease || q.Target != authority || q.TargetOwner != owner {
			return fmt.Errorf("consumer SQL/current authority binding differs")
		}
		out.Consumers[OperationKey{keys[0], keys[1]}] = c
		out.Capacity.ReservedSlots += c.JournalSlots
		retain("consumer", c.Admission.Lease, c.Revision, c.Updated, raw, c.State != "terminal")
		return nil
	})
	if err != nil {
		return out, err
	}
	err = read("share_key_fences", MaxConsumers, func(keys []string, raw []byte) error {
		f, e := DecodeFence(raw)
		if e != nil {
			return e
		}
		q := f.Control
		if keys[0] != q.Principal() || keys[1] != q.Key || keys[2] != f.ControlSHA256 || keys[3] != f.State || keys[4] != f.Lease || q.Target != authority || q.TargetOwner != owner {
			return fmt.Errorf("fence SQL/current authority binding differs")
		}
		out.Fences[OperationKey{keys[0], keys[1]}] = f
		retain("fence", f.ControlSHA256, 1, f.At, raw, false)
		return nil
	})
	if err != nil {
		return out, err
	}
	if len(out.Publications) == 0 && len(out.Fences) == 0 {
		return out, fmt.Errorf("established share namespace has no first immutable receipt")
	}
	if out.EvidenceReserved > MaxEvidenceBytes || out.ManifestRaw > MaxManifestRawBytes {
		return out, fmt.Errorf("retained share evidence/future manifest capacity exceeded")
	}
	if err = out.validateRelations(); err != nil {
		return out, err
	}
	if err = readManifestParts(ctx, tx, out.Publications); err != nil {
		return out, err
	}
	if err = out.readJournal(ctx, tx, authority, owner, hashes, revisions, updates); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return out, nil
}
func (s Snapshot) validateRelations() error {
	leases := map[string]bool{}
	keys := map[string]bool{}
	publishKeys := map[string]bool{}
	for _, p := range s.Publications {
		if leases[p.Lease] || keys[p.Request.Key] {
			return fmt.Errorf("duplicate publication lease/preparation key")
		}
		if p.Publish != nil {
			if publishKeys[p.Publish.Key] {
				return fmt.Errorf("duplicate immutable publication key")
			}
			publishKeys[p.Publish.Key] = true
		}
		leases[p.Lease] = true
		keys[p.Request.Key] = true
	}
	for k, c := range s.Consumers {
		p, ok := s.Publications[c.Admission.Control.Share]
		q := c.Admission.Control
		if !ok || p.Generation != q.Generation || p.Summary == nil || p.Summary.SHA256 != q.ManifestSHA256 || p.Published == nil || c.Admission.At.Before(*p.Published) || c.Admission.Expires.After(p.Expires) {
			return fmt.Errorf("consumer publication generation/lifetime differs")
		}
		if p.Retired != nil && c.Ended != nil && c.Ended.After(*p.Retired) {
			return fmt.Errorf("publication retired before consumer joined")
		}
		if c.State != "terminal" && p.Retired != nil {
			return fmt.Errorf("retired share retains unsettled consumer")
		}
		if leases[c.Admission.Lease] {
			return fmt.Errorf("duplicate share obligation lease")
		}
		leases[c.Admission.Lease] = true
		f, ok := s.Fences[k]
		ah, err := hash(c.Admission)
		if !ok || err != nil || f.State != "admitted" || f.Lease != c.Admission.Lease || f.AdmissionSHA256 != ah || f.At != c.Admission.At || f.Control != q {
			return fmt.Errorf("consumer exact admitted key fence missing")
		}
	}
	for k, f := range s.Fences {
		if f.State == "admitted" {
			if _, ok := s.Consumers[k]; !ok {
				return fmt.Errorf("admitted fence has no obligation")
			}
		} else if _, ok := s.Consumers[k]; ok {
			return fmt.Errorf("sealed key was admitted")
		}
	}
	return nil
}
func checkSchema(ctx context.Context, tx *sql.Tx) (bool, error) {
	var marker string
	err := tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='share_schema'`).Scan(&marker)
	absent := errors.Is(err, sql.ErrNoRows)
	if err != nil && !absent {
		return false, err
	}
	if !absent && marker != "2" {
		return false, fmt.Errorf("unknown share schema")
	}
	tables := []string{"share_publications", "share_manifest_parts", "share_consumers", "share_key_fences", "share_journal"}
	expected := map[string]string{}
	for i, name := range tables {
		expected[name] = SchemaStatements()[i]
	}
	rows, err := tx.QueryContext(ctx, `SELECT CASE WHEN length(CAST(type AS BLOB))<=32 THEN type END,CASE WHEN length(CAST(name AS BLOB))<=128 THEN name END,CASE WHEN length(CAST(tbl_name AS BLOB))<=64 THEN tbl_name END,CASE WHEN length(CAST(COALESCE(sql,'') AS BLOB))<=4096 THEN COALESCE(sql,'') END FROM sqlite_schema WHERE name GLOB 'share_*' OR tbl_name GLOB 'share_*' LIMIT 32`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	indices := map[string]bool{"sqlite_autoindex_share_publications_1": true, "sqlite_autoindex_share_manifest_parts_1": true, "sqlite_autoindex_share_consumers_1": true, "sqlite_autoindex_share_consumers_2": true, "sqlite_autoindex_share_key_fences_1": true, "sqlite_autoindex_share_key_fences_2": true}
	count := 0
	for rows.Next() {
		var typ, name, table, statement string
		if err = rows.Scan(&typ, &name, &table, &statement); err != nil {
			return false, err
		}
		count++
		if absent {
			return false, fmt.Errorf("unmarked partial share group")
		}
		if want, ok := expected[name]; ok && typ == "table" && table == name && statement == want {
			delete(expected, name)
			continue
		}
		if indices[name] && typ == "index" && statement == "" && strings.HasPrefix(name, "sqlite_autoindex_"+table+"_") {
			delete(indices, name)
			continue
		}
		return false, fmt.Errorf("unknown or altered share table/index/trigger")
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	if absent && count == 0 {
		return false, nil
	}
	if len(expected) != 0 || len(indices) != 0 {
		return false, fmt.Errorf("incomplete share group")
	}
	return true, nil
}
