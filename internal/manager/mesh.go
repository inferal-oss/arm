package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"net"
	"time"
)

// AcquirePeer assigns authentication-derived provenance before entering the
// ordinary single-authority scheduler. No caller supplied peer name is trusted.
func (m *Manager) AcquirePeer(peer string, r model.Request, run *RunRequest, limit model.Resources, maxTTL time.Duration) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.Budget != "" || r.ServiceLease != "" || isGradleProfile(r.Profile) || r.StartDeadline != nil || r.Queue || len(r.Pools) > 0 || r.Kind == "storage" || r.Kind == "ports" || len(r.Ports) > 0 || r.StorageLease != "" || r.PortLease != "" || r.Agent != "" || r.UsagePolicy != nil {
		return model.Lease{}, model.Err("unsupported_remote_claim", "initial remote claims support immediate native budgets and supervised execution; policy selectors, services, queue, start deadline, pool, storage, port and agent attachments need separate authorization support")
	}
	if r.TTL <= 0 || r.TTL > maxTTL || !r.Resources.Fits(limit) {
		return model.Lease{}, model.Err("forbidden", "peer resource/TTL ceiling exceeded")
	}
	r.Peer = peer
	r.OriginKey = r.Key
	r.Key = mesh.Key(peer, r.Key)
	var old []byte
	if e := m.db.QueryRow(`SELECT data FROM leases WHERE request_key=?`, r.Key).Scan(&old); e == nil {
		return m.acquire(r, run)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return model.Lease{}, e
	}
	ls, e := m.list()
	if e != nil {
		return model.Lease{}, e
	}
	var used model.Resources
	for _, l := range ls {
		if l.Request.Peer == peer && consuming(l.State) {
			used = used.Add(l.Request.Resources)
		}
	}
	if !r.Resources.FitsRemaining(used, limit) {
		return model.Lease{}, model.Err("forbidden", "aggregate peer resource ceiling exceeded")
	}
	return m.acquire(r, run)
}
func (m *Manager) PeerLease(peer, id string) (model.Lease, error) {
	l, e := m.Inspect(id)
	if e != nil {
		return l, e
	}
	if l.Request.Peer != peer {
		return model.Lease{}, model.Err("forbidden", "lease belongs to another principal")
	}
	return l, nil
}
func (m *Manager) PeerOnce(peer, key, operation string, params json.RawMessage, fn func() (any, error)) (any, error) {
	return m.PeerOnceAuthenticated(peer, key, operation, params, "", 0, fn)
}
func (m *Manager) PeerOnceAuthenticated(peer, key, operation string, params json.RawMessage, certificate string, revision int, fn func() (any, error)) (any, error) {
	if !mesh.ValidKey(key) || peer == "" {
		return nil, model.Err("invalid_request", "bounded principal operation key required")
	}
	var decoded any
	if err := mesh.Decode(params, &decoded); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, err
	}
	params = canonical
	m.mu.Lock()
	_, e := m.db.Exec(`CREATE TABLE IF NOT EXISTS mesh_operations (principal TEXT,key TEXT,operation TEXT,request TEXT,result TEXT,admission TEXT NOT NULL,PRIMARY KEY(principal,key))`)
	if e != nil {
		m.mu.Unlock()
		return nil, e
	}
	var op, request string
	var saved *string
	e = m.db.QueryRow(`SELECT operation,request,result FROM mesh_operations WHERE principal=? AND key=?`, peer, key).Scan(&op, &request, &saved)
	if e == nil {
		m.mu.Unlock()
		if op != operation || request != string(params) {
			return nil, model.Err("idempotency_conflict", "mesh key belongs to a different operation")
		}
		if saved == nil {
			return nil, model.Err("remote_outcome_unknown", "previous operation has a durable intent but no verified outcome; inspect its lease before another change")
		}
		var response struct {
			Value json.RawMessage `json:"value"`
			Error *model.Error    `json:"error"`
		}
		if e = json.Unmarshal([]byte(*saved), &response); e != nil {
			return nil, e
		}
		if response.Error != nil {
			return nil, response.Error
		}
		return response.Value, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		m.mu.Unlock()
		return nil, e
	}
	admission, _ := json.Marshal(map[string]any{"peer": peer, "host": m.host, "certificate": certificate, "grant_revision": revision, "admitted_at": time.Now().UTC()})
	_, e = m.db.Exec(`INSERT INTO mesh_operations(principal,key,operation,request,admission) VALUES(?,?,?,?,?)`, peer, key, operation, string(params), string(admission))
	m.mu.Unlock()
	if e != nil {
		return nil, e
	}
	value, callErr := fn()
	var api *model.Error
	if callErr != nil {
		if a, ok := callErr.(*model.Error); ok {
			api = a
		} else {
			api = &model.Error{Code: "operation_failed", Message: fmt.Sprint(callErr)}
		}
	}
	raw, e := json.Marshal(struct {
		Value any          `json:"value"`
		Error *model.Error `json:"error,omitempty"`
	}{value, api})
	if e != nil {
		return nil, e
	}
	m.mu.Lock()
	_, e = m.db.Exec(`UPDATE mesh_operations SET result=? WHERE principal=? AND key=?`, string(raw), peer, key)
	m.mu.Unlock()
	if e != nil {
		return nil, model.Err("remote_outcome_unknown", "operation ran but result could not be persisted; inspect lease")
	}
	return value, callErr
}

func (m *Manager) TrackMeshInfrastructure(address net.Addr) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := address.(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("mesh listener is not TCP")
	}
	network := "tcp6"
	if a.IP.To4() != nil {
		network = "tcp4"
	}
	namespace := m.portNamespace
	if m.portErr != nil {
		namespace = "unknown"
	}
	keep := m.infrastructure[:0]
	for _, binding := range m.infrastructure {
		if binding.Name != "arm-mesh" {
			keep = append(keep, binding)
		}
	}
	m.infrastructure = keep
	m.infrastructure = append(m.infrastructure, model.PortBinding{PortClaim: model.PortClaim{Name: "arm-mesh", Network: network, Address: a.IP.String(), Port: a.Port, Mode: "hold", Namespace: namespace}, State: "infrastructure", Detail: "Protected ARM mutually authenticated direct-peer listener"})
	return nil
}

func (m *Manager) RemoveMeshInfrastructure() {
	m.mu.Lock()
	defer m.mu.Unlock()
	keep := m.infrastructure[:0]
	for _, binding := range m.infrastructure {
		if binding.Name != "arm-mesh" {
			keep = append(keep, binding)
		}
	}
	m.infrastructure = keep
}

func (m *Manager) PeerRenew(peer, id string, ttl time.Duration, limit model.Resources, maxTTL time.Duration, runGranted bool) (model.Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, e := m.get(id)
	if e != nil {
		return lease, e
	}
	if lease.Request.Peer != peer {
		return model.Lease{}, model.Err("forbidden", "lease belongs to another principal")
	}
	if ttl <= 0 || ttl > maxTTL {
		return lease, model.Err("forbidden", "renewal exceeds current peer TTL grant")
	}
	var command sql.NullString
	if e = m.db.QueryRow(`SELECT run FROM leases WHERE id=?`, id).Scan(&command); e != nil {
		return lease, e
	}
	if command.Valid {
		var stored RunRequest
		if e = json.Unmarshal([]byte(command.String), &stored); e != nil {
			return lease, e
		}
		if stored.SSH != nil {
			return lease, model.Err("forbidden", "SSH executions have fixed invocation deadlines; ordinary run grants cannot renew them")
		}
	}
	if command.Valid && !runGranted {
		return lease, model.Err("forbidden", "execution renewal requires explicit run grant")
	}
	leases, e := m.list()
	if e != nil {
		return lease, e
	}
	var used model.Resources
	for _, l := range leases {
		if l.Request.Peer == peer && consuming(l.State) {
			used = used.Add(l.Request.Resources)
		}
	}
	if !used.Fits(limit) {
		return lease, model.Err("forbidden", "current peer commitments exceed its present aggregate grant; existing obligations remain but cannot be extended")
	}
	return m.renew(id, ttl)
}
