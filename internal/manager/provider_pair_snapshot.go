package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"getarm.org/internal/providerpreparation"
)

type providerPairSQLText struct {
	raw  []byte
	kind string
}

func (v providerPairSQLText) text(limit int) (string, error) {
	if v.kind != "text" || len(v.raw) == 0 || len(v.raw) > limit || !utf8.Valid(v.raw) {
		return "", fmt.Errorf("pair SQL text/type bound")
	}
	return string(v.raw), nil
}

type providerPairSQLNumber struct {
	value sql.NullInt64
	kind  string
}

func (v providerPairSQLNumber) integer() (int64, error) {
	if v.kind != "integer" || !v.value.Valid {
		return 0, fmt.Errorf("pair SQL integer required")
	}
	return v.value.Int64, nil
}

type providerPairHeader struct {
	id, key, authority, owner, profile, state, worker, temp                       string
	generation, cpu, memory, disk, revision, reserve, initialBytes, progressBytes int64
}

func scanProviderPairHeader(rows *sql.Rows) (providerPairHeader, error) {
	var h providerPairHeader
	var text [8]providerPairSQLText
	var nums [6]providerPairSQLNumber
	var initial, progress sql.NullInt64
	args := []any{}
	addText := func(i int) { args = append(args, &text[i].raw, &text[i].kind) }
	addNumber := func(i int) { args = append(args, &nums[i].value, &nums[i].kind) }
	for i := 0; i < 5; i++ {
		addText(i)
	}
	addNumber(0)
	for i := 5; i < 8; i++ {
		addText(i)
	}
	for i := 1; i < 6; i++ {
		addNumber(i)
	}
	args = append(args, &initial, &progress)
	if e := rows.Scan(args...); e != nil {
		return h, e
	}
	targets := []*string{&h.id, &h.key, &h.authority, &h.owner, &h.profile, &h.state, &h.worker, &h.temp}
	limits := []int{32, 128, 32, 10, 32, 16, 32, 32}
	for i := range text {
		s, e := text[i].text(limits[i])
		if e != nil {
			return h, e
		}
		*targets[i] = s
	}
	values := []*int64{&h.generation, &h.cpu, &h.memory, &h.disk, &h.revision, &h.reserve}
	for i := range nums {
		n, e := nums[i].integer()
		if e != nil {
			return h, e
		}
		*values[i] = n
	}
	if !initial.Valid || !progress.Valid || initial.Int64 <= 0 || initial.Int64 > providerpreparation.MaxInitialBytes || progress.Int64 <= 0 || progress.Int64 > providerpreparation.MaxProgressBytes {
		return h, fmt.Errorf("pair body byte length required")
	}
	h.initialBytes, h.progressBytes = initial.Int64, progress.Int64
	if !providerPairHexID(h.id) || !providerPairRequestKey(h.key) || !providerPairHexID(h.authority) || !providerPairUID(h.owner) || h.profile != providerpreparation.InertProfile || h.generation != 1 || !providerPairHexID(h.worker) || !providerPairHexID(h.temp) || h.worker == h.temp || h.cpu != 1000 || h.memory != 64<<20 || h.disk != 64<<20 || h.reserve != providerpreparation.RecordReserveBytes {
		return h, fmt.Errorf("pair header profile/identity/quantity")
	}
	if !(h.state == "preparing" && h.revision == 1 || h.state == "revoked" && h.revision == 2 || h.state == "unknown" && (h.revision == 2 || h.revision == 3)) {
		return h, fmt.Errorf("pair header retaining phase")
	}
	return h, nil
}

type providerPairMembership struct{ lease, pair, role string }

// readProviderPairJournalSnapshot reads only the declared paired namespace and
// its indexed protected rows. It is not the C2 census of all ordinary leases,
// complete policy accounting, DDL/format permission or a current owner check.
func readProviderPairJournalSnapshot(ctx context.Context, tx *sql.Tx, host, owner string) (result []providerPairDecoded, err error) {
	if ctx == nil || tx == nil || !providerPairHexID(host) || !providerPairUID(owner) {
		return nil, fmt.Errorf("bounded pair snapshot identity required")
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	var marker providerPairSQLText
	if e := tx.QueryRowContext(ctx, `SELECT substr(CAST(value AS BLOB),1,2),typeof(value) FROM metadata WHERE key=?`, providerpreparation.SchemaKey).Scan(&marker.raw, &marker.kind); e != nil {
		return nil, e
	}
	if s, e := marker.text(1); e != nil || s != "1" {
		return nil, fmt.Errorf("pair schema marker unavailable")
	}
	headers := []providerPairHeader{}
	byID := map[string]providerPairHeader{}
	keys := map[string]bool{}
	expected := map[string]providerPairMembership{}
	var aggregate int64
	rows, e := tx.QueryContext(ctx, providerpreparation.ReadHeadersSQL)
	if e != nil {
		return nil, e
	}
	readHeaders := func() (resultErr error) {
		defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
		for rows.Next() {
			if len(headers) == providerpreparation.MaxJournalRows {
				return fmt.Errorf("pair header sentinel")
			}
			h, e := scanProviderPairHeader(rows)
			if e != nil {
				return e
			}
			if h.authority != host || h.owner != owner || byID[h.id].id != "" || keys[h.key] {
				return fmt.Errorf("pair header authority/duplicate")
			}
			if aggregate > providerpreparation.MaxAggregateBytes-h.reserve {
				return fmt.Errorf("pair aggregate logical reservation bound")
			}
			aggregate += h.reserve
			for i, id := range []string{h.worker, h.temp} {
				if _, ok := expected[id]; ok {
					return fmt.Errorf("member reused across headers")
				}
				expected[id] = providerPairMembership{id, h.id, []string{"worker", "temp"}[i]}
			}
			headers = append(headers, h)
			byID[h.id] = h
			keys[h.key] = true
		}
		return rows.Err()
	}
	if e = readHeaders(); e != nil {
		return nil, e
	}
	// Complete reciprocal membership is checked before allocating any body.
	rows, e = tx.QueryContext(ctx, providerpreparation.ReadMembersSQL)
	if e != nil {
		return nil, e
	}
	readMembers := func() (resultErr error) {
		defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
		seen := map[string]bool{}
		roles := map[[2]string]bool{}
		count := 0
		for rows.Next() {
			if count == providerpreparation.MaxMembers {
				return fmt.Errorf("pair membership sentinel")
			}
			count++
			var a, b, c providerPairSQLText
			var generation providerPairSQLNumber
			if e := rows.Scan(&a.raw, &a.kind, &b.raw, &b.kind, &c.raw, &c.kind, &generation.value, &generation.kind); e != nil {
				return e
			}
			lease, e := a.text(32)
			if e != nil {
				return e
			}
			pair, e := b.text(32)
			if e != nil {
				return e
			}
			role, e := c.text(16)
			if e != nil {
				return e
			}
			g, e := generation.integer()
			if e != nil {
				return e
			}
			want, ok := expected[lease]
			key := [2]string{pair, role}
			if !ok || !providerPairHexID(lease) || !providerPairHexID(pair) || g != 1 || seen[lease] || roles[key] || want.pair != pair || want.role != role {
				return fmt.Errorf("foreign/duplicate/nonreciprocal pair membership")
			}
			seen[lease] = true
			roles[key] = true
		}
		if e := rows.Err(); e != nil {
			return e
		}
		if len(seen) != len(expected) {
			return fmt.Errorf("missing reciprocal pair membership")
		}
		return nil
	}
	if e = readMembers(); e != nil {
		return nil, e
	}
	for _, h := range headers {
		p, e := readProviderPairBody(ctx, tx, h)
		if e != nil {
			return nil, e
		}
		result = append(result, p)
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	return result, nil
}

func readProviderPairBody(ctx context.Context, tx *sql.Tx, h providerPairHeader) (providerPairDecoded, error) {
	var p providerPairDecoded
	var digest, initial, progress providerPairSQLText
	var initialSize, progressSize sql.NullInt64
	if e := tx.QueryRowContext(ctx, providerpreparation.ReadBodySQL, h.id).Scan(&digest.raw, &digest.kind, &initial.kind, &initialSize, &initial.raw, &progress.kind, &progressSize, &progress.raw); e != nil {
		return p, e
	}
	hash, e := digest.text(64)
	if e != nil || !providerPairDigest(hash) {
		return p, fmt.Errorf("pair initial digest required")
	}
	if !initialSize.Valid || initialSize.Int64 != h.initialBytes || int64(len(initial.raw)) != h.initialBytes || initial.kind != "text" || !progressSize.Valid || progressSize.Int64 != h.progressBytes || int64(len(progress.raw)) != h.progressBytes || progress.kind != "text" || providerPairSHA(initial.raw) != hash {
		return p, fmt.Errorf("pair body header/type/hash binding")
	}
	p.initial, e = decodeProviderPairInitial(initial.raw)
	if e != nil {
		return p, e
	}
	if e = decodeProviderPairJSON(progress.raw, &p.progress, providerpreparation.MaxProgressBytes, providerpreparation.MaxEnvelopeDepth, true); e != nil {
		return p, e
	}
	if !providerPairProgressValid(p.progress) || string(p.initial.Pair) != h.id || string(p.initial.OperationKey) != h.key || string(p.initial.Authority) != h.authority || string(p.initial.Owner) != h.owner || string(p.initial.Members[0].Row.ID) != h.worker || string(p.initial.Members[1].Row.ID) != h.temp || string(p.progress.Pair) != h.id || string(p.progress.Phase) != h.state || string(p.progress.Revision) != fmt.Sprint(h.revision) {
		return p, fmt.Errorf("pair journal/header/progress identity")
	}
	for i, id := range []string{h.worker, h.temp} {
		p.rows[i], e = readProviderPairLeaseRow(ctx, tx, id)
		if e != nil {
			return p, e
		}
	}
	if e = validateProviderPairDecoded(&p); e != nil {
		return p, e
	}
	p.rawInitial = append([]byte(nil), initial.raw...)
	p.rawProgress = append([]byte(nil), progress.raw...)
	return p, nil
}

// Unused observation wrapper. It cannot mint or recover providerPairOriginal,
// register native work, supply Remaining or satisfy providerexec entitlement.
func (m *Manager) observeProviderPairJournal(ctx context.Context) (result []providerPairDecoded, resultErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("caller context required")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if e := m.lockProviderPairMembers(ctx); e != nil {
		return nil, e
	}
	defer m.mu.Unlock()
	if m.db == nil || m.closing || m.draining {
		return nil, fmt.Errorf("pair observation unavailable")
	}
	tx, e := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			result = nil
			resultErr = errors.Join(resultErr, e)
		}
		if e := ctx.Err(); e != nil {
			result = nil
			resultErr = errors.Join(resultErr, e)
		}
	}()
	return readProviderPairJournalSnapshot(ctx, tx, m.host, m.owner)
}
