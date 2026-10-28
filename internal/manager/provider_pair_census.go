package manager

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/providerpreparation"
)

const providerPairC2Rows = 4096
const providerPairC2LeaseBytes = 64 << 10
const providerPairC2BudgetBytes = 16 << 10
const providerPairC2TotalBytes = 32 << 20

type providerPairC2Amounts struct {
	resources providerPairQuantity
	pools     map[string]int64
}

// Covered means only that this bounded ledger snapshot was fully accounted.
// It carries neither live capacity nor permission, a Remaining value or owner.
// Unknown totals are retained observations and MUST NOT be used as headroom.
type providerPairC2Projection struct {
	state                         string
	physical                      providerPairQuantity
	pools                         map[string]int64
	budgets                       map[string]providerPairC2Amounts
	logicalBytes                  int64
	ordinaryRows, pairs, problems int
	problem                       error
}

func (p *providerPairC2Projection) fail(e error) {
	if e != nil {
		p.state = "unknown"
		p.problems++
		if p.problem == nil {
			p.problem = e
		}
	}
}
func uint32OwnerForC2(owner string) uint32 {
	n, _ := strconv.ParseUint(owner, 10, 32)
	return uint32(n)
}
func providerPairC2Add(a *providerPairQuantity, b providerPairQuantity) error {
	var bad bool
	for _, v := range [][2]*int64{{&a.cpuMillis, &b.cpuMillis}, {&a.memoryBytes, &b.memoryBytes}, {&a.diskBytes, &b.diskBytes}} {
		if *v[1] < 0 {
			bad = true
			continue
		}
		if *v[0] > math.MaxInt64-*v[1] {
			*v[0] = math.MaxInt64
			bad = true
		} else {
			*v[0] += *v[1]
		}
	}
	if bad {
		return fmt.Errorf("census quantity overflow/negative")
	}
	return nil
}
func providerPairC2AddPool(m map[string]int64, id string, n int64) error {
	if n < 0 || m[id] > math.MaxInt64-n {
		m[id] = math.MaxInt64
		return fmt.Errorf("census pool overflow/negative")
	}
	m[id] += n
	return nil
}
func (p *providerPairC2Projection) charge(idPath []string, q providerPairQuantity, pools map[string]int64) {
	p.fail(providerPairC2Add(&p.physical, q))
	for _, id := range idPath {
		v := p.budgets[id]
		if v.pools == nil {
			v.pools = map[string]int64{}
		}
		p.fail(providerPairC2Add(&v.resources, q))
		for pool, n := range pools {
			p.fail(providerPairC2AddPool(v.pools, pool, n))
		}
		p.budgets[id] = v
	}
}

type providerPairC2Header struct {
	id, key                            string
	keyNull, hasBudget, hasPoolBinding bool
	runKind                            string
	dataBytes, runBytes                int64
}
type providerPairC2Reader struct {
	ctx         context.Context
	tx          *sql.Tx
	host, owner string
	remaining   int64
	budgetCache map[string]model.Budget
	poolCache   map[string]model.Pool
}

// Shared raw budget includes ordinary body/run/sidecar bytes and all additional
// policy/lineage bytes. SQL CASE refuses oversize BEFORE returning payloads.
func (r *providerPairC2Reader) raw(table, keyColumn, id string, limit int) ([]byte, error) {
	switch table + "/" + keyColumn {
	case "leases/id", "lease_budget_bindings/lease", "budgets/id", "pool_claim_bindings/lease", "pools/id", "pool_lineage/pool":
	default:
		return nil, fmt.Errorf("private census table")
	}
	var kind string
	var n sql.NullInt64
	var raw []byte
	query := `SELECT typeof(data),length(CAST(data AS BLOB)),CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB)) BETWEEN 1 AND ? AND length(CAST(data AS BLOB))<=? THEN CAST(data AS BLOB) END FROM ` + table + ` WHERE ` + keyColumn + `=?`
	if e := r.tx.QueryRowContext(r.ctx, query, limit, r.remaining, id).Scan(&kind, &n, &raw); e != nil {
		return nil, e
	}
	if kind != "text" || !n.Valid || n.Int64 < 1 || n.Int64 > int64(limit) || n.Int64 > r.remaining || int64(len(raw)) != n.Int64 {
		return nil, fmt.Errorf("census raw TEXT/byte bound")
	}
	r.remaining -= n.Int64
	return raw, nil
}
func (r *providerPairC2Reader) headers() (headers []providerPairC2Header, err error) {
	rows, e := r.tx.QueryContext(r.ctx, `SELECT substr(CAST(id AS BLOB),1,33),typeof(id),substr(CAST(request_key AS BLOB),1,129),typeof(request_key),typeof(data),length(CAST(data AS BLOB)),typeof(run),length(CAST(run AS BLOB)) FROM leases ORDER BY rowid LIMIT 4097`)
	if e != nil {
		return nil, e
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	ids, keys := map[string]bool{}, map[string]bool{}
	var total int64
	for rows.Next() {
		if len(headers) == providerPairC2Rows {
			return nil, fmt.Errorf("ordinary row sentinel")
		}
		var id, key []byte
		var it, kt, dt, rt string
		var dn, rn sql.NullInt64
		if e = rows.Scan(&id, &it, &key, &kt, &dt, &dn, &rt, &rn); e != nil {
			return nil, e
		}
		if it != "text" || !providerPairHexID(string(id)) || ids[string(id)] || dt != "text" || !dn.Valid || dn.Int64 < 1 || dn.Int64 > providerPairC2LeaseBytes {
			return nil, fmt.Errorf("ordinary physical tuple/type/bytes")
		}
		if kt != "null" && (kt != "text" || !providerPairRequestKey(string(key)) || keys[string(key)]) {
			return nil, fmt.Errorf("ordinary physical request key")
		}
		if kt == "null" && len(key) != 0 {
			return nil, fmt.Errorf("ordinary NULL request key")
		}
		n := int64(0)
		if rt != "null" {
			if rt != "text" || !rn.Valid || rn.Int64 < 1 || rn.Int64 > providerPairC2LeaseBytes {
				return nil, fmt.Errorf("ordinary run descriptor bound")
			}
			n = rn.Int64
		}
		if dn.Int64 > providerPairC2TotalBytes-total-n {
			return nil, fmt.Errorf("ordinary aggregate bytes")
		}
		total += dn.Int64 + n
		headers = append(headers, providerPairC2Header{id: string(id), key: string(key), keyNull: kt == "null", runKind: rt, dataBytes: dn.Int64, runBytes: n})
		ids[string(id)] = true
		if kt != "null" {
			keys[string(key)] = true
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	// Every sidecar key is included, including terminal/orphan rows. Nothing can
	// disappear through a LEFT JOIN or a NULL/non-TEXT identity alias.
	rows2, e := r.tx.QueryContext(r.ctx, `SELECT substr(CAST(lease AS BLOB),1,33),typeof(lease),typeof(data),length(CAST(data AS BLOB)) FROM lease_budget_bindings ORDER BY rowid LIMIT 4097`)
	if e != nil {
		return nil, e
	}
	defer func() { err = errors.Join(err, rows2.Close()) }()
	seen := map[string]bool{}
	for rows2.Next() {
		if len(seen) == providerPairC2Rows {
			return nil, fmt.Errorf("ordinary sidecar sentinel")
		}
		var id []byte
		var it, dt string
		var n sql.NullInt64
		if e = rows2.Scan(&id, &it, &dt, &n); e != nil {
			return nil, e
		}
		if it != "text" || !providerPairHexID(string(id)) || !ids[string(id)] || seen[string(id)] || dt != "text" || !n.Valid || n.Int64 < 1 || n.Int64 > providerPairC2BudgetBytes {
			return nil, fmt.Errorf("ordinary sidecar tuple/type/bytes")
		}
		if n.Int64 > providerPairC2TotalBytes-total {
			return nil, fmt.Errorf("ordinary aggregate sidecar bytes")
		}
		total += n.Int64
		seen[string(id)] = true
	}
	if e = rows2.Err(); e != nil {
		return nil, e
	}
	for i := range headers {
		headers[i].hasBudget = seen[headers[i].id]
	}
	rows3, e := r.tx.QueryContext(r.ctx, `SELECT substr(CAST(lease AS BLOB),1,33),typeof(lease),typeof(data),length(CAST(data AS BLOB)) FROM pool_claim_bindings ORDER BY rowid LIMIT 4097`)
	if e != nil {
		return nil, e
	}
	defer func() { err = errors.Join(err, rows3.Close()) }()
	poolSeen := map[string]bool{}
	for rows3.Next() {
		if len(poolSeen) == providerPairC2Rows {
			return nil, fmt.Errorf("ordinary pool sidecar sentinel")
		}
		var id []byte
		var it, dt string
		var n sql.NullInt64
		if e = rows3.Scan(&id, &it, &dt, &n); e != nil {
			return nil, e
		}
		if it != "text" || !providerPairHexID(string(id)) || !ids[string(id)] || poolSeen[string(id)] || dt != "text" || !n.Valid || n.Int64 < 1 || n.Int64 > providerPairC2LeaseBytes {
			return nil, fmt.Errorf("ordinary pool sidecar tuple/type/bytes")
		}
		if n.Int64 > providerPairC2TotalBytes-total {
			return nil, fmt.Errorf("ordinary aggregate pool bytes")
		}
		total += n.Int64
		poolSeen[string(id)] = true
	}
	if e = rows3.Err(); e != nil {
		return nil, e
	}
	for i := range headers {
		headers[i].hasPoolBinding = poolSeen[headers[i].id]
	}
	return headers, nil
}
func (r *providerPairC2Reader) ordinary(h providerPairC2Header, raw []byte) (model.Lease, error) {
	var l model.Lease
	// This raw-presence check remains independent even if a future central patch
	// adds a typed model LeaseRef. Null/false is still an orphan, never absence.
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return l, fmt.Errorf("ordinary lease object")
	}
	for name := range fields {
		if strings.EqualFold(name, "provider_preparation") {
			return l, fmt.Errorf("ordinary row has uncovered raw pair reference")
		}
	}
	if e := decodeProviderPairC2JSON(raw, &l, providerPairC2LeaseBytes); e != nil {
		return l, e
	}
	if l.ID != h.id || l.Host != r.host || l.Owner != r.owner || l.Revision < 1 || l.Created.IsZero() || l.Request.Key != h.key || h.keyNull != (l.Request.Key == "") || !budgetText(l.Request.Reason, 4096, true) || l.Request.TTL <= 0 || l.Request.TTL > 7*24*time.Hour {
		return l, fmt.Errorf("ordinary identity/request binding")
	}
	if l.Request.Host != "" && l.Request.Host != "localhost" && l.Request.Host != r.host {
		return l, fmt.Errorf("ordinary request authority")
	}
	switch l.Request.Kind {
	case "", "execution", "storage", "ports", "relay-transit", "sync-preparation", "forward-listener":
	default:
		return l, fmt.Errorf("unknown ordinary provider kind")
	}
	switch l.State {
	case "queued", "active", "provisioning", "ending", "reclaiming", "needs_attention":
	case "terminal":
		if l.Ended == nil || l.Ended.IsZero() || l.Ended.Before(l.Created) {
			return l, fmt.Errorf("ordinary terminal lacks settlement observation")
		}
		switch l.Outcome {
		case "released", "cancelled", "expired", "revoked", "completed", "failed", "launch_failed", "admission_failed", "interrupted", "reconciled", "handed_off", "rejected", "relay_closed", "forward_closed", "stopped", "listener_closed", "mesh_closed", "forward_listener_closed", "listener_admission_failed", "start_failed":
		default:
			return l, fmt.Errorf("unknown ordinary terminal outcome")
		}
	default:
		return l, fmt.Errorf("unknown ordinary lifecycle")
	}
	if h.runKind != "null" {
		var n sql.NullInt64
		var kind string
		var body []byte
		if e := r.tx.QueryRowContext(r.ctx, `SELECT typeof(run),length(CAST(run AS BLOB)),CASE WHEN typeof(run)='text' AND length(CAST(run AS BLOB))<=? THEN CAST(run AS BLOB) END FROM leases WHERE id=?`, min(int64(providerPairC2LeaseBytes), r.remaining), h.id).Scan(&kind, &n, &body); e != nil {
			return l, e
		}
		if kind != "text" || !n.Valid || n.Int64 != h.runBytes || n.Int64 > r.remaining || int64(len(body)) != n.Int64 {
			return l, fmt.Errorf("ordinary run tuple changed")
		}
		r.remaining -= n.Int64
		var run RunRequest
		if e := decodeProviderPairC2JSON(body, &run, providerPairC2LeaseBytes); e != nil {
			return l, e
		}
		if e := providerPairC2SameRequestCPU(raw, body); e != nil {
			return l, e
		}
		if !reflect.DeepEqual(run.Request, l.Request) || len(run.Command) == 0 || !strings.HasPrefix(run.CWD, "/") {
			return l, fmt.Errorf("ordinary run request binding")
		}
	} else if l.PID != 0 || l.Process != nil {
		return l, fmt.Errorf("ordinary native identity without run")
	}
	return l, nil
}

// This unused census has no reviewed accounting bridge for named storage roots.
// Refuse their namespace in the SAME snapshot, but keep scanning retained floors.
func (r *providerPairC2Reader) storageFormatBoundary() (err error) {
	rows, err := r.tx.QueryContext(r.ctx, `SELECT typeof(key),length(CAST(key AS BLOB)),substr(CAST(key AS BLOB),1,65),typeof(value),length(CAST(value AS BLOB)),substr(CAST(value AS BLOB),1,2) FROM metadata WHERE lower(replace(replace(CAST(substr(CAST(key AS BLOB),1,65) AS TEXT),'ſ','s'),'K','k')) IN ('workspace_format','storage_pools') LIMIT 3`)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	seen := false
	for rows.Next() {
		var keyKind, valueKind string
		var key, value []byte
		var keySize, size sql.NullInt64
		if err := rows.Scan(&keyKind, &keySize, &key, &valueKind, &size, &value); err != nil {
			return err
		}
		if seen || keyKind != "text" || !keySize.Valid || keySize.Int64 != int64(len(key)) || string(key) != "workspace_format" || valueKind != "text" || !size.Valid || size.Int64 != 1 || string(value) != "1" {
			return fmt.Errorf("named storage namespace is outside the private census contract")
		}
		seen = true
	}
	return rows.Err()
}

// Absent optional namespace is a real empty observation only if the marker and
// all six declared schema objects are absent. This is not a format permission.
func (r *providerPairC2Reader) namespace() (present bool, err error) {
	expected := map[string][3]string{
		"provider_preparations": {"table", "provider_preparations", providerpreparation.TableDDL}, "provider_preparations_operation_key": {"index", "provider_preparations", providerpreparation.OperationIndexDDL},
		"provider_preparation_members": {"table", "provider_preparation_members", providerpreparation.MembersDDL}, "provider_preparation_roles": {"index", "provider_preparation_members", providerpreparation.RolesIndexDDL},
		"sqlite_autoindex_provider_preparations_1": {"index", "provider_preparations", ""}, "sqlite_autoindex_provider_preparation_members_1": {"index", "provider_preparation_members", ""},
	}
	rows, e := r.tx.QueryContext(r.ctx, `SELECT substr(CAST(type AS BLOB),1,17),substr(CAST(name AS BLOB),1,129),substr(CAST(tbl_name AS BLOB),1,129),substr(CAST(sql AS BLOB),1,8193) FROM sqlite_schema WHERE name GLOB 'provider_prepar*' OR name GLOB 'sqlite_autoindex_provider_prepar*' OR tbl_name GLOB 'provider_prepar*' OR sql LIKE '%provider_preparations%' OR sql LIKE '%provider_preparation_members%' ORDER BY name LIMIT 8`)
	if e != nil {
		return false, e
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	seen := map[string]bool{}
	for rows.Next() {
		var kind, name, table string
		var ddl sql.NullString
		if e = rows.Scan(&kind, &name, &table, &ddl); e != nil {
			return false, e
		}
		want, ok := expected[name]
		if !ok || seen[name] || len(ddl.String) > 8192 || [3]string{kind, table, ddl.String} != want || ddl.Valid == (want[2] == "") {
			return false, fmt.Errorf("optional pair namespace is altered/partial")
		}
		seen[name] = true
	}
	if e = rows.Err(); e != nil {
		return false, e
	}
	var kind string
	var raw []byte
	e = r.tx.QueryRowContext(r.ctx, `SELECT typeof(value),substr(CAST(value AS BLOB),1,2) FROM metadata WHERE key=?`, providerpreparation.SchemaKey).Scan(&kind, &raw)
	if len(seen) == 0 && errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil || len(seen) != len(expected) || kind != "text" || string(raw) != "1" {
		return false, fmt.Errorf("optional pair marker/group incomplete")
	}
	return true, nil
}
func (r *providerPairC2Reader) floors(p *providerPairC2Projection) (map[string]providerPairQuantity, error) {
	floors := map[string]providerPairQuantity{}
	rows, e := r.tx.QueryContext(r.ctx, providerpreparation.ReadHeadersSQL)
	if e != nil {
		return floors, e
	}
	var err error
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if p.pairs == providerpreparation.MaxJournalRows {
			return floors, fmt.Errorf("pair header sentinel")
		}
		h, e := scanProviderPairHeader(rows)
		if e != nil {
			return floors, e
		}
		if h.authority != r.host || h.owner != r.owner {
			return floors, fmt.Errorf("pair authority differs")
		}
		if _, ok := floors[h.worker]; ok {
			return floors, fmt.Errorf("pair member alias")
		}
		if _, ok := floors[h.temp]; ok {
			return floors, fmt.Errorf("pair member alias")
		}
		floors[h.worker] = providerPairQuantity{cpuMillis: 1000, memoryBytes: 64 << 20}
		floors[h.temp] = providerPairQuantity{diskBytes: 64 << 20}
		p.pairs++
		p.logicalBytes += h.reserve
		if p.logicalBytes > providerpreparation.MaxAggregateBytes {
			return floors, fmt.Errorf("pair logical reservation bound")
		}
	}
	err = rows.Err()
	return floors, errors.Join(err, rows.Close())
}
func readProviderPairInventory(ctx context.Context, tx *sql.Tx, host, owner string) (p providerPairC2Projection, err error) {
	p.state = "unknown"
	p.budgets = map[string]providerPairC2Amounts{}
	p.pools = map[string]int64{}
	if ctx == nil || tx == nil || !providerPairHexID(host) || !providerPairUID(owner) {
		p.fail(fmt.Errorf("census snapshot/authority required"))
		return p, p.problem
	}
	r := providerPairC2Reader{ctx: ctx, tx: tx, host: host, owner: owner, remaining: providerPairC2TotalBytes, budgetCache: map[string]model.Budget{}, poolCache: map[string]model.Pool{}}
	floors := map[string]providerPairQuantity{}
	pairs := []providerPairDecoded{}
	p.fail(r.storageFormatBoundary())
	present, namespaceErr := r.namespace()
	p.fail(namespaceErr)
	// A damaged marker/optional group cannot erase readable fixed header duties.
	// This bounded projection is retained data only, never positive coverage.
	headersReadable := present
	if namespaceErr != nil {
		var headerErr error
		headersReadable, headerErr = r.knownHeaderTable()
		p.fail(headerErr)
	}
	if headersReadable {
		var floorErr error
		floors, floorErr = r.floors(&p)
		p.fail(floorErr)
	}
	if present && namespaceErr == nil {
		var pairErr error
		pairs, pairErr = readProviderPairJournalSnapshot(ctx, tx, host, owner)
		p.fail(pairErr)
	}
	headers, e := r.headers()
	if e != nil {
		p.fail(e)
		for _, q := range floors {
			p.fail(providerPairC2Add(&p.physical, q))
		}
		return p, p.problem
	}
	covered := map[string]providerPairDecoded{}
	for _, pair := range pairs {
		if pair.progress.Phase == providerpreparation.Unknown {
			p.fail(fmt.Errorf("pair retains unknown preparation state"))
		}
		for _, row := range pair.rows {
			covered[row.id] = pair
		}
	}
	for _, h := range headers {
		if e := ctx.Err(); e != nil {
			p.fail(e)
			break
		}
		p.ordinaryRows++
		raw, e := r.raw("leases", "id", h.id, providerPairC2LeaseBytes)
		if e != nil {
			p.fail(e)
			continue
		}
		if int64(len(raw)) != h.dataBytes {
			p.fail(fmt.Errorf("census physical body length differs"))
			continue
		}
		if q, ok := floors[h.id]; ok {
			if h.hasPoolBinding {
				p.fail(fmt.Errorf("inert pair has foreign pool sidecar"))
			}
			if larger, e := providerPairC2QuantityFromRaw(raw); e == nil {
				q.cpuMillis = max(q.cpuMillis, larger.cpuMillis)
				q.memoryBytes = max(q.memoryBytes, larger.memoryBytes)
				q.diskBytes = max(q.diskBytes, larger.diskBytes)
				floors[h.id] = q
			}
			if pair, ok := covered[h.id]; ok {
				matched := false
				for _, row := range pair.rows {
					if row.id == h.id {
						matched = bytes.Equal(raw, row.lease) && h.key == row.requestKey && h.runKind == "null"
					}
				}
				if !matched {
					p.fail(fmt.Errorf("pair ordinary coverage changed"))
				}
			}
			continue
		}
		l, e := r.ordinary(h, raw)
		if e != nil {
			p.fail(e)
			if q, qe := providerPairC2QuantityFromRaw(raw); qe == nil {
				p.fail(providerPairC2Add(&p.physical, q))
			}
			continue
		}
		q, e := providerPairC2QuantityFromRaw(raw)
		if e != nil {
			p.fail(e)
			continue
		}
		retained := l.State != "queued" && l.State != "terminal"
		hasStorageCustody := l.ManagedDir != ""
		for _, capability := range l.Capabilities {
			if capability == "managed-storage" || capability == "explicit-storage-consumers" {
				hasStorageCustody = true
			}
		}
		if l.State == "terminal" && l.Request.Kind == "storage" && l.Artifact == nil && (hasStorageCustody || l.Outcome != "cancelled" && l.Outcome != "rejected") {
			p.fail(fmt.Errorf("terminal storage lost artifact history"))
			retained = true
		}
		if l.State == "terminal" && l.Artifact != nil && l.Artifact.Phase == "handed_off" && l.Outcome != "handed_off" {
			p.fail(fmt.Errorf("artifact handoff outcome differs"))
			retained = true
		}
		if l.State == "terminal" && l.Artifact != nil && l.Artifact.Phase != "handed_off" {
			a := l.Artifact
			if a.Phase != "removed" || a.Name != l.ID || a.Identity == nil || a.Identity.Inode == 0 || a.Identity.Owner != uint32OwnerForC2(l.Owner) || !a.NamespaceRemoved || a.AllocatedBefore == nil || *a.AllocatedBefore != 0 || a.ContentObserved {
				p.fail(fmt.Errorf("terminal artifact debt unresolved"))
				retained = true
			}
		}
		for _, port := range l.Ports {
			if l.State == "terminal" && port.State != "observed_available" {
				p.fail(fmt.Errorf("terminal port custody unresolved"))
			}
		}
		var path []string
		if l.State == "terminal" && h.hasPoolBinding {
			p.fail(r.terminalPoolBinding(l))
		}
		poolAmounts := map[string]int64{}
		if retained || l.State == "queued" || h.hasBudget || l.Budget != nil {
			path, e = r.budgetPath(l)
			p.fail(e)
		}
		if retained || l.State == "queued" {
			poolAmounts, e = r.poolAmounts(l)
			p.fail(e)
		}
		if retained {
			p.charge(path, q, poolAmounts)
			for _, claim := range l.Request.Pools {
				p.fail(providerPairC2AddPool(p.pools, claim.Pool, claim.Quantity))
			}
		}
	}
	// Each protected member replaces its ordinary SQL row ONCE, including missing
	// or terminal members. Ancestor rollups never get added to physical promises.
	for id, q := range floors {
		var path []string
		if pair, ok := covered[id]; ok {
			path = pair.budgets[0].Path
			if e = r.checkPairPolicyRevisions(pair); e != nil {
				p.fail(e)
				path = nil
			}
		}
		p.charge(path, q, nil)
	}
	if e = ctx.Err(); e != nil {
		p.fail(e)
	}
	if p.problem == nil {
		p.state = "covered"
	}
	return p, p.problem
}
func (m *Manager) observeProviderPairInventory(ctx context.Context) (p providerPairC2Projection, err error) {
	p.state = "unknown"
	if ctx == nil {
		p.fail(fmt.Errorf("caller context required"))
		return p, p.problem
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if e := m.lockProviderPairMembers(ctx); e != nil {
		p.fail(e)
		return p, p.problem
	}
	defer m.mu.Unlock()
	if m.db == nil || m.closing || m.draining {
		p.fail(fmt.Errorf("census unavailable"))
		return p, p.problem
	}
	tx, e := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		p.fail(e)
		return p, p.problem
	}
	defer func() {
		if e := tx.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
			p.fail(e)
		}
		if e := ctx.Err(); e != nil {
			p.fail(e)
		}
		err = p.problem
	}()
	return readProviderPairInventory(ctx, tx, m.host, m.owner)
}

// Marker/group damage never makes an unknown table/view a trusted header
// source. Only the exact known table definition permits conservative floors.
func (r *providerPairC2Reader) knownHeaderTable() (bool, error) {
	var kind, name, table string
	var ddl sql.NullString
	e := r.tx.QueryRowContext(r.ctx, `SELECT substr(CAST(type AS BLOB),1,17),substr(CAST(name AS BLOB),1,129),substr(CAST(tbl_name AS BLOB),1,129),substr(CAST(sql AS BLOB),1,8193) FROM sqlite_schema WHERE name='provider_preparations'`).Scan(&kind, &name, &table, &ddl)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if kind != "table" || name != "provider_preparations" || table != name || !ddl.Valid || ddl.String != providerpreparation.TableDDL {
		return false, fmt.Errorf("pair header table schema is unsupported")
	}
	return true, nil
}
