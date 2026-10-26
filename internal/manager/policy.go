package manager

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

type incidentClock struct{ since, last, warned time.Time }

func memoryExceeds(value uint64, limit int64, tolerance float64) bool {
	// Keep integral budgets exact even above float64's consecutive-integer
	// range. Tolerance is the exact configured float64 fraction.
	factor := new(big.Rat).Add(big.NewRat(1, 1), new(big.Rat).SetFloat64(tolerance))
	threshold := factor.Mul(factor, new(big.Rat).SetInt64(limit))
	observed := new(big.Rat).SetInt(new(big.Int).SetUint64(value))
	return observed.Cmp(threshold) > 0
}

func (m *Manager) resolveUsagePolicy(r *model.Request, supervised bool) error {
	if r.UsagePolicy == nil {
		return nil
	}
	p := *r.UsagePolicy
	if !supervised || r.Agent == "" || (r.Resources.CPU == 0 && r.Resources.Memory == 0) {
		return model.Err("invalid_policy", "usage policy requires a supervised run, a registered --agent, and a CPU or memory budget")
	}
	if p.Mode != "warn" && p.Mode != "revoke" {
		return model.Err("invalid_policy", "usage policy mode must be warn or revoke")
	}
	if p.Window == 0 {
		p.Window = 5 * time.Second
	}
	if p.Grace == 0 {
		p.Grace = 30 * time.Second
	}
	if p.Warnings == 0 {
		p.Warnings = 3
	}
	if p.Window < time.Second || p.Window > time.Hour || p.Grace < time.Second || p.Grace > 24*time.Hour || p.Warnings < 1 || p.Warnings > 10 || math.IsNaN(p.Tolerance) || math.IsInf(p.Tolerance, 0) || p.Tolerance < 0 || p.Tolerance > 1 {
		return model.Err("invalid_policy", "window must be 1s..1h, grace 1s..24h, warnings 1..10, and tolerance 0..1")
	}
	r.UsagePolicy = &p
	return nil
}

// Caller holds m.mu. Revisions and notification creation commit together.
func (m *Manager) saveIncident(i model.UsageIncident, kind string, msg *Message, target *agents.Contact) error {
	b, err := json.Marshal(i)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO usage_incidents(id,lease,resource,data) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, i.ID, i.Lease, i.Resource, string(b)); err != nil {
		return err
	}
	if kind != "" {
		if err = event(tx, kind, i.ID, i); err != nil {
			return err
		}
	}
	if msg != nil {
		if err = insertMessage(tx, *msg, []agents.Contact{*target}); err != nil {
			return err
		}
		if target.Delivery != "poll" {
			b, err := json.Marshal(policyDelivery{Incident: i.ID, Target: *target})
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO policy_outbox(message,state,data) VALUES(?,'queued',?)`, msg.ID, string(b)); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if msg != nil {
		m.notifyInbox()
	}
	return nil
}

func (m *Manager) incident(id string) (model.UsageIncident, error) {
	var i model.UsageIncident
	var b []byte
	err := m.db.QueryRow(`SELECT data FROM usage_incidents WHERE id=?`, id).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &i)
	}
	return i, err
}
func (m *Manager) incidents(lease string, open bool) ([]model.UsageIncident, error) {
	query := `SELECT data FROM usage_incidents WHERE (?='' OR lease=?)`
	if open {
		query += ` AND json_extract(data,'$.state')<>'closed'`
	}
	query += ` ORDER BY rowid DESC`
	if !open {
		query += ` LIMIT 100`
	}
	rows, err := m.db.Query(query, lease, lease)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.UsageIncident{}
	for rows.Next() {
		var b []byte
		var i model.UsageIncident
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &i); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
func (m *Manager) Incidents(lease string) ([]model.UsageIncident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lease != "" {
		if _, err := m.get(lease); err != nil {
			return nil, err
		}
	}
	return m.incidents(lease, false)
}

// Evaluates one NEW sampler observation while holding m.mu. No samples are
// fabricated for unsupervised leases. Monotonic streaks are never recovered
// from wall-clock timestamps or a saved PID.
func (m *Manager) evaluateUsage(id string, view model.WorkloadUsage, at time.Time) error {
	l, err := m.get(id)
	if err != nil {
		return err
	}
	j := m.jobs[id]
	if l.State != "active" || l.Request.UsagePolicy == nil || j == nil || j.identity == nil || (l.Expires != nil && !time.Now().Before(*l.Expires)) {
		return nil
	}
	open, err := m.incidents(id, true)
	if err != nil {
		return err
	}
	byResource := map[string]model.UsageIncident{}
	for _, i := range open {
		byResource[i.Resource] = i
	}
	valid := view.Live && !view.Stale && view.IdentityVerified && view.Root == *j.identity
	for _, resource := range []string{"cpu", "memory"} {
		limit, value, unit, known := l.Request.Resources.CPU, float64(0), "logical-core", false
		if resource == "cpu" {
			if view.CPUCores != nil && view.Window > 0 && view.Window <= 3*time.Second && view.CPUQuality == "lower-bound-overlap-average" {
				value, known = *view.CPUCores, true
			}
		} else {
			limit, unit = float64(l.Request.Resources.Memory), "byte"
			if view.AccountedMemoryBytes != nil && (view.MemoryMetric == "sum-private-resident" || view.MemoryMetric == "sum-process-physical-footprint") && (view.MemoryQuality == "reported-sum" || view.MemoryQuality == "partial-reported-sum") {
				value, known = float64(*view.AccountedMemoryBytes), true
			}
		}
		if limit <= 0 {
			continue
		}
		i, exists := byResource[resource]
		clock := m.policyClocks[i.ID]
		if exists && !clock.last.IsZero() && !at.After(clock.last) {
			continue
		}
		known = known && valid && !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
		threshold := limit * (1 + l.Request.UsagePolicy.Tolerance)
		// Rounding at the comparison boundary must not invent an overage.
		breach := known && value > math.Nextafter(threshold, math.Inf(1))
		quantity := model.FloatQuantity(limit)
		if resource == "memory" {
			quantity = model.CountQuantity(l.Request.Resources.Memory)
			breach = known && memoryExceeds(*view.AccountedMemoryBytes, l.Request.Resources.Memory, l.Request.UsagePolicy.Tolerance)
		}
		if !breach {
			if !exists {
				continue
			}
			i.Updated, i.Revision = at.UTC(), i.Revision+1
			i.BreachSince = nil
			clock.since, clock.last = time.Time{}, at
			kind := ""
			if known && !view.Complete {
				known = false
			}
			if known {
				i.State, i.Outcome, i.Detail = "closed", "observed_within_threshold", "Fresh complete scoped observation is within the threshold; this does not prove complete physical accounting"
				i.Deadline = nil
				evidence := view
				evidence.Live, evidence.Members = false, nil
				i.Evidence = &evidence
				kind = "incident.observed_within_threshold"
			} else {
				i.Detail = "Observation unavailable or unsuitable; continuity reset, escalation suspended, existing lease expiry unchanged"
			}
			if err = m.saveIncident(i, kind, nil, nil); err != nil {
				return err
			}
			if known {
				delete(m.policyClocks, i.ID)
			} else {
				m.policyClocks[i.ID] = clock
			}
			continue
		}
		kind := ""
		if !exists {
			c, err := m.contact(l.Request.Agent)
			if err != nil {
				return err
			}
			i = model.UsageIncident{ID: local.ID(), Lease: id, Host: l.Host, Owner: l.Owner, OwnerUsername: l.OwnerUsername, Agent: c.ID, Session: c.Session, Registration: c.Registration, Resource: resource, Unit: unit, Limit: quantity, Threshold: threshold, Policy: *l.Request.UsagePolicy, State: "observing", Opened: at.UTC(), Messages: []string{}}
			clock = incidentClock{}
			kind = "incident.opened"
		}
		if clock.since.IsZero() || clock.last.IsZero() || at.Sub(clock.last) > 3*time.Second {
			clock.since = at
		}
		clock.last = at
		since := clock.since.UTC()
		i.BreachSince, i.Updated, i.Revision = &since, at.UTC(), i.Revision+1
		evidence := view
		evidence.Live, evidence.Members = false, nil
		i.Evidence = &evidence
		i.Detail = "Attributable observed usage exceeds the policy threshold; incomplete samples are lower bounds, not complete physical accounting"
		var message *Message
		var target *agents.Contact
		if at.Sub(clock.since) >= i.Policy.Window {
			due := i.WarningCount == 0 || (!clock.warned.IsZero() && at.Sub(clock.warned) >= i.Policy.Grace)
			if due && i.WarningCount < i.Policy.Warnings {
				c, err := m.contact(i.Agent)
				if err != nil {
					return err
				}
				if c.Session != i.Session || c.Registration != i.Registration {
					return model.Err("contact_changed", "incident recipient identity changed")
				}
				i.State, i.WarningCount = "warning", i.WarningCount+1
				deadline := at.UTC().Add(i.Policy.Grace)
				i.Deadline, clock.warned = &deadline, at
				consequence := "ARM will record continuing overage; this policy does not revoke"
				if i.Policy.Mode == "revoke" {
					consequence = "ARM will revoke this lease and stop its owned supervised group after the final warning grace period"
				}
				purpose := []rune(l.Request.Reason)
				if len(purpose) > 512 {
					purpose = append(purpose[:512], []rune("… (full purpose in lease)")...)
				}
				body := fmt.Sprintf("ARM usage warning %d/%d; incident %s; lease %s\nResource %s: observed %g %s, granted %s, approximate threshold %g. Observed at %s. Method: %s; CPU quality: %s; memory metric: %s. Reduce usage below the threshold or release the lease. Remediation deadline: %s. %s. Check current lease/incident state before acting. Acknowledgement or renewal does not establish compliance or suspend this deadline. Purpose (caller-supplied data): %q", i.WarningCount, i.Policy.Warnings, i.ID, id, resource, value, unit, quantity.String(), threshold, view.Observed.Format(time.RFC3339Nano), view.Method, view.CPUQuality, view.MemoryMetric, deadline.Format(time.RFC3339Nano), consequence, string(purpose))
				delivery := agents.Delivery{State: "pending", Mode: c.Delivery, Registration: c.Registration}
				if c.Delivery == "poll" {
					delivery.State = "available"
				}
				msg := Message{ID: local.ID(), Owner: m.owner, OwnerUsername: m.ownerUsername, Sender: "arm-policy", Audience: c.ID, Body: body, Lease: id, Created: at.UTC(), Deliveries: map[string]agents.Delivery{c.ID: delivery}}
				message, target = &msg, &c
				i.Messages = append(append([]string{}, i.Messages...), msg.ID)
				kind = "incident.warning"
			} else if due && i.Policy.Mode == "revoke" && i.WarningCount >= i.Policy.Warnings {
				i.State, i.Detail = "revocation_pending", "Final grace elapsed with a fresh sustained attributable breach; revocation requested"
				kind = "incident.revocation_requested"
			}
		}
		if err = m.saveIncident(i, kind, message, target); err != nil {
			return err
		}
		m.policyClocks[i.ID] = clock
		if i.State == "revocation_pending" {
			if _, err = m.end(id, "revoked"); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// Reconcile durable decisions without ever adopting a recovered PID. Caller
// holds m.mu; only the existing live job object can authorize termination.
func (m *Manager) reconcileIncidents() error {
	incidents, err := m.incidents("", true)
	if err != nil {
		return err
	}
	for _, i := range incidents {
		l, err := m.get(i.Lease)
		if err != nil {
			return err
		}
		if i.State == "revocation_pending" && l.State == "active" && m.jobs[l.ID] != nil {
			l, err = m.end(l.ID, "revoked")
			if err != nil {
				return err
			}
		}
		if l.State == "active" && m.jobs[l.ID] != nil {
			continue
		}
		state, outcome := "closed", l.Outcome
		detail := "Lease no longer active; consult lease state for actual resource settlement"
		if l.State == "ending" && l.Outcome == "revoked" {
			state, outcome, detail = "reclaiming", "", "Revocation is terminating owned work; allocations remain charged until settlement"
		}
		if l.State == "needs_attention" || (l.State == "active" && m.jobs[l.ID] == nil) {
			outcome, detail = "supervision_unavailable", "No live supervision authority; incident closed without signalling a persisted PID; unresolved allocations remain charged"
		}
		if i.State == state && i.Outcome == outcome {
			continue
		}
		i.State, i.Outcome, i.Detail = state, outcome, detail
		i.Updated, i.Revision = time.Now().UTC(), i.Revision+1
		i.BreachSince, i.Deadline = nil, nil
		if err = m.saveIncident(i, "incident."+state, nil, nil); err != nil {
			return err
		}
		delete(m.policyClocks, i.ID)
	}
	return nil
}
