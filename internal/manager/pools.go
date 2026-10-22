package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

func poolText(s string, max int, required bool) bool {
	return utf8.ValidString(s) && len(s) <= max && (!required || strings.TrimSpace(s) != "") && !strings.ContainsFunc(s, unicode.IsControl)
}
func (m *Manager) pool(id string) (model.Pool, error) {
	var p model.Pool
	var raw []byte
	err := m.db.QueryRow(`SELECT data FROM pools WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, model.Err("pool_not_found", "pool is not registered")
	}
	if err == nil {
		err = json.Unmarshal(raw, &p)
	}
	return p, err
}

// MutatePool serializes admission and capacity changes. State, journal, durable
// retry result and the event commit together; a disconnected caller can retry.
func (m *Manager) MutatePool(r model.PoolMutation) (model.PoolChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result model.PoolChange
	if m.closing || m.draining {
		return result, model.Err("backend_stopping", "backend is shutting down")
	}
	if strings.TrimSpace(r.Reason) == "" || len(r.Reason) > 4096 || r.Key == "" || len(r.Key) > 128 || len(r.Project) > 256 || len(r.Agent) > 128 {
		return result, model.Err("invalid_request", "provide a reason (1..4096 bytes), operation key (1..128 bytes) and bounded attribution")
	}
	if err := m.normalizeSubpoolCreate(&r); err != nil {
		return result, err
	}
	if r.Operation == "create" {
		if r.ID != "" || r.Delta != nil || r.Revision != nil || r.Capacity == nil || *r.Capacity < 0 || !poolText(r.Name, 128, true) || !poolText(r.Description, 4096, false) {
			return result, model.Err("invalid_request", "create requires a name and nonnegative integral capacity; no ID, delta or revision")
		}
		r.Name = strings.TrimSpace(r.Name)
		if r.Unit == "" {
			r.Unit = "count"
		}
		if !poolText(r.Unit, 64, true) {
			return result, model.Err("invalid_request", "unit must contain 1..64 printable bytes")
		}
		switch r.Scope {
		case "host":
			if r.Host != "" && r.Host != "localhost" && r.Host != m.host {
				return result, model.Err("host_unavailable", "only the local host authority is registered")
			}
			r.Host = m.host
		case "global":
			if r.Host != "" {
				return result, model.Err("invalid_request", "global pools do not have a resource host")
			}
		default:
			return result, model.Err("invalid_request", "select explicit scope: host or global")
		}
	} else {
		if r.ID == "" || r.Scope != "" || r.Host != "" || r.Unit != "" || r.Parent != "" || r.ParentRevision != nil {
			return result, model.Err("invalid_request", "mutations require an ID and cannot change resource scope, host or unit")
		}
		if r.Operation != "adjust" && (r.Revision == nil || *r.Revision < 1) {
			return result, model.Err("invalid_request", "provide the expected pool revision")
		}
		switch r.Operation {
		case "set":
			if r.Capacity == nil || *r.Capacity < 0 || r.Delta != nil {
				return result, model.Err("invalid_request", "set requires a nonnegative integral capacity")
			}
		case "adjust":
			if r.Delta == nil || *r.Delta == 0 || r.Capacity != nil {
				return result, model.Err("invalid_request", "adjust requires a nonzero signed integral delta")
			}
		case "drain", "resume", "delete", "rename", "describe":
			if r.Capacity != nil || r.Delta != nil {
				return result, model.Err("invalid_request", "capacity and delta do not apply to this operation")
			}
		default:
			return result, model.Err("invalid_request", "unknown pool operation")
		}
		if r.Operation == "rename" {
			if !poolText(r.Name, 128, true) {
				return result, model.Err("invalid_request", "rename requires a printable name")
			}
			r.Name = strings.TrimSpace(r.Name)
		} else if r.Name != "" {
			return result, model.Err("invalid_request", "name applies only to create or rename")
		}
		if r.Operation == "describe" {
			if !poolText(r.Description, 4096, false) {
				return result, model.Err("invalid_request", "description must be at most 4096 printable bytes")
			}
		} else if r.Description != "" {
			return result, model.Err("invalid_request", "description applies only to create or describe")
		}
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return result, err
	}
	var saved, reply []byte
	err = m.db.QueryRow(`SELECT request,result FROM pool_operations WHERE key=?`, r.Key).Scan(&saved, &reply)
	if err == nil {
		if string(saved) != string(raw) {
			return result, model.Err("idempotency_conflict", "pool operation key was used with different parameters")
		}
		err = json.Unmarshal(reply, &result)
		return result, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if policyErr := m.poolPolicyError(); policyErr != nil {
		return result, policyErr
	}
	if err = m.reviewPoolMutationLocked(r); err != nil {
		return result, err
	}
	session, registration := "", ""
	if r.Agent != "" {
		c, contactErr := m.contact(r.Agent)
		err = contactErr
		if errors.Is(err, sql.ErrNoRows) {
			return result, model.Err("invalid_contact", "register the agent before attributing pool changes")
		}
		if err != nil {
			return result, err
		}
		session = c.Session
		registration = c.Registration
	}
	now := time.Now().UTC()
	var p model.Pool
	if r.Operation == "create" {
		p = model.Pool{ID: "pool_" + local.ID(), Parent: r.Parent, Name: r.Name, Scope: r.Scope, Host: r.Host, Authority: m.host, Owner: m.owner, OwnerUsername: m.ownerUsername, Agent: r.Agent, Session: session, Registration: registration, Project: r.Project, Description: r.Description, Unit: r.Unit, Capacity: *r.Capacity, Revision: 1, State: "active", Accounting: "cooperative-counted-permits", Expiry: "return-after-lease-settlement", Created: now, Updated: now}
	} else {
		p, err = m.pool(r.ID)
		if err != nil {
			return result, err
		}
		if p.Authority != m.host {
			return result, model.Err("authority_unavailable", "pool belongs to another authority")
		}
		if p.State == "deleted" {
			return result, model.Err("pool_deleted", "pool is a historical tombstone")
		}
		if r.Revision != nil && *r.Revision != p.Revision {
			return result, model.Err("revision_conflict", "pool changed; inspect its current revision before retrying")
		}
		if p.Revision == math.MaxInt64 {
			return result, model.Err("revision_exhausted", "pool revision cannot be incremented")
		}
		before := p
		result.Previous = &before
		switch r.Operation {
		case "set":
			p.Capacity = *r.Capacity
		case "adjust":
			if *r.Delta < -p.Capacity || *r.Delta > math.MaxInt64-p.Capacity {
				return result, model.Err("invalid_request", "adjustment would make capacity negative or overflow int64")
			}
			p.Capacity += *r.Delta
		case "drain":
			p.State = "draining"
		case "resume":
			p.State = "active"
		case "rename":
			p.Name = r.Name
		case "describe":
			p.Description = r.Description
		case "delete":
			ls, err := m.list()
			if err != nil {
				return result, err
			}
			for _, l := range ls {
				if poolCharged(l.State) || l.State == "queued" {
					for _, c := range l.Request.Pools {
						if c.Pool == p.ID {
							return result, model.Err("pool_in_use", "finish all queued and consuming claims before deleting a pool")
						}
					}
				}
			}
			p.State = "deleted"
		}
		p.Revision++
		p.Updated = now
	}
	partitions, lineage, err := m.validateSubpoolMutation(r, p)
	if err != nil {
		return result, err
	}
	result.Partitions = partitions
	var collision int
	err = m.db.QueryRow(`SELECT 1 FROM pools WHERE scope=? AND host=? AND name=? AND id<>? AND json_extract(data,'$.state')<>'deleted' LIMIT 1`, p.Scope, p.Host, p.Name, p.ID).Scan(&collision)
	if err == nil {
		return result, model.Err("pool_exists", "a live pool already has this name in the selected scope")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	result.Operation = r.Operation
	result.Key = r.Key
	result.Actor = m.owner
	result.ActorUsername = m.ownerUsername
	result.Agent = r.Agent
	result.Session = session
	result.Registration = registration
	result.Project = r.Project
	result.Reason = r.Reason
	result.At = now
	result.Pool = p
	result.Delta = p.Capacity
	if result.Previous != nil {
		result.Delta -= result.Previous.Capacity
	}
	tx, err := m.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if r.Operation == "create" {
		if err = insertPoolLineage(tx, lineage); err != nil {
			return result, err
		}
	}
	data, _ := json.Marshal(p)
	if _, err = tx.Exec(`INSERT INTO pools(id,scope,host,name,data) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,data=excluded.data`, p.ID, p.Scope, p.Host, p.Name, string(data)); err != nil {
		return result, err
	}
	data, _ = json.Marshal(result)
	inserted, err := tx.Exec(`INSERT INTO pool_changes(pool,data) VALUES(?,?)`, p.ID, string(data))
	if err != nil {
		return result, err
	}
	if result.Sequence, err = inserted.LastInsertId(); err != nil {
		return result, err
	}
	data, _ = json.Marshal(result)
	if _, err = tx.Exec(`INSERT INTO pool_operations(key,request,result) VALUES(?,?,?)`, r.Key, string(raw), string(data)); err != nil {
		return result, err
	}
	if err = event(tx, "pool."+r.Operation, p.ID, result); err != nil {
		return result, err
	}
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func (m *Manager) normalizePoolClaims(claims []model.PoolClaim) ([]model.PoolClaim, error) {
	if len(claims) == 0 {
		return nil, nil
	}
	if len(claims) > 64 {
		return nil, model.Err("invalid_request", "request at most 64 pool claims")
	}
	counts := map[string]int64{}
	for _, c := range claims {
		if c.Quantity <= 0 || c.Quantity > math.MaxInt64-counts[c.Pool] {
			return nil, model.Err("invalid_request", "pool quantities must be positive int64 counts without overflow")
		}
		counts[c.Pool] += c.Quantity
	}
	out := make([]model.PoolClaim, 0, len(counts))
	for id, q := range counts {
		p, err := m.pool(id)
		if err != nil {
			return nil, err
		}
		if p.Authority != m.host || (p.Scope == "host" && p.Host != m.host) {
			return nil, model.Err("authority_unavailable", "pool is not managed by this authority")
		}
		for _, c := range claims {
			if c.Pool == id && ((c.Scope != "" && c.Scope != p.Scope) || (c.Host != "" && c.Host != p.Host) || (c.Authority != "" && c.Authority != p.Authority)) {
				return nil, model.Err("invalid_request", "pool claim scope/authority conflicts with its definition")
			}
		}
		out = append(out, model.PoolClaim{Pool: id, Quantity: q, Scope: p.Scope, Host: p.Host, Authority: p.Authority})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pool < out[j].Pool })
	return out, nil
}
func poolReservations(ls []model.Lease) (map[string]int64, error) {
	used := map[string]int64{}
	for _, l := range ls {
		if !poolCharged(l.State) && l.State != "queued" && l.State != "terminal" {
			return nil, model.Err("invalid_accounting", "unknown lifecycle cannot free pool backing")
		}
		if poolCharged(l.State) {
			for _, c := range l.Request.Pools {
				if c.Quantity <= 0 || c.Quantity > math.MaxInt64-used[c.Pool] {
					return nil, model.Err("invalid_accounting", "persisted pool claims are invalid or overflow")
				}
				used[c.Pool] += c.Quantity
			}
		}
	}
	return used, nil
}
func (m *Manager) poolsFit(claims []model.PoolClaim, used map[string]int64) (bool, error) {
	if err := m.poolPolicyError(); err != nil {
		return false, err
	}
	if len(claims) == 0 {
		return true, nil
	}
	t, err := m.checkedPoolTree()
	if err != nil {
		return false, err
	}
	return t.admit(claims, used)
}
func (m *Manager) poolViews(ls []model.Lease, all bool) ([]model.PoolView, error) {
	t, err := m.checkedPoolTree()
	if err != nil {
		return nil, err
	}
	definitions := []model.Pool{}
	for _, p := range t.nodes {
		definitions = append(definitions, p)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	used, err := poolReservations(ls)
	if err != nil {
		return nil, err
	}
	accounting, err := t.accounting(used)
	if err != nil {
		return nil, err
	}
	tokens, unavailable, err := m.hierarchyReviewTokens(t, ls, accounting, "")
	if err != nil {
		return nil, err
	}
	out := []model.PoolView{}
	for _, p := range definitions {
		if p.State == "deleted" && !all {
			continue
		}
		v := model.PoolView{Review: &model.PoolReview{Authority: m.host, Consumers: []model.LeaseRevision{}, Hierarchy: tokens[p.ID], Unavailable: unavailable[p.ID]}, Pool: p, Reserved: used[p.ID], Available: max(0, p.Capacity-used[p.ID]), Deficit: max(0, used[p.ID]-p.Capacity), Users: []model.ResourceUse{}, Queued: []model.ResourceUse{}}
		partition := accounting[p.ID]
		v.Partition = &partition
		v.Available = partition.Available
		v.Deficit = partition.Deficit
		v.Ancestors = append([]string{}, t.paths[p.ID]...)
		v.Children = append([]string{}, t.children[p.ID]...)
		if p.State == "deleted" {
			v.Available = 0
		}
		for _, l := range ls {
			if poolCharged(l.State) || l.State == "queued" {
				for _, c := range l.Request.Pools {
					if c.Pool == p.ID {
						v.Review.Consumers = append(v.Review.Consumers, model.LeaseRevision{ID: l.ID, Revision: l.Revision})
						u := model.ResourceUse{Lease: l.ID, Owner: l.Owner, OwnerUsername: l.OwnerUsername, Agent: l.Request.Agent, Project: l.Request.Project, Reason: l.Request.Reason, Quantity: model.CountQuantity(c.Quantity), State: l.State, Expires: l.Expires}
						if l.State == "queued" {
							v.Queued = append(v.Queued, u)
						} else {
							v.Users = append(v.Users, u)
						}
					}
				}
			}
		}
		sort.Slice(v.Review.Consumers, func(i, j int) bool { return v.Review.Consumers[i].ID < v.Review.Consumers[j].ID })
		out = append(out, v)
	}
	return out, nil
}
func (m *Manager) Pools(all bool) ([]model.PoolView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, err := m.list()
	if err != nil {
		return nil, err
	}
	return m.poolViews(ls, all)
}
func (m *Manager) PoolInspect(id string) (model.PoolView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.pool(id); err != nil {
		return model.PoolView{}, err
	}
	ls, err := m.list()
	if err != nil {
		return model.PoolView{}, err
	}
	views, err := m.poolViews(ls, true)
	if err != nil {
		return model.PoolView{}, err
	}
	for _, v := range views {
		if v.Pool.ID == id {
			return v, nil
		}
	}
	return model.PoolView{}, model.Err("pool_not_found", "pool is not registered")
}
func (m *Manager) PoolHistory(id string, after int64) (model.PoolHistory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := model.PoolHistory{Pool: id, Cursor: after, Entries: []model.PoolChange{}}
	if after < 0 {
		return result, model.Err("invalid_request", "history cursor must be nonnegative")
	}
	if _, err := m.pool(id); err != nil {
		return result, err
	}
	rows, err := m.db.Query(`SELECT sequence,data FROM pool_changes WHERE pool=? AND sequence>? ORDER BY sequence LIMIT 101`, id, after)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	pageBytes := 0
	for rows.Next() {
		var entry model.PoolChange
		var seq int64
		var raw []byte
		if err = rows.Scan(&seq, &raw); err != nil {
			return result, err
		}
		if err = json.Unmarshal(raw, &entry); err != nil {
			return result, err
		}
		if len(result.Entries) == 100 || (len(result.Entries) > 0 && pageBytes+len(raw) > 1<<20) {
			result.More = true
			break
		}
		pageBytes += len(raw)
		entry.Sequence = seq
		result.Cursor = seq
		result.Entries = append(result.Entries, entry)
	}
	return result, rows.Err()
}
