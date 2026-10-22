package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/yrashk/arm/internal/model"
)

// All callers hold m.mu or are in Open before admission is available.
func (m *Manager) poolDefinitions() ([]model.Pool, error) {
	rows, err := m.db.Query(`SELECT id,scope,host,name,length(CAST(data AS BLOB)),CASE WHEN length(CAST(data AS BLOB))<=65536 THEN data END FROM pools ORDER BY id LIMIT 4097`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Pool{}
	for rows.Next() {
		var id, scope, host, name string
		var n int64
		var raw []byte
		var p model.Pool
		if err = rows.Scan(&id, &scope, &host, &name, &n, &raw); err != nil {
			return nil, err
		}
		if n > 65536 || decodePoolRecord(raw, &p) != nil || p.ID != id || p.Scope != scope || p.Host != host || p.Name != name || p.Authority != m.host || p.Owner != m.owner || !poolText(p.Name, 128, true) || !poolText(p.Unit, 64, true) || p.Created.IsZero() || p.Updated.Before(p.Created) {
			return nil, model.Err("invalid_accounting", "pool identity/authority evidence is invalid")
		}
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
func poolLineage(t *poolTree, id string) model.PoolLineage {
	p := t.nodes[id]
	return model.PoolLineage{Pool: id, Parent: p.Parent, Authority: p.Authority, Owner: p.Owner, Scope: p.Scope, Host: p.Host, Unit: p.Unit, Path: append([]string{}, t.paths[id]...)}
}
func (m *Manager) checkedPoolTree() (*poolTree, error) {
	if err := m.poolPolicyError(); err != nil {
		return nil, err
	}
	defs, err := m.poolDefinitions()
	if err != nil {
		return nil, err
	}
	t, err := buildPoolTree(defs)
	if err != nil {
		return nil, err
	}
	rows, err := m.db.Query(`SELECT pool,length(CAST(data AS BLOB)),CASE WHEN length(CAST(data AS BLOB))<=16384 THEN data END FROM pool_lineage LIMIT 4097`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		var n int64
		var raw []byte
		var v model.PoolLineage
		if err = rows.Scan(&id, &n, &raw); err != nil {
			return nil, err
		}
		if n > 16384 || decodePoolRecord(raw, &v) != nil || t.nodes[id].ID == "" || !reflect.DeepEqual(v, poolLineage(t, id)) {
			return nil, model.Err("invalid_accounting", "immutable pool lineage is missing, malformed or changed")
		}
		seen[id] = true
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(seen) != len(t.nodes) {
		return nil, model.Err("invalid_accounting", "pool definitions lost immutable lineage")
	}
	return t, nil
}
func (m *Manager) initSubpoolRecords() error {
	for _, q := range []string{`CREATE TABLE IF NOT EXISTS pool_lineage (pool TEXT PRIMARY KEY,data TEXT NOT NULL)`, `CREATE TABLE IF NOT EXISTS pool_claim_bindings (lease TEXT PRIMARY KEY,data TEXT NOT NULL)`} {
		if _, err := m.db.Exec(q); err != nil {
			return err
		}
	}
	var version string
	err := m.db.QueryRow(`SELECT value FROM metadata WHERE key='subpool_schema'`).Scan(&version)
	if err == nil {
		if version != "1" {
			return model.Err("invalid_accounting", "unsupported subpool accounting schema")
		}
		t, e := m.checkedPoolTree()
		if e != nil {
			return e
		}
		if e = m.validatePoolBindings(t); e != nil {
			return e
		}
		ls, e := m.list()
		if e != nil {
			return e
		}
		used, e := poolReservations(ls)
		if e != nil {
			return e
		}
		_, e = t.accounting(used)
		return e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var n int
	if err = m.db.QueryRow(`SELECT (SELECT COUNT(*) FROM pool_lineage)+(SELECT COUNT(*) FROM pool_claim_bindings)`).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return model.Err("invalid_accounting", "subpool schema marker is missing from existing evidence")
	}
	defs, err := m.poolDefinitions()
	if err != nil {
		return err
	}
	for _, p := range defs {
		if p.Parent != "" {
			return model.Err("invalid_accounting", "uninitialized ledger already contains subpool parentage")
		}
	}
	t, err := buildPoolTree(defs)
	if err != nil {
		return err
	}
	leases, err := m.list()
	if err != nil {
		return err
	}
	bindings := map[string]model.PoolClaimBinding{}
	for _, l := range leases {
		if len(l.Request.Pools) == 0 {
			continue
		}
		if l.Host != m.host || l.Owner != m.owner {
			return model.Err("invalid_accounting", "legacy allocation belongs to another authority/owner")
		}
		v, e := m.newPoolClaimBinding(t, l.Request.Pools)
		if e != nil {
			return e
		}
		if poolCharged(l.State) || l.State == "queued" {
			for _, c := range v.Claims {
				for _, id := range c.Path {
					if t.nodes[id].State == "deleted" {
						return model.Err("invalid_accounting", "legacy outstanding allocation traverses a deleted pool")
					}
				}
			}
		}
		bindings[l.ID] = v
	}
	used, err := poolReservations(leases)
	if err != nil {
		return err
	}
	if _, err = t.accounting(used); err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id := range t.nodes {
		if err = insertPoolLineage(tx, poolLineage(t, id)); err != nil {
			return err
		}
	}
	for id, v := range bindings {
		if err = insertPoolClaimBinding(tx, id, v, "pool.binding_migrated"); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO metadata(key,value) VALUES('subpool_schema','1')`); err != nil {
		return err
	}
	if err = event(tx, "pool.lineage_initialized", m.host, map[string]int{"definitions": len(defs), "bindings": len(bindings)}); err != nil {
		return err
	}
	return tx.Commit()
}
func insertPoolLineage(tx *sql.Tx, v model.PoolLineage) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO pool_lineage(pool,data) VALUES(?,?)`, v.Pool, string(raw))
	return err
}
func (m *Manager) newPoolClaimBinding(t *poolTree, claims []model.PoolClaim) (model.PoolClaimBinding, error) {
	v := model.PoolClaimBinding{Authority: m.host, Owner: m.owner, Bound: time.Now().UTC(), Claims: []model.BoundPoolClaim{}}
	if len(claims) > 64 {
		return v, model.Err("invalid_accounting", "pool claim binding exceeds64 direct dimensions")
	}
	seen := map[string]bool{}
	for _, c := range claims {
		if seen[c.Pool] {
			return v, model.Err("invalid_accounting", "duplicate direct pool binding")
		}
		seen[c.Pool] = true
		p, ok := t.nodes[c.Pool]
		if !ok || c.Quantity <= 0 || c.Authority != p.Authority || c.Scope != p.Scope || c.Host != p.Host {
			return v, model.Err("invalid_accounting", "pool claim differs from its immutable definition")
		}
		v.Claims = append(v.Claims, model.BoundPoolClaim{Pool: c.Pool, Quantity: c.Quantity, Path: append([]string{}, t.paths[c.Pool]...)})
	}
	return v, nil
}
func insertPoolClaimBinding(tx *sql.Tx, id string, v model.PoolClaimBinding, kind string) error {
	if len(v.Claims) == 0 {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(raw) > 65536 {
		return model.Err("invalid_request", "complete pool claim lineage exceeds64KiB")
	}
	if _, err = tx.Exec(`INSERT INTO pool_claim_bindings(lease,data) VALUES(?,?)`, id, string(raw)); err != nil {
		return err
	}
	return event(tx, kind, id, v)
}
func (m *Manager) poolClaimBinding(t *poolTree, l model.Lease) (model.PoolClaimBinding, error) {
	var v model.PoolClaimBinding
	if l.Host != m.host || l.Owner != m.owner {
		return v, model.Err("invalid_accounting", "allocation authority differs from immutable pool binding")
	}
	var n int64
	var raw []byte
	err := m.db.QueryRow(`SELECT length(CAST(data AS BLOB)),CASE WHEN length(CAST(data AS BLOB))<=65536 THEN data END FROM pool_claim_bindings WHERE lease=?`, l.ID).Scan(&n, &raw)
	if len(l.Request.Pools) == 0 {
		if errors.Is(err, sql.ErrNoRows) {
			return v, nil
		}
		if err != nil {
			return v, err
		}
		return v, model.Err("invalid_accounting", "unexpected pool binding on allocation without claims")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return v, model.Err("invalid_accounting", "allocation lost immutable pool claim binding")
	}
	if err != nil {
		return v, err
	}
	if n > 65536 || decodePoolRecord(raw, &v) != nil || v.Authority != m.host || v.Owner != m.owner || v.Bound.IsZero() {
		return v, model.Err("invalid_accounting", "pool claim binding is malformed or foreign")
	}
	expected, err := m.newPoolClaimBinding(t, l.Request.Pools)
	if err != nil {
		return v, err
	}
	if !reflect.DeepEqual(v.Claims, expected.Claims) || l.PoolBinding != nil && !reflect.DeepEqual(*l.PoolBinding, v) {
		return v, model.Err("invalid_accounting", "pool claim lineage differs from immutable allocation")
	}
	for _, c := range v.Claims {
		for _, id := range c.Path {
			if t.nodes[id].State == "deleted" && (poolCharged(l.State) || l.State == "queued") {
				return v, model.Err("invalid_accounting", "outstanding claim traverses deleted pool")
			}
		}
	}
	return v, nil
}
func (m *Manager) validatePoolBindings(t *poolTree) error {
	ls, err := m.list()
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, l := range ls {
		known[l.ID] = true
		if _, err = m.poolClaimBinding(t, l); err != nil {
			return err
		}
	}
	rows, err := m.db.Query(`SELECT lease FROM pool_claim_bindings`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		if !known[id] {
			return model.Err("invalid_accounting", "orphan pool claim binding")
		}
	}
	return rows.Err()
}
func (m *Manager) degradePoolStartup(err error) bool {
	var api *model.Error
	if !errors.As(err, &api) || api.Code != "invalid_accounting" {
		return false
	}
	m.poolStartupError = &model.Error{Code: "invalid_accounting", Message: "Pool lineage accounting is degraded; grants, renewals and pool changes are paused. Ordinary provider-safe cleanup remains available. " + api.Message}
	return true
}
func (m *Manager) poolPolicyError() *model.Error {
	if m.poolStartupError == nil {
		return nil
	}
	v := *m.poolStartupError
	return &v
}
