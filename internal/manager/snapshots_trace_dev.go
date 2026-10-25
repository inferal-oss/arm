//go:build arm_devtrace

package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type snapshotTraceRow struct {
	Observed        time.Time               `json:"observed"`
	Ordinal         uint64                  `json:"ordinal"`
	Home            string                  `json:"home"`
	Host            string                  `json:"host"`
	Owner           string                  `json:"owner"`
	Runtime         string                  `json:"runtime"`
	SQLView         bool                    `json:"sql_view"`
	Cut             *snapshotTraceCut       `json:"cut"`
	Physical        []snapshotPhysicalTable `json:"physical"`
	Schema          string                  `json:"schema"`
	Action          string                  `json:"action"`
	ID              string                  `json:"id"`
	Transaction     bool                    `json:"transaction"`
	Copies          []json.RawMessage       `json:"copies"`
	Parts           []snapshotTracePart     `json:"parts"`
	Journals        []json.RawMessage       `json:"journals"`
	Leases          []json.RawMessage       `json:"leases"`
	Fences          []string                `json:"fences"`
	Controls        []snapshotStopControl   `json:"controls"`
	CommitPending   bool                    `json:"commit_pending"`
	BoundaryUnknown bool                    `json:"boundary_unknown"`
	Error           string                  `json:"error"`
}
type snapshotTracePart struct {
	Copy string `json:"copy"`
	Part int    `json:"part"`
	Data []byte `json:"data"`
}
type snapshotTraceObserver struct {
	mu      sync.Mutex
	Rows    []snapshotTraceRow
	Bytes   int
	Failure error
	// Synchronous test-only cut hook. Called after the immutable row is retained,
	// outside observer.mu; never receives the original mutable source pointers.
	Hook func(snapshotTraceRow)
}

var snapshotTraceObservers sync.Map // private dev tests only; no env activation

func (m *Manager) traceSnapshotLocked(action, id string) {
	v, ok := snapshotTraceObservers.Load(m.home)
	if !ok {
		return
	}
	o := v.(*snapshotTraceObserver)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, e := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		row := m.snapshotTraceEnvelope(action, id)
		row.Error = e.Error()
		o.append(row)
		return
	}
	row := m.readSnapshotTrace(tx, action, id, false)
	if e = tx.Rollback(); e != nil {
		row.Error = errors.Join(errors.New(row.Error), e).Error()
	}
	o.append(row)
}
func (m *Manager) traceSnapshotTransactionLocked(tx *sql.Tx, action, id string) {
	v, ok := snapshotTraceObservers.Load(m.home)
	if !ok {
		return
	}
	o := v.(*snapshotTraceObserver)
	row := m.readSnapshotTrace(tx, action, id, true)
	o.append(row)
}

type snapshotTraceReader interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func (m *Manager) readSnapshotTrace(db snapshotTraceReader, action, id string, transaction bool) snapshotTraceRow {
	out := m.snapshotTraceEnvelope(action, id)
	out.Transaction, out.SQLView = transaction, true

	read := func(query string, limit int, appendRow func([]byte)) error {
		rows, e := db.Query(query)
		if e != nil {
			return e
		}
		count, total := 0, 0
		for rows.Next() {
			var raw []byte
			if e = rows.Scan(&raw); e != nil {
				break
			}
			count++
			total += len(raw)
			if len(raw) > 1<<20 || count > limit || total > 2<<20 {
				e = fmt.Errorf("raw snapshot trace byte/count bound")
				break
			}
			appendRow(append([]byte(nil), raw...))
		}
		return errorsWithSnapshotTraceRows(e, rows)
	}
	var present int
	e := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='snapshot_copies'`).Scan(&present)
	if e == nil && present == 1 {
		e = read(`SELECT data FROM snapshot_copies ORDER BY id LIMIT 17`, 16, func(raw []byte) { out.Copies = append(out.Copies, raw) })
	}
	if e == nil && present == 1 {
		rows, err := db.Query(`SELECT copy,part,CASE WHEN typeof(data)='blob' AND length(data) BETWEEN 1 AND 8192 THEN data END FROM snapshot_copy_parts ORDER BY copy,part LIMIT 2049`)
		if err != nil {
			e = err
		} else {
			total := 0
			for rows.Next() {
				var p snapshotTracePart
				if err = rows.Scan(&p.Copy, &p.Part, &p.Data); err != nil {
					break
				}
				total += len(p.Data)
				if len(p.Copy) > 32 || len(p.Data) == 0 || len(out.Parts) >= 2048 || total > 2<<20 {
					err = fmt.Errorf("raw snapshot part occupancy bound")
					break
				}
				p.Data = append([]byte(nil), p.Data...)
				out.Parts = append(out.Parts, p)
			}
			e = errorsWithSnapshotTraceRows(err, rows)
		}
	}
	if e == nil && present == 1 {
		e = read(`SELECT data FROM snapshot_copy_journal ORDER BY sequence LIMIT 257`, 256, func(raw []byte) { out.Journals = append(out.Journals, raw) })
	}
	if e == nil {
		e = read(`SELECT data FROM leases ORDER BY id LIMIT 65`, 64, func(raw []byte) { out.Leases = append(out.Leases, raw) })
	}
	if e == nil {
		e = read(`SELECT key FROM cancelled_requests ORDER BY key LIMIT 65`, 64, func(raw []byte) { out.Fences = append(out.Fences, string(raw)) })
	}
	if e == nil {
		out.Physical, e = readSnapshotPhysical(db, present == 1)
	}
	if e != nil {
		out.Error = e.Error()
	}
	return out
}
func errorsWithSnapshotTraceRows(e error, r *sql.Rows) error {
	return errors.Join(e, r.Err(), r.Close())
}
func (m *Manager) snapshotTraceEnvelope(action, id string) snapshotTraceRow {
	row := snapshotTraceRow{Observed: time.Now().UTC(), Schema: "arm.snapshot-reservation.raw/v2", Action: action, ID: id,
		Home: m.home, Host: m.host, Owner: m.owner, Copies: []json.RawMessage{},
		Parts: []snapshotTracePart{}, Journals: []json.RawMessage{}, Leases: []json.RawMessage{},
		Fences: []string{}, Physical: []snapshotPhysicalTable{}}
	if m.snapshots != nil {
		row.Runtime = m.snapshots.runtime
		row.Controls = m.snapshotStopControls()
		row.CommitPending = m.snapshots.commitPending
		row.BoundaryUnknown = m.snapshots.failure != nil
	}
	return row
}
func (m *Manager) traceSnapshotCut(action, id string, cut snapshotTraceCut) {
	v, ok := snapshotTraceObservers.Load(m.home)
	if !ok {
		return
	}
	row := m.snapshotTraceEnvelope(action, id)
	row.Cut = &cut
	v.(*snapshotTraceObserver).append(row)
}
func (o *snapshotTraceObserver) append(row snapshotTraceRow) {
	o.mu.Lock()
	if o.Failure != nil {
		o.mu.Unlock()
		return
	}
	row.Ordinal = uint64(len(o.Rows) + 1)
	raw, e := json.Marshal(row)
	if e == nil && (len(o.Rows) >= 128 || o.Bytes+len(raw) > 4<<20) {
		e = fmt.Errorf("snapshot actual trace occupancy bound")
	}
	if e != nil {
		o.Failure = e
		o.mu.Unlock()
		return
	}
	// Detach from all caller-owned pointer/slice storage before invoking hooks.
	var saved snapshotTraceRow
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e = decoder.Decode(&saved); e != nil {
		o.Failure = e
		o.mu.Unlock()
		return
	}
	o.Bytes += len(raw)
	o.Rows = append(o.Rows, saved)
	hook := o.Hook
	o.mu.Unlock()
	if hook != nil {
		var detached snapshotTraceRow
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&detached) == nil {
			hook(detached)
		}
	}
}

// Closed SQL evidence, including physical types and key/body bindings. This is
// a fixed dev-only capture list, not an arbitrary SQL/column interface.
type snapshotPhysicalCell struct {
	Type  string `json:"type"`
	Value any    `json:"value"`
}
type snapshotPhysicalTable struct {
	Name string                   `json:"name"`
	Rows [][]snapshotPhysicalCell `json:"rows"`
}

func readSnapshotPhysical(db snapshotTraceReader, enabled bool) ([]snapshotPhysicalTable, error) {
	specs := []struct {
		name, query    string
		columns, limit int
	}{
		{"metadata", `SELECT typeof(key),CASE WHEN length(key)<=128 THEN key END,typeof(value),CASE WHEN length(value)<=4096 THEN value END FROM metadata WHERE key IN ('storage_root','snapshot_copy_schema') ORDER BY key LIMIT 3`, 2, 2},
		{"leases", `SELECT typeof(id),CASE WHEN length(id)<=128 THEN id END,typeof(request_key),CASE WHEN length(request_key)<=256 THEN request_key END,typeof(data),CASE WHEN length(data)<=1048576 THEN data END,typeof(run),CASE WHEN length(run)<=1048576 THEN run END FROM leases ORDER BY id LIMIT 65`, 4, 64},
		{"lease_budget_bindings", `SELECT typeof(lease),CASE WHEN length(lease)<=128 THEN lease END,typeof(data),CASE WHEN length(data)<=1048576 THEN data END FROM lease_budget_bindings ORDER BY lease LIMIT 65`, 2, 64},
		{"cancelled_requests", `SELECT typeof(key),CASE WHEN length(key)<=256 THEN key END FROM cancelled_requests ORDER BY key LIMIT 65`, 1, 64},
	}
	if enabled {
		specs = append(specs, struct {
			name, query    string
			columns, limit int
		}{"snapshot_copies", `SELECT typeof(id),CASE WHEN length(id)<=128 THEN id END,typeof(request_key),CASE WHEN length(request_key)<=256 THEN request_key END,typeof(work_lease),CASE WHEN length(work_lease)<=128 THEN work_lease END,typeof(destination_lease),CASE WHEN length(destination_lease)<=128 THEN destination_lease END,typeof(data),CASE WHEN length(data)<=1048576 THEN data END FROM snapshot_copies ORDER BY id LIMIT 17`, 5, 16},
			struct {
				name, query    string
				columns, limit int
			}{"snapshot_copy_parts", `SELECT typeof(copy),CASE WHEN length(copy)<=128 THEN copy END,typeof(part),part,typeof(data),CASE WHEN length(data)<=8192 THEN data END FROM snapshot_copy_parts ORDER BY copy,part LIMIT 2049`, 3, 2048},
			struct {
				name, query    string
				columns, limit int
			}{"snapshot_copy_journal", `SELECT typeof(sequence),sequence,typeof(data),CASE WHEN length(data)<=1048576 THEN data END FROM snapshot_copy_journal ORDER BY sequence LIMIT 257`, 2, 256})
	}
	out := make([]snapshotPhysicalTable, 0, len(specs))
	total := 0
	for _, spec := range specs {
		table := snapshotPhysicalTable{Name: spec.name, Rows: [][]snapshotPhysicalCell{}}
		rows, e := db.Query(spec.query)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			values := make([]any, spec.columns*2)
			targets := make([]any, len(values))
			for i := range values {
				targets[i] = &values[i]
			}
			if e = rows.Scan(targets...); e != nil {
				break
			}
			cells := make([]snapshotPhysicalCell, spec.columns)
			for i := range cells {
				typ, ok := values[2*i].(string)
				if !ok {
					e = fmt.Errorf("invalid physical SQL type observation")
					break
				}
				v := values[2*i+1]
				if b, ok := v.([]byte); ok {
					v = append([]byte(nil), b...)
				}
				cells[i] = snapshotPhysicalCell{Type: typ, Value: v}
			}
			if e != nil {
				break
			}
			raw, err := json.Marshal(cells)
			if err != nil {
				e = err
				break
			}
			total += len(raw)
			if len(table.Rows) >= spec.limit || total > 2<<20 {
				e = fmt.Errorf("physical SQL capture bound")
				break
			}
			table.Rows = append(table.Rows, cells)
		}
		if e = errorsWithSnapshotTraceRows(e, rows); e != nil {
			return nil, e
		}
		out = append(out, table)
	}
	return out, nil
}

func (m *Manager) traceSnapshotMethod(action string, cut snapshotTraceCut) {
	if _, ok := snapshotTraceObservers.Load(m.home); !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.traceSnapshotCut(action, "", cut)
}

func (o *snapshotTraceObserver) setHook(hook func(snapshotTraceRow)) {
	o.mu.Lock()
	o.Hook = hook
	o.mu.Unlock()
}
