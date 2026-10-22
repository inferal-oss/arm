package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/yrashk/arm/internal/model"
)

// The absolute deadline is durable; the process-local clock additionally bounds
// a live wait through backwards wall-clock changes. UTC wall time also catches
// sleep on systems where Go's monotonic source does not include suspension.
func (m *Manager) schedulingTime() (time.Time, time.Time) {
	now := time.Now()
	wall, elapsed := now.UTC(), now
	if m.scheduleWallNow != nil {
		wall = m.scheduleWallNow().Round(0).UTC()
	}
	if m.scheduleElapsedNow != nil {
		elapsed = m.scheduleElapsedNow()
	}
	return wall, elapsed
}
func (m *Manager) observeSchedulingClock() (time.Time, time.Time) {
	wall, elapsed := m.schedulingTime()
	if !m.scheduleHighwater.IsZero() && wall.Before(m.scheduleHighwater) {
		m.scheduleRollback = true
	}
	if wall.After(m.scheduleHighwater) {
		m.scheduleHighwater = wall
	}
	return wall, elapsed
}
func (m *Manager) initScheduling() error {
	m.startClocks = map[string]time.Time{}
	var raw string
	err := m.db.QueryRow(`SELECT value FROM metadata WHERE key='scheduler_clock'`).Scan(&raw)
	if err == nil {
		m.scheduleCheckpoint, err = time.Parse(time.RFC3339Nano, raw)
		m.scheduleHighwater = m.scheduleCheckpoint
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return m.reconcileQueued()
}
func (m *Manager) checkpointSchedulingClock(at time.Time) error {
	if !m.scheduleCheckpoint.IsZero() && at.Sub(m.scheduleCheckpoint) < time.Second {
		return nil
	}
	_, err := m.db.Exec(`INSERT INTO metadata(key,value) VALUES('scheduler_clock',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, at.Format(time.RFC3339Nano))
	if err == nil {
		m.scheduleCheckpoint = at
	}
	return err
}
func (m *Manager) rememberStartClock(l model.Lease, wall, elapsed time.Time) {
	if l.Request.StartDeadline == nil {
		return
	}
	if m.startClocks == nil {
		m.startClocks = map[string]time.Time{}
	}
	if _, exists := m.startClocks[l.ID]; !exists {
		m.startClocks[l.ID] = elapsed.Add(l.Request.StartDeadline.Sub(wall))
	}
}
func (m *Manager) startDeadlineProblem(l model.Lease) *model.Error {
	if l.Request.StartDeadline == nil {
		return nil
	}
	wall, elapsed := m.observeSchedulingClock()
	m.rememberStartClock(l, wall, elapsed)
	if m.scheduleRollback {
		return &model.Error{Code: "scheduler_clock_rollback", Message: "The scheduler observed wall time move backwards; this deadline wait was rejected rather than extended. Correct the host clock and restart the backend before submitting another deadline."}
	}
	if !wall.Before(*l.Request.StartDeadline) || !elapsed.Before(m.startClocks[l.ID]) {
		return &model.Error{Code: "start_deadline_exceeded", Message: "The latest permitted start time passed before this request could start. No new execution is authorized."}
	}
	return nil
}
func rejectedQueueInfo(at time.Time, reason *model.Error) *model.QueueInfo {
	return &model.QueueInfo{Policy: "fifo-no-bypass", Blockers: []model.QueueBlocker{{Code: reason.Code, Detail: reason.Message}}, Estimate: model.QueueEstimate{Kind: "unknown", Detail: "This request was rejected; it has no future reservation."}, Updated: at, Rejection: reason}
}
func (m *Manager) rejectUnstarted(l model.Lease, reason *model.Error) (model.Lease, error) {
	wall, _ := m.schedulingTime()
	l.QueueInfo = rejectedQueueInfo(wall, reason)
	l.Failure = reason.Message
	l.Started = nil
	l.Expires = nil
	l.Ended = &wall
	l.Outcome = "rejected"
	l.State = "terminal"
	l.Revision++
	// A provider may have created an owned artifact before a slow operation
	// crossed the deadline. Use normal reclamation and retain uncertainty.
	if l.Artifact != nil && !l.Artifact.NamespaceRemoved {
		l.State = "ending"
		l.Ended = nil
		return m.endStorage(l, "rejected")
	}
	if len(l.Ports) > 0 {
		m.finishPorts(&l)
	}
	err := m.put(l, "lease.rejected")
	if err == nil {
		delete(m.startClocks, l.ID)
	}
	return l, err
}

// Revalidate every waiting request before FIFO dispatch, including tail
// requests that were admitted while physical inventory was unknown. A growing
// custom coordination pool is intentionally not a permanent native ceiling.
func (m *Manager) reconcileQueued() error {
	wall, elapsed := m.observeSchedulingClock()
	leases, err := m.list()
	if err != nil {
		return err
	}
	pendingDeadline := false
	for _, l := range leases {
		if l.State == "queued" && l.Request.StartDeadline != nil {
			pendingDeadline = true
			m.rememberStartClock(l, wall, elapsed)
		}
	}
	if pendingDeadline {
		if err = m.checkpointSchedulingClock(m.scheduleHighwater); err != nil {
			return err
		}
	}
	keep := map[string]bool{}
	for _, l := range leases {
		if l.State == "queued" || l.State == "provisioning" {
			keep[l.ID] = true
		}
		if l.State != "queued" {
			continue
		}
		reason := m.startDeadlineProblem(l)
		if reason == nil && !m.requestFitsCeiling(l.Request.Resources) {
			reason = &model.Error{Code: "capacity_exceeded", Message: "Known physical inventory or a configured host ceiling cannot satisfy this queued request. Submit a new request with feasible quantities."}
		}
		if reason != nil {
			if _, err = m.rejectUnstarted(l, reason); err != nil {
				return err
			}
		}
	}
	for id := range m.startClocks {
		if !keep[id] {
			delete(m.startClocks, id)
		}
	}
	return m.refreshQueueInfo()
}

type queueSnapshot struct {
	leases   []model.Lease
	used     model.Resources
	poolUsed map[string]int64
	holders  map[string][]string
	counts   map[string]int
}

func schedulingSnapshot(leases []model.Lease) (queueSnapshot, error) {
	s := queueSnapshot{leases: leases, holders: map[string][]string{}, counts: map[string]int{}}
	var err error
	s.poolUsed, err = poolReservations(leases)
	if err != nil {
		return s, err
	}
	add := func(resource, id string) {
		s.counts[resource]++
		if len(s.holders[resource]) < 32 {
			s.holders[resource] = append(s.holders[resource], id)
		}
	}
	for _, l := range leases {
		if consuming(l.State) {
			s.used = s.used.Add(l.Request.Resources)
			if l.Request.Resources.CPU > 0 {
				add("cpu", l.ID)
			}
			if l.Request.Resources.Memory > 0 {
				add("memory", l.ID)
			}
			if l.Request.Resources.Disk > 0 {
				add("disk", l.ID)
			}
			for _, claim := range l.Request.Pools {
				add(claim.Pool, l.ID)
			}
		}
	}
	diskReserved, err := diskPromises(leases)
	if err != nil {
		return s, err
	}
	s.used.Disk = diskReserved
	return s, nil
}
func number(value json.Number) *json.Number { return &value }
func (m *Manager) explainQueued(l model.Lease, position int, before []string, s queueSnapshot, at time.Time) (*model.QueueInfo, error) {
	info := &model.QueueInfo{Policy: "fifo-no-bypass", Position: position, Blockers: []model.QueueBlocker{}, Estimate: model.QueueEstimate{Kind: "unknown", Detail: "No reliable start estimate is available; waiting is not a future reservation."}, Updated: at}
	add := func(code, resource, unit, detail string, want, available *json.Number) {
		key := resource
		if resource == m.host+"/cpu" {
			key = "cpu"
		}
		if resource == m.host+"/memory" {
			key = "memory"
		}
		if resource == m.host+"/disk" {
			key = "disk"
		}
		info.Blockers = append(info.Blockers, model.QueueBlocker{Code: code, Resource: resource, Requested: want, Available: available, Unit: unit, Leases: append([]string(nil), s.holders[key]...), BlockingCount: s.counts[key], Detail: detail})
	}
	if m.draining {
		add("backend_draining", "", "", "The backend is draining and does not admit new grants.", nil, nil)
	}
	if position > 1 {
		info.Blockers = append(info.Blockers, model.QueueBlocker{Code: "queue_order", Leases: append([]string(nil), before...), BlockingCount: position - 1, Detail: "Earlier waiting requests have priority; later requests do not bypass the FIFO head."})
	}
	if l.Request.Resources.CPU > max(0, m.capacity.CPU-s.used.CPU) || s.used.CPU > m.capacity.CPU {
		add("budget_reserved", m.host+"/cpu", "logical-core-equivalents", "CPU budget is committed to existing leases.", number(model.FloatQuantity(l.Request.Resources.CPU)), number(model.FloatQuantity(max(0, m.capacity.CPU-s.used.CPU))))
	}
	if l.Request.Resources.Disk > 0 {
		budget := m.diskBudget(s.used.Disk)
		if !budget.Known {
			add("observation_unavailable", m.host+"/disk", "bytes", "Fresh nonprivileged state-filesystem headroom is required before disk admission.", number(model.CountQuantity(l.Request.Resources.Disk)), nil)
		} else if l.Request.Resources.Disk > budget.Available {
			add("disk_headroom", m.host+"/disk", "bytes", "State-filesystem headroom is insufficient after full outstanding promises; free space and filesystem size can change.", number(model.CountQuantity(l.Request.Resources.Disk)), number(model.CountQuantity(budget.Available)))
		}
		if len(info.Blockers) > 0 && info.Blockers[len(info.Blockers)-1].Resource == m.host+"/disk" {
			last := &info.Blockers[len(info.Blockers)-1]
			last.Stale = budget.Stale
			if !budget.Observation.At.IsZero() {
				stamp := budget.Observation.At
				last.ObservationAt = &stamp
			}
		}
	}
	if l.Request.Resources.Memory > 0 {
		budget := m.memoryBudget(s.used.Memory)
		if budget.Stale || budget.Observation.Available == nil || budget.Observation.Total == nil || *budget.Observation.Total <= 0 || *budget.Observation.Available < 0 || *budget.Observation.Available > *budget.Observation.Total {
			add("observation_unavailable", m.host+"/memory", "bytes", "A fresh valid memory-headroom observation is required before admission.", number(model.CountQuantity(l.Request.Resources.Memory)), nil)
		} else if l.Request.Resources.Memory > budget.Available {
			add("memory_headroom", m.host+"/memory", "bytes", "Current memory headroom is insufficient after conservative outstanding promises.", number(model.CountQuantity(l.Request.Resources.Memory)), number(model.CountQuantity(budget.Available)))
		}
		if len(info.Blockers) > 0 && info.Blockers[len(info.Blockers)-1].Resource == m.host+"/memory" {
			last := &info.Blockers[len(info.Blockers)-1]
			last.Stale = budget.Stale
			if !budget.Observation.At.IsZero() {
				stamp := budget.Observation.At
				last.ObservationAt = &stamp
			}
		}
	}
	poolBlockers, poolErr := m.subpoolQueueBlockers(l, s)
	if poolErr != nil {
		info.Blockers = append(info.Blockers, model.QueueBlocker{Code: "invalid_accounting", Detail: "Pool lineage or partition accounting is invalid; grants remain paused while provider cleanup remains available."})
	} else {
		info.Blockers = append(info.Blockers, poolBlockers...)
	}
	policyBlockers, err := m.queuedBudgetBlockers(l, s.leases)
	if err != nil {
		policyBlockers = []model.QueueBlocker{{Code: "invalid_accounting", Detail: "Policy lineage or outstanding accounting is invalid; admission is paused. Inspect budget evidence and reconcile provider obligations without rebinding allocations."}}
	}
	info.Blockers = append(info.Blockers, policyBlockers...)
	if len(l.Request.Ports) > 0 {
		detail := "Port availability is checked at binding time; no socket is held by this queued request."
		if l.Failure != "" {
			detail = l.Failure
		}
		add("port_admission", m.host+"/ports", "socket-binding", detail, nil, nil)
	}
	if l.Request.StorageLease != "" {
		if _, err := m.storageForRun(l.Request); err != nil {
			add("storage_dependency", l.Request.StorageLease, "", err.Error(), nil, nil)
		}
	}
	if l.Request.PortLease != "" {
		if _, err := m.portParent(l.Request, l.ID); err != nil {
			add("port_dependency", l.Request.PortLease, "", err.Error(), nil, nil)
		}
	}
	if len(info.Blockers) == 0 {
		add("dispatch_pending", "", "", "Budget currently fits; the scheduler must still make a fresh atomic admission decision.", nil, nil)
	}
	if position == 1 && !m.draining && len(policyBlockers) == 0 {
		info.Estimate = m.estimateQueued(l, s, at)
	}
	return info, nil
}
func (m *Manager) estimateQueued(l model.Lease, s queueSnapshot, now time.Time) model.QueueEstimate {
	unknown := model.QueueEstimate{Kind: "unknown", Detail: "Live memory/disk headroom, provider reclamation, port availability or earlier queue work cannot be predicted reliably."}
	if l.Request.Resources.Disk > 0 || l.Request.Resources.Memory > 0 || len(l.Request.Ports) > 0 || len(l.Request.Pools) > 0 || l.Request.StorageLease != "" || l.Request.PortLease != "" || l.Request.Kind == "storage" {
		return unknown
	}
	capacity := m.capacity
	capacity.Memory = 0
	used := s.used
	used.Memory = 0
	candidates := []model.Lease{}
	for _, other := range s.leases {
		if consuming(other.State) && (other.Request.Resources.CPU > 0 || other.Request.Resources.Disk > 0) {
			if other.State != "active" || other.PID != 0 || other.Process != nil || other.Artifact != nil || len(other.Ports) > 0 || other.Expires == nil {
				return unknown
			}
			candidates = append(candidates, other)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Expires.Before(*candidates[j].Expires) })
	want := l.Request.Resources
	want.Memory = 0
	at := now
	if want.FitsRemaining(used, capacity) {
		return model.QueueEstimate{Kind: "unknown", Detail: "Budget currently fits; dispatch still requires a fresh atomic provider/admission check."}
	}
	if !want.FitsRemaining(used, capacity) {
		found := false
		for _, other := range candidates {
			used.CPU = max(0, used.CPU-other.Request.Resources.CPU)
			used.Disk = max(int64(0), used.Disk-other.Request.Resources.Disk)
			at = now
			if other.Expires.After(at) {
				at = *other.Expires
			}
			if want.FitsRemaining(used, capacity) {
				found = true
				break
			}
		}
		if !found {
			return unknown
		}
	}
	detail := "Advisory only: assumes ordinary raw reservations end at their recorded expiries. Early release, renewal, new pressure and failed cleanup can change this; no future capacity is reserved."
	if l.Request.StartDeadline != nil && at.After(*l.Request.StartDeadline) {
		detail += " This estimate is later than the start deadline; only earlier capacity could let the request start."
	}
	return model.QueueEstimate{Kind: "advisory", Start: &at, Detail: detail}
}
func queueMeaning(info *model.QueueInfo) any {
	if info == nil {
		return nil
	}
	copy := *info
	copy.Updated = time.Time{}
	copy.Blockers = append([]model.QueueBlocker(nil), info.Blockers...)
	for i := range copy.Blockers {
		b := &copy.Blockers[i]
		b.Available = nil
		b.ObservationAt = nil
	}
	return copy
}
func (m *Manager) refreshQueueInfo() error {
	leases, err := m.list()
	if err != nil {
		return err
	}
	s, err := schedulingSnapshot(leases)
	if err != nil {
		return err
	}
	wall, _ := m.schedulingTime()
	position := 0
	before := []string{}
	for _, l := range leases {
		if l.State != "queued" {
			continue
		}
		position++
		info, err := m.explainQueued(l, position, before, s, wall)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(queueMeaning(l.QueueInfo), queueMeaning(info)) {
			l.QueueInfo = info
			l.Revision++
			if err = m.put(l, "queue.changed"); err != nil {
				return err
			}
		}
		if len(before) < 32 {
			before = append(before, l.ID)
		}
	}
	return nil
}
func (m *Manager) queueViews(leases []model.Lease) error {
	s, err := schedulingSnapshot(leases)
	if err != nil {
		return err
	}
	wall, _ := m.schedulingTime()
	position := 0
	before := []string{}
	for i := range leases {
		l := &leases[i]
		if l.State != "queued" {
			continue
		}
		position++
		l.QueueInfo, err = m.explainQueued(*l, position, before, s, wall)
		if err != nil {
			return err
		}
		if len(before) < 32 {
			before = append(before, l.ID)
		}
	}
	return nil
}
func canonicalStartDeadline(r *model.Request) error {
	if r.StartDeadline == nil {
		return nil
	}
	deadline := r.StartDeadline.Round(0).UTC()
	if _, err := deadline.MarshalJSON(); err != nil {
		return model.Err("invalid_request", fmt.Sprintf("start deadline must be an RFC3339 timestamp: %v", err))
	}
	r.StartDeadline = &deadline
	return nil
}
