package routing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/yrashk/arm/internal/published"
	"github.com/yrashk/arm/internal/workspace"
)

const MaxEvents = 2048

func Read(ctx context.Context, tx *sql.Tx) (Records, error) {
	out := Records{Roots: []Root{}, Outbox: []Outbox{}, Inbox: []Inbox{}, Events: []Change{}}
	pubs, e := published.Read(ctx, tx)
	if e != nil {
		return out, e
	}
	if pubs.Initialized {
		out.Published = &pubs
	}
	initialized, e := schema(ctx, tx, false)
	if e != nil || !initialized {
		return out, e
	}
	out.Initialized = true
	read := func(table, columns string, limit int, fn func([]string, []byte) error) error {
		order := ""
		if table == "routing_events" {
			order = " ORDER BY sequence"
		}
		rows, e := tx.QueryContext(ctx, fmt.Sprintf("SELECT %s,substr(CAST(data AS BLOB),1,%d) FROM %s%s LIMIT %d", columns, MaxRecordBytes+1, table, order, limit+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			count++
			if count > limit {
				return invalid("routing row count exceeds bounded ledger")
			}
			names, e := rows.Columns()
			if e != nil {
				return e
			}
			keys := make([]string, len(names)-1)
			values := make([]any, len(names))
			for i := range keys {
				values[i] = &keys[i]
			}
			var raw []byte
			values[len(values)-1] = &raw
			if e = rows.Scan(values...); e != nil {
				return e
			}
			if len(raw) > MaxRecordBytes {
				return invalid("routing record exceeds byte bound")
			}
			out.rawBytes += len(raw)
			if out.rawBytes > MaxLedgerBytes {
				return invalid("complete routing ledger exceeds byte bound")
			}
			if e = fn(keys, raw); e != nil {
				return invalid(table + ": " + e.Error())
			}
		}
		return rows.Err()
	}
	if e = read(names[0], "substr(workspace,1,33)", workspace.MaxWorkspaces, func(keys []string, raw []byte) error {
		var v Root
		if e := workspace.Decode(raw, &v); e != nil {
			return e
		}
		if v.Workspace != keys[0] {
			return invalid("root key differs")
		}
		out.Roots = append(out.Roots, v)
		return nil
	}); e != nil {
		return out, e
	}
	if e = read(names[1], "substr(workspace,1,33),substr(endpoint,1,65),substr(key,1,129),substr(message,1,33),substr(CAST(request AS BLOB),1,65537)", workspace.MaxWorkspaces*MaxOutbox, func(keys []string, raw []byte) error {
		var v Outbox
		if e := workspace.Decode(raw, &v); e != nil {
			return e
		}
		out.rawBytes += len(keys[4])
		if out.rawBytes > MaxLedgerBytes {
			return invalid("routing records plus exact requests exceed byte bound")
		}
		intent, _ := json.Marshal(v.Intent)
		if v.Workspace != keys[0] || hash(v.Envelope.From) != keys[1] || v.Intent.Key != keys[2] || v.Envelope.ID != keys[3] || string(intent) != keys[4] {
			return invalid("outbox key/request differs")
		}
		out.Outbox = append(out.Outbox, v)
		return nil
	}); e != nil {
		return out, e
	}
	if e = read(names[2], "substr(workspace,1,33),substr(origin,1,33),substr(endpoint,1,65),substr(key,1,129),substr(message,1,33)", workspace.MaxWorkspaces*MaxMessages, func(keys []string, raw []byte) error {
		var v Inbox
		if e := workspace.Decode(raw, &v); e != nil {
			return e
		}
		if v.Workspace != keys[0] || v.Envelope.Origin != keys[1] || hash(v.Envelope.From) != keys[2] || v.Envelope.Key != keys[3] || v.Envelope.ID != keys[4] {
			return invalid("inbox key differs")
		}
		out.Inbox = append(out.Inbox, v)
		return nil
	}); e != nil {
		return out, e
	}
	if e = read(names[3], "CAST(sequence AS TEXT),substr(workspace,1,33)", workspace.MaxWorkspaces*MaxEvents, func(keys []string, raw []byte) error {
		var v Change
		if e := workspace.Decode(raw, &v); e != nil {
			return e
		}
		n, e := strconv.ParseInt(keys[0], 10, 64)
		if e != nil || n < 1 || v.Workspace != keys[1] {
			return invalid("journal key differs")
		}
		out.Events = append(out.Events, v)
		return nil
	}); e != nil {
		return out, e
	}
	var host string
	if e = tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='host'`).Scan(&host); e != nil {
		return out, e
	}
	for _, root := range out.Roots {
		var raw []byte
		if e = tx.QueryRowContext(ctx, `SELECT substr(CAST(data AS BLOB),1,1048577) FROM workspace_instances WHERE id=?`, root.Workspace).Scan(&raw); e != nil {
			if e == sql.ErrNoRows {
				return out, invalid("routing root has no original workspace")
			}
			return out, e
		}
		var parent workspace.Instance
		if e = workspace.Decode(raw, &parent); e != nil {
			return out, invalid("routing parent evidence is malformed")
		}
		if parent.ID != root.Workspace || parent.Generation != root.Generation || parent.Authority != root.Authority || parent.Authority.Host != host || parent.Self.Fingerprint != root.LocalPin {
			return out, invalid("routing root immutable parent authority differs")
		}
	}
	if e = readGraph(ctx, tx, &out); e != nil {
		return out, e
	}
	if e = Validate(out); e != nil {
		return out, e
	}
	return out, nil
}
func rootOf(v Records, id string) (Root, bool) {
	for _, r := range v.Roots {
		if r.Workspace == id {
			return r, true
		}
	}
	return Root{}, false
}
func outboxOf(v Records, id string, from Endpoint, key string) (Outbox, bool) {
	for _, o := range v.Outbox {
		if o.Workspace == id && o.Envelope.From == from && o.Intent.Key == key {
			return o, true
		}
	}
	return Outbox{}, false
}
func inboxOf(v Records, id, origin string, from Endpoint, key string) (Inbox, bool) {
	for _, o := range v.Inbox {
		if o.Workspace == id && o.Envelope.Origin == origin && o.Envelope.From == from && o.Envelope.Key == key {
			return o, true
		}
	}
	return Inbox{}, false
}
func put(ctx context.Context, tx *sql.Tx, query string, value any, args ...any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	if len(raw) > MaxRecordBytes {
		return invalid("routing row too large")
	}
	args = append(args, string(raw))
	_, e = tx.ExecContext(ctx, query, args...)
	return e
}

// FindOutbox requires the exact local key and never infers a source endpoint
// from the caller. Native host inspection may omit from; ambiguity refuses.
func FindOutbox(v Records, id, key string) (Outbox, bool, error) {
	var out Outbox
	found := false
	for _, o := range v.Outbox {
		if o.Workspace == id && o.Intent.Key == key {
			if found {
				return Outbox{}, false, invalid("outbound key is ambiguous across retained endpoints")
			}
			out, found = o, true
		}
	}
	return out, found, nil
}
