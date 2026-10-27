package manager

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

func (m *Manager) ConfigureNodeStop(boundary *nodestop.Boundary) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if boundary == nil || m.nodeStopBoundary != nil || m.closing || m.draining {
		return model.Err("node_stop_unavailable", "one backend lifetime boundary must be installed before admission")
	}
	m.nodeStopBoundary = boundary
	return nil
}

// nodeStopCensus runs under Manager.mu. Caller additionally holds the server's
// native-registration and mesh lifecycle journal fences until commit. That
// external scope is not an untrusted wire value.
func (m *Manager) nodeStopCensus(external nodestop.Impact, view nodestop.LeaseReadScope) (nodestop.Impact, []model.Lease, error) {
	ls, err := m.list()
	if err != nil {
		return nodestop.Impact{}, nil, err
	}
	impact := external
	impact.Obligations = append([]nodestop.Obligation{}, external.Obligations...)
	for n := range impact.Obligations {
		if v := impact.Obligations[n].Lease; v != nil {
			copy := *v
			impact.Obligations[n].Lease = &copy
		}
	}
	impact.Blockers = append([]nodestop.Blocker{}, external.Blockers...)
	impact.Peers = append([]string{}, external.Peers...)
	var syncUsed string
	syncErr := m.db.QueryRow(`SELECT value FROM metadata WHERE key='sync_used'`).Scan(&syncUsed)
	if syncErr != nil && !errors.Is(syncErr, sql.ErrNoRows) {
		return impact, nil, syncErr
	}
	if syncErr == nil {
		if syncUsed != "1" {
			return impact, nil, model.Err("sync_evidence_unavailable", "sync use marker changed")
		}
		impact.Blockers = append(impact.Blockers, nodestop.Blocker{Class: "unknown", ID: "sync:retained", Detail: "Sync operation/native cleanup evidence remains retained; Stop cannot settle it without an exact join and cleanup proof."})
	}
	// Native account lookup is process-global and cannot be cancelled by its
	// caller. The original goroutine retains this bounded slot until it returns.
	// Caller holds the existing final server registration/admission fence, so a
	// new SSH lookup cannot enter after this snapshot and before Stop commit.
	accountLookupPending := len(sshAccountSlot) != 0
	if accountLookupPending {
		impact.Blockers = append(impact.Blockers, nodestop.Blocker{Class: "unjoined", ID: "ssh-account-lookup", Detail: "An SSH account lookup is still finishing. Its original native call remains tracked; caller cancellation does not establish completion."})
	}
	private := []model.Lease{}
	for _, lease := range ls {
		switch lease.State {
		case "queued", "active", "provisioning", "ending", "needs_attention", "terminal":
		default:
			return impact, nil, model.Err("node_stop_scope_incomplete", "unknown durable lease state cannot be treated as settled")
		}
		if !outstanding(lease) {
			continue
		}
		item := nodestop.Obligation{ID: "lease:" + lease.ID, Kind: "lease", State: lease.State, Revision: int64(lease.Revision), LeaseDetails: "not-permitted"}
		if view.Allows(lease.Request.Peer) {
			item.LeaseDetails = "permitted"
			item.Lease = &nodestop.LeaseSummary{Owner: lease.Owner, Username: lease.OwnerUsername, Reason: lease.Request.Reason, Agent: lease.Request.Agent, Project: lease.Request.Project}
		}
		impact.Obligations = append(impact.Obligations, item)
		lease.Usage = nil // sampled usage is not a different reclamation authority
		private = append(private, lease)
		if lease.State == "needs_attention" || lease.Host != m.host || lease.Owner != m.owner {
			impact.Blockers = append(impact.Blockers, nodestop.Blocker{Class: "unknown", ID: "lease:" + lease.ID, Detail: "Unresolved or foreign work remains charged; force cannot erase it."})
		}
	}
	_, present, readErr := m.nodeStopDesired()
	if readErr != nil {
		return impact, nil, readErr
	}
	if present {
		all, e := nodestop.ReadRecords(m.db)
		if e != nil {
			return impact, nil, e
		}
		for _, older := range all {
			if older.Phase != "drain-ready" {
				impact.Blockers = append(impact.Blockers, nodestop.Blocker{Class: "unknown", ID: "stop:" + older.Plan.Key, Detail: "An earlier stop has no joined-generation evidence; restart retained its unresolved duty."})
			}
		}
	}
	boundNodeStopPreviews(impact.Obligations)
	impact.Outstanding = len(impact.Obligations)
	sort.Slice(impact.Obligations, func(i, j int) bool { return impact.Obligations[i].ID < impact.Obligations[j].ID })
	sort.Slice(private, func(i, j int) bool { return private[i].ID < private[j].ID })
	sort.Slice(impact.Blockers, func(i, j int) bool {
		if impact.Blockers[i].Class != impact.Blockers[j].Class {
			return impact.Blockers[i].Class < impact.Blockers[j].Class
		}
		return impact.Blockers[i].ID < impact.Blockers[j].ID
	})
	encoded, err := json.Marshal(struct {
		External                string        `json:"external"`
		Leases                  []model.Lease `json:"leases"`
		SSHAccountLookupPending bool          `json:"ssh_account_lookup_pending"`
		SyncUsed                bool          `json:"sync_used,omitempty"`
	}{external.SHA256, private, accountLookupPending, syncErr == nil})
	if err != nil {
		return impact, nil, err
	}
	sum := sha256.Sum256(encoded)
	impact.SHA256 = hex.EncodeToString(sum[:])
	return impact, ls, nil
}

func (m *Manager) nodeStopDesired() (nodestop.State, bool, error) {
	s, present, err := nodestop.ReadState(m.db)
	if err == nil && present && (s.Target.Host != m.host || s.Target.Owner != m.owner || s.Target.Home != m.home) {
		err = model.Err("node_stop_scope_changed", "stop ledger belongs to another exact authority")
	}
	return s, present, err
}

func (m *Manager) PlanNodeStop(key string, actor nodestop.Actor, policy model.ShutdownRequest, reason string, external nodestop.Impact, views ...nodestop.LeaseReadScope) (nodestop.Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || m.draining || m.nodeStopUnknown || m.nodeStopBoundary == nil {
		return nodestop.Plan{}, model.Err("backend_stopping", "authority is not admitting a new stop")
	}
	s, present, err := m.nodeStopDesired()
	if err != nil {
		return nodestop.Plan{}, err
	}
	revision := uint64(1)
	if present {
		if !s.Running {
			return nodestop.Plan{}, model.Err("node_stopped", "durable stopped intent requires original-key inspection")
		}
		revision = s.Revision
	}
	impact, _, err := m.nodeStopCensus(external, nodeStopReadScope(actor, views))
	if err != nil {
		return nodestop.Plan{}, err
	}
	return m.nodeStopBoundary.Prepare(key, actor, revision, policy, reason, impact, time.Now())
}

// BeginNodeStop must be invoked inside the SAME authenticated Store.WithPeer
// grant snapshot and native-registration/journal fence as the current census.
// It commits the original request, ordinary drain and desired-stopped intent
// together before any cancellation or provider effect. Known keys never replay.
func (m *Manager) BeginNodeStop(q nodestop.Apply, actor nodestop.Actor, granted bool, external nodestop.Impact, views ...nodestop.LeaseReadScope) (nodestop.Record, error) {
	// Match ordinary shutdown and Close: no native warning CLI can start after
	// this accepted stop crosses its m.draining transition.
	m.pressureLaunchMu.Lock()
	defer m.pressureLaunchMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	var empty nodestop.Record
	s, present, err := m.nodeStopDesired()
	if err != nil {
		return empty, err
	}
	if present {
		all, e := nodestop.ReadRecords(m.db)
		if e != nil {
			return empty, e
		}
		if len(all) >= nodestop.MaxRecords {
			if _, e = nodestop.Lookup(m.db, q.Key); e != nil {
				return empty, model.Err("node_stop_history_full", "stop history is full; retained obligations were not removed")
			}
		}
		previous, e := nodestop.Lookup(m.db, q.Key)
		if e == nil {
			digest, e := q.Plan.Digest()
			if e != nil || q.Key != q.Plan.Key || digest != q.ReviewSHA256 || previous.Digest != digest || previous.Plan.Actor != q.Plan.Actor || actor.Host != previous.Plan.Actor.Host || actor.SPKI != previous.Plan.Actor.SPKI || actor.Certificate != previous.Plan.Actor.Certificate || !granted {
				return empty, model.Err("idempotency_conflict", "original stop key and authenticated request must match; no effect was replayed")
			}
			return previous, nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return empty, e
		}
	}
	if m.closing || m.draining || m.nodeStopUnknown || m.nodeStopBoundary == nil {
		return empty, model.Err("backend_stopping", "authority is not admitting new work")
	}
	revision := uint64(1)
	running := true
	if present {
		revision = s.Revision
		running = s.Running
	}
	impact, leases, err := m.nodeStopCensus(external, nodeStopReadScope(actor, views))
	if err != nil {
		return empty, err
	}
	current := nodestop.Current{Target: q.Plan.Target, Actor: actor, NodeStopGranted: granted, DesiredRevision: revision, DesiredRunning: running, Impact: impact}
	a, err := m.nodeStopBoundary.Admit(q, current, time.Now())
	if err != nil {
		return empty, err
	}
	if !m.nodeStopBoundary.Owns(a) {
		return empty, model.Err("node_generation_changed", "admission belongs to another retained lifetime")
	}
	plan, err := a.Plan()
	if err != nil {
		return empty, err
	}
	report, deadline := m.newShutdownRecord(plan.Policy, leases)
	now := time.Now().UTC()
	record := nodestop.Record{Schema: nodestop.RecordSchema, Plan: plan, Digest: q.ReviewSHA256, Shutdown: report.ID, DesiredRevision: revision + 1, Phase: "accepted", Created: now, Updated: now}
	state := nodestop.State{Schema: nodestop.StateSchema, Revision: revision + 1, Running: false, Target: plan.Target, Key: plan.Key, Digest: record.Digest}
	if err = state.Validate(record); err != nil {
		return empty, err
	}
	rawRecord, err := json.Marshal(record)
	if err != nil {
		return empty, err
	}
	rawState, err := json.Marshal(state)
	if err != nil {
		return empty, err
	}
	rawShutdown, err := json.Marshal(report)
	if err != nil {
		return empty, err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if !present {
		for _, query := range nodestop.Tables {
			if _, err = tx.Exec(query); err != nil {
				return empty, err
			}
		}
		if _, err = tx.Exec(`INSERT INTO node_stop_schema(singleton,version) VALUES(1,1)`); err != nil {
			return empty, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO node_stop_operations(key,data) VALUES(?,?)`, plan.Key, string(rawRecord)); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(`INSERT INTO node_stop_state(singleton,data) VALUES(1,?) ON CONFLICT(singleton) DO UPDATE SET data=excluded.data`, string(rawState)); err != nil {
		return empty, err
	}
	if err = saveShutdownTx(tx, report, "shutdown.started", rawShutdown); err != nil {
		return empty, err
	}
	if err = event(tx, "node_stop.accepted", plan.Key, record); err != nil {
		return empty, err
	}
	// A commit error may have happened after durability. Pause admission BEFORE
	// calling Commit; uncertainty must not trigger provider work or clear intent.
	if err = m.nodeStopBoundary.CheckAdmission(a); err != nil {
		return empty, err
	}
	m.draining = true
	if err = tx.Commit(); err != nil {
		m.nodeStopUnknown = true
		return empty, model.Err("node_stop_outcome_unknown", "stop commit outcome unknown; retained original key requires inspection, no provider effects dispatched")
	}
	m.traceNodeStop("accepted-commit")
	m.nodeStopAccepted = true
	m.nodeStopAcceptedTarget = q.Plan.Target
	m.shutdownDeadline = deadline
	m.cancelShutdownQueue(&report)
	if err = m.saveShutdown(report, "shutdown.queue_cancelled"); err != nil {
		return record, model.Err("node_stop_in_progress", "stop accepted; queued cancellation reporting failed, destination continuation remains active")
	}
	return record, nil
}

func (m *Manager) LookupNodeStop(key string, actor nodestop.Actor) (nodestop.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, present, err := m.nodeStopDesired()
	if err != nil {
		return nodestop.Record{}, err
	}
	if !present {
		return nodestop.Record{}, model.Err("not_found", "stop key not recorded; absence is not proof of nonexecution")
	}
	r, err := nodestop.Lookup(m.db, key)
	if errors.Is(err, sql.ErrNoRows) {
		return r, model.Err("not_found", "stop key not recorded; absence is not proof of nonexecution")
	}
	if err != nil {
		return r, err
	}
	if actor.Host != r.Plan.Actor.Host || actor.Certificate != r.Plan.Actor.Certificate || actor.SPKI != r.Plan.Actor.SPKI {
		return nodestop.Record{}, model.Err("forbidden", "stop receipt belongs to another authenticated principal")
	}
	return r, nil
}

// NodeStopDrainReady persists only ordinary drain readiness. Server/native
// observation must independently establish joined exact-generation absence.
func (m *Manager) NodeStopDrainReady(report model.ShutdownReport) (nodestop.Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, present, err := m.nodeStopDesired()
	if err != nil {
		return nodestop.Record{}, present, err
	}
	if m.nodeStopUnknown {
		return nodestop.Record{}, true, model.Err("node_stop_outcome_unknown", "current stop admission is unresolved; readiness cannot settle it")
	}
	if !present {
		if m.nodeStopAccepted {
			return nodestop.Record{}, true, model.Err("node_stop_scope_changed", "current admitted stop ledger is missing")
		}
		return nodestop.Record{}, false, nil
	}
	r, err := nodestop.Lookup(m.db, s.Key)
	if err != nil {
		return r, true, err
	}
	if s.Running && !m.nodeStopAccepted && (r.Phase == "interrupted" || r.Phase == "drain-ready") {
		// Validated restart history is retained, but is not a Stop admitted by
		// this Manager lifetime. Ordinary local shutdown needs no Stop closure.
		return nodestop.Record{}, false, nil
	}
	if !m.nodeStopAccepted || s.Target != m.nodeStopAcceptedTarget {
		return nodestop.Record{}, true, model.Err("node_stop_scope_changed", "readiness has no matching current-generation stop admission")
	}
	if r.Shutdown != report.ID || s.Running || r.Phase == "interrupted" {
		return r, true, model.Err("node_stop_scope_changed", "drain is not the original stop operation")
	}
	if report.Phase != "ready" || len(report.Remaining) != 0 {
		return r, true, model.Err("node_stop_blocked", "ordinary obligations are not settled")
	}
	if r.Phase == "drain-ready" {
		return r, true, nil
	}
	r.Phase = "drain-ready"
	r.Updated = time.Now().UTC()
	b, err := json.Marshal(r)
	if err != nil {
		return r, true, err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return r, true, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE node_stop_operations SET data=? WHERE key=?`, string(b), r.Plan.Key); err != nil {
		return r, true, err
	}
	if err = event(tx, "node_stop.drain_ready", r.Plan.Key, r); err != nil {
		return r, true, err
	}
	err = tx.Commit()
	if err == nil {
		m.traceNodeStop("ready-commit")
	}
	return r, true, err
}

// NodeStopHistory is a detached closed ledger snapshot; callers still need
// native marker/lock evidence before claiming an earlier generation joined.
func (m *Manager) NodeStopHistory() ([]nodestop.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, present, err := m.nodeStopDesired()
	if err != nil {
		return nil, err
	}
	if !present {
		return []nodestop.Record{}, nil
	}
	return nodestop.ReadRecords(m.db)
}

func (m *Manager) NodeStopDesiredState() (nodestop.State, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.nodeStopDesired()
}
