package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

const reviewMaxPolicies = 1024
const reviewMaxPending = 64
const reviewMaxBatch = 16
const reviewMaxClosed = 1024
const reviewMaxContext = 64 << 10

// Leave room for the wire envelope under the common 4MiB client limit.
const reviewPageBytes = 1 << 20

const reviewUnavailableMessage = "Optional lease check-ins are unavailable because their stored schema is unsupported or incomplete. Evidence was preserved; ordinary lease operations continue."

func (m *Manager) reviewAvailabilityError() error {
	if m.leaseReviewsUnavailable {
		return model.Err("reviews_unavailable", reviewUnavailableMessage)
	}
	return nil
}

func (m *Manager) initLeaseReviews() (err error) {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			// A real transaction failure must not be downgraded by the optional
			// schema sentinel, including an early read-only preflight refusal.
			err = rollbackErr
		}
	}()
	// Inspect the optional namespace before any DDL. Unknown markers and partial
	// groups are evidence, not an invitation to fill in or migrate them.
	// Return two integers, never the unbounded marker bytes. Match exact key
	// bytes across SQLite storage classes: a BLOB alias must not hide beside or
	// replace the TEXT key. Only one physically TEXT key/value pair is supported.
	var markers, supported int64
	err = tx.QueryRow(`SELECT count(*),coalesce(sum(CASE WHEN typeof(key)='text' AND typeof(value)='text' AND length(CAST(value AS BLOB))=1 AND CAST(value AS BLOB)=x'31' THEN 1 ELSE 0 END),0) FROM metadata WHERE CAST(key AS BLOB)=CAST('lease_review_schema' AS BLOB)`).Scan(&markers, &supported)
	if err != nil {
		return err
	}
	if markers > 1 || markers == 1 && supported != 1 {
		return database.ErrLeaseReviewSchema
	}
	marker := markers == 1
	present, err := database.CheckLeaseReviewSchema(context.Background(), tx)
	if err != nil {
		return err
	}
	if present != marker {
		return database.ErrLeaseReviewSchema
	}
	if present {
		return tx.Commit()
	}
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS lease_review_policies(lease TEXT PRIMARY KEY,generation INTEGER NOT NULL,enabled INTEGER NOT NULL,data TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS lease_review_policy_active ON lease_review_policies(enabled,lease)`,
		`CREATE TABLE IF NOT EXISTS lease_reviews(sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT UNIQUE NOT NULL,agent TEXT NOT NULL,registration TEXT NOT NULL,state TEXT NOT NULL,data TEXT NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS lease_reviews_pending ON lease_reviews(agent) WHERE state='pending'`,
		`INSERT INTO metadata(key,value) VALUES('lease_review_schema','1') ON CONFLICT(key) DO NOTHING`,
	} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func reviewEligible(l model.Lease, now time.Time) bool {
	return l.State == "active" && l.Expires != nil && now.Before(*l.Expires) && l.Request.Agent != ""
}
func (m *Manager) reviewPolicy(id string) (model.LeaseReviewPolicy, error) {
	var p model.LeaseReviewPolicy
	var raw []byte
	var generation int64
	var enabled bool
	err := m.db.QueryRow(`SELECT generation,enabled,data FROM lease_review_policies WHERE lease=?`, id).Scan(&generation, &enabled, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return model.LeaseReviewPolicy{Lease: id}, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if p.Generation != generation || p.Enabled != enabled || p.Lease != id || generation <= 0 {
		return p, model.Err("review_policy_invalid", "review policy columns differ from durable data")
	}
	return p, nil
}
func (m *Manager) ConfigureLeaseReview(q model.LeaseReviewConfigure) (model.LeaseReviewPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out model.LeaseReviewPolicy
	if err := m.reviewAvailabilityError(); err != nil {
		return out, err
	}
	if m.closing {
		return out, model.Err("backend_stopping", "backend is shutting down")
	}
	if q.Generation == nil || *q.Generation < 0 {
		return out, model.Err("invalid_request", "exact policy_generation is required (zero only when policy has never existed)")
	}
	l, err := m.get(q.Lease)
	if err != nil {
		return out, err
	}
	if q.Revision <= 0 || l.Revision != q.Revision {
		return out, model.Err("revision_conflict", "inspect the current lease revision before configuring reviews")
	}
	old, err := m.reviewPolicy(l.ID)
	if err != nil {
		return out, err
	}
	if old.Generation != *q.Generation {
		return old, model.Err("policy_conflict", "review policy changed; inspect its current generation before another change")
	}
	now := time.Now().UTC()
	if q.Enabled {
		if !reviewEligible(l, now) {
			return out, model.Err("lease_inactive", "reviews require an unexpired active lease attributed to an exact registered agent")
		}
		if _, err = m.contact(l.Request.Agent); err != nil {
			return out, err
		}
		if q.Interval == 0 {
			q.Interval = 10 * time.Minute
		}
		if q.ReplyTimeout == 0 {
			q.ReplyTimeout = time.Minute
		}
		if q.Interval < 15*time.Second || q.Interval > 7*24*time.Hour || q.ReplyTimeout < 5*time.Second || q.ReplyTimeout > time.Hour || q.ContentionInterval < 0 || (q.ContentionInterval != 0 && (q.ContentionInterval < 15*time.Second || q.ContentionInterval > q.Interval)) {
			return out, model.Err("invalid_request", "interval must be 15s..7d, reply timeout 5s..1h, contention interval zero or 15s..interval")
		}
		ctx := reviewContext(l, now, 0)
		raw, marshalErr := json.Marshal(ctx)
		if marshalErr != nil {
			return out, marshalErr
		}
		if len(raw) > reviewMaxContext {
			return out, model.Err("review_context_too_large", "lease claims exceed the bounded review context")
		}
	} else {
		q.Interval = 0
		q.ContentionInterval = 0
		q.ReplyTimeout = 0
	}
	if old.Generation > 0 && old.Enabled == q.Enabled && old.Revision == q.Revision && old.Interval == q.Interval && old.ContentionInterval == q.ContentionInterval && old.ReplyTimeout == q.ReplyTimeout {
		return old, nil
	}
	if old.Generation == math.MaxInt64 {
		return out, model.Err("review_generation_exhausted", "review policy generation cannot advance")
	}
	if q.Enabled && !old.Enabled {
		var n int
		if err = m.db.QueryRow(`SELECT count(*) FROM lease_review_policies WHERE enabled=1`).Scan(&n); err != nil {
			return out, err
		}
		if n >= reviewMaxPolicies {
			return out, model.Err("review_capacity", "active review policy capacity reached; disable an active policy first")
		}
	}
	out = model.LeaseReviewPolicy{Generation: old.Generation + 1, Lease: l.ID, Revision: l.Revision, Enabled: q.Enabled, Interval: q.Interval, ContentionInterval: q.ContentionInterval, ReplyTimeout: q.ReplyTimeout, Updated: now}
	if q.Enabled {
		out.Next = now.Add(q.Interval)
	}
	tx, err := m.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if old.Generation == 0 {
		_, err = tx.Exec(`INSERT INTO lease_review_policies VALUES(?,?,?,?)`, l.ID, out.Generation, out.Enabled, string(raw))
	} else {
		var result sql.Result
		result, err = tx.Exec(`UPDATE lease_review_policies SET generation=?,enabled=?,data=? WHERE lease=? AND generation=?`, out.Generation, out.Enabled, string(raw), l.ID, old.Generation)
		if err == nil {
			n, e := result.RowsAffected()
			err = e
			if n != 1 && err == nil {
				err = model.Err("policy_conflict", "review policy changed during transaction")
			}
		}
	}
	if err != nil {
		return out, err
	}
	if err = event(tx, "lease_review.configured", l.ID, out); err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func reviewContext(l model.Lease, now time.Time, demand int) model.LeaseReviewContext {
	u := model.LeaseReviewUsage{CPUQuality: "unavailable", MemoryQuality: "unavailable", Stale: true, Detail: "No attributable usage measurement; reservation is not measured consumption."}
	if l.Usage != nil {
		s := l.Usage
		t := s.Observed
		u = model.LeaseReviewUsage{Observed: &t, CPUCores: s.CPUCores, CPUQuality: s.CPUQuality, MemoryBytes: s.AccountedMemoryBytes, MemoryQuality: s.MemoryQuality, Stale: s.Stale || now.Sub(t) > 3*time.Second || now.Before(t), Complete: s.Complete, Detail: s.Detail}
		if len(u.Detail) > 512 {
			u.Detail = u.Detail[:512]
		}
	}
	return model.LeaseReviewContext{Lease: l.ID, Revision: l.Revision, Owner: l.Owner, Purpose: l.Request.Reason, Resources: l.Request.Resources, Pools: l.Request.Pools, Ports: l.Request.Ports, Expires: *l.Expires, Remaining: l.Expires.Sub(now), Usage: u, QueuedDemand: demand}
}
func (m *Manager) review(id string) (model.LeaseReview, error) {
	var x model.LeaseReview
	var raw []byte
	err := m.db.QueryRow(`SELECT sequence,data FROM lease_reviews WHERE id=?`, id).Scan(&x.Sequence, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return x, model.Err("not_found", "review is absent or outside the retained review window; inspect the event journal")
	}
	if err != nil {
		return x, err
	}
	seq := x.Sequence
	err = json.Unmarshal(raw, &x)
	x.Sequence = seq
	return x, err
}
func (m *Manager) LeaseReviews(q model.LeaseReviewQuery) (model.LeaseReviewPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := model.LeaseReviewPage{Items: []model.LeaseReview{}, Cursor: q.After, MaxPending: reviewMaxPending, RetainedClosed: reviewMaxClosed, SchedulerError: m.leaseReviewError}
	if err := m.reviewAvailabilityError(); err != nil {
		return out, err
	}
	if q.After < 0 || len(q.Agent) > 128 || len(q.Lease) > 128 {
		return out, model.Err("invalid_request", "invalid review cursor or selector")
	}
	query := `SELECT sequence,data FROM lease_reviews WHERE sequence>?`
	args := []any{q.After}
	if q.Agent != "" {
		query += ` AND agent=?`
		args = append(args, q.Agent)
	}
	if q.Pending {
		query += ` AND state='pending'`
	}
	if q.Lease != "" {
		query += ` AND EXISTS(SELECT 1 FROM json_each(lease_reviews.data,'$.leases') WHERE json_extract(value,'$.lease')=?)`
		args = append(args, q.Lease)
	}
	query += ` ORDER BY sequence LIMIT 65`
	// Measure the actual encoded metadata, including the diagnostic. Reserve
	// the widest legal cursor and false (one byte longer than true), so later
	// cursor/More changes cannot exceed this whole-page byte bound.
	metadata := out
	metadata.Cursor = math.MaxInt64
	metadata.More = false
	encodedMetadata, metadataErr := json.Marshal(metadata)
	if metadataErr != nil {
		return out, metadataErr
	}
	pageBytes := len(encodedMetadata)
	if pageBytes >= reviewPageBytes {
		return out, model.Err("review_metadata_too_large", "checkpoint page metadata exceeds its bounded response")
	}
	maxRecordBytes := reviewPageBytes - pageBytes
	rows, err := m.db.Query(query, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var raw []byte
		if err = rows.Scan(&seq, &raw); err != nil {
			return out, err
		}
		if len(out.Items) == 64 {
			out.More = true
			break
		}
		var x model.LeaseReview
		if err = json.Unmarshal(raw, &x); err != nil {
			return out, err
		}
		x.Sequence = seq
		encoded, encodeErr := json.Marshal(x)
		if encodeErr != nil {
			return out, encodeErr
		}
		if len(encoded) > maxRecordBytes {
			return out, model.Err("review_record_too_large", "a whole checkpoint exceeds the bounded response; ledger is not silently truncated")
		}
		additionalBytes := len(encoded)
		if len(out.Items) > 0 {
			additionalBytes++
		} // comma between complete items
		if pageBytes+additionalBytes > reviewPageBytes {
			out.More = true
			break
		}
		pageBytes += additionalBytes
		out.Items = append(out.Items, x)
		out.Cursor = seq
	}
	return out, rows.Err()
}
func (m *Manager) InspectLeaseReview(id string) (model.LeaseReview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reviewAvailabilityError(); err != nil {
		return model.LeaseReview{}, err
	}
	return m.review(id)
}
func (m *Manager) reviewCurrent(x model.LeaseReview, now time.Time) string {
	if now.Before(x.Created) {
		return "clock moved before the captured review time"
	}
	c, err := m.contact(x.Agent)
	if err != nil || c.Session != x.Session || c.Registration != x.Registration {
		return "registered recipient changed"
	}
	for _, ctx := range x.Context {
		policy, err := m.reviewPolicy(ctx.Lease)
		if err != nil || !policy.Enabled || policy.Generation != ctx.PolicyGeneration {
			return "lease review policy generation changed or was disabled"
		}
		l, err := m.get(ctx.Lease)
		if err != nil || !reviewEligible(l, now) || l.Owner != ctx.Owner || l.Request.Agent != x.Agent || l.Revision != ctx.Revision || !l.Expires.Equal(ctx.Expires) {
			return "lease changed or entitlement ended; refresh before any action"
		}
	}
	return ""
}
func saveReview(tx *sql.Tx, x model.LeaseReview, kind string) error {
	raw, err := json.Marshal(x)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE lease_reviews SET state=?,data=? WHERE id=?`, x.State, string(raw), x.ID); err != nil {
		return err
	}
	return event(tx, kind, x.ID, x)
}

// Reply is a durable explanation only. It deliberately cannot call renew/end,
// resize, enforcement or a native agent adapter. Ordinary LeaseAction is separate.
func (m *Manager) ReplyLeaseReview(q model.LeaseReviewReply) (model.LeaseReview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var x model.LeaseReview
	if err := m.reviewAvailabilityError(); err != nil {
		return x, err
	}
	if m.closing {
		return x, model.Err("backend_stopping", "backend is shutting down")
	}
	if q.Registration == "" || len(q.Explanation) > 4096 || strings.TrimSpace(q.Explanation) == "" || (q.Intent != "needed" && q.Intent != "release" && q.Intent != "renew" && q.Intent != "resize") {
		return x, model.Err("invalid_request", "exact registration, an intent and a 1..4096 byte explanation are required")
	}
	if _, err := m.inboxContact(q.Agent, q.Session, q.Registration); err != nil {
		return x, err
	}
	var err error
	x, err = m.review(q.ID)
	if err != nil {
		return x, err
	}
	if x.Agent != q.Agent || x.Session != q.Session || x.Registration != q.Registration {
		return x, model.Err("contact_changed", "review belongs to another exact session")
	}
	if x.Reply != nil {
		if *x.Reply == q {
			return x, nil
		}
		return x, model.Err("reply_conflict", "review already has a different recorded reply")
	}
	now := time.Now().UTC()
	if x.State != "pending" {
		return x, model.Err("review_closed", "review is closed; intent cannot revive entitlement")
	}
	state, detail := "", ""
	if detail = m.reviewCurrent(x, now); detail != "" {
		state = "superseded"
	} else if !now.Before(x.Deadline) {
		state = "timed_out"
		detail = "reply deadline passed; lease expiry remains unchanged"
	}
	if state != "" {
		x.State = state
		x.Detail = detail
		x.Closed = &now
		tx, e := m.db.Begin()
		if e != nil {
			return x, e
		}
		defer tx.Rollback()
		if e = saveReview(tx, x, "lease_review.closed"); e == nil {
			e = tx.Commit()
		}
		if e != nil {
			return x, e
		}
		return x, model.Err("review_closed", detail)
	}
	x.State = "replied"
	x.Reply = &q
	x.Closed = &now
	tx, err := m.db.Begin()
	if err != nil {
		return x, err
	}
	defer tx.Rollback()
	if err = saveReview(tx, x, "lease_review.replied"); err != nil {
		return x, err
	}
	msg := Message{ID: local.ID(), Owner: m.owner, OwnerUsername: m.ownerUsername, Sender: q.Agent, ReplyTo: x.Message, Audience: "operator", Body: fmt.Sprintf("Lease review %s — %s: %s\nIntent only; inspect current leases and use ordinary guarded actions.", x.ID, q.Intent, q.Explanation), Created: now, Deliveries: map[string]agents.Delivery{}}
	if err = insertMessage(tx, msg, nil); err != nil {
		return x, err
	}
	if err = tx.Commit(); err != nil {
		return x, err
	}
	m.notifyInbox()
	return x, nil
}

// Caller holds m.mu. Checkpoints, fairness timestamps and inbox rows commit
// together. Polling is durable and has zero native invocations/retries.
func (m *Manager) tickLeaseReviews(now time.Time, ls []model.Lease) error {
	if err := m.reviewAvailabilityError(); err != nil {
		return err
	}
	rows, err := m.db.Query(`SELECT data FROM lease_reviews WHERE state='pending' ORDER BY sequence LIMIT 64`)
	if err != nil {
		return err
	}
	pending := []model.LeaseReview{}
	for rows.Next() {
		var raw []byte
		var x model.LeaseReview
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal(raw, &x)
		}
		if err != nil {
			break
		}
		pending = append(pending, x)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	busy := map[string]bool{}
	for _, x := range pending {
		why := m.reviewCurrent(x, now)
		state := "superseded"
		if why == "" && !now.Before(x.Deadline) {
			why = "reply deadline passed; no response changes lease expiry"
			state = "timed_out"
		}
		if why == "" {
			busy[x.Agent] = true
			continue
		}
		x.State = state
		x.Detail = why
		x.Closed = &now
		tx, e := m.db.Begin()
		if e != nil {
			return e
		}
		e = saveReview(tx, x, "lease_review.closed")
		if e == nil {
			e = tx.Commit()
		}
		tx.Rollback()
		if e != nil {
			return e
		}
	}
	// Detailed closed checkpoints are a bounded working set; complete context and
	// responses remain in the ordinary durable event journal (and message inbox).
	if _, err = m.db.Exec(`DELETE FROM lease_reviews WHERE state<>'pending' AND sequence NOT IN(SELECT sequence FROM lease_reviews WHERE state<>'pending' ORDER BY sequence DESC LIMIT 1024)`); err != nil {
		return err
	}
	if m.draining {
		return nil
	}
	rows, err = m.db.Query(`SELECT data FROM lease_review_policies WHERE enabled=1 ORDER BY lease LIMIT 1024`)
	if err != nil {
		return err
	}
	policies := []model.LeaseReviewPolicy{}
	for rows.Next() {
		var raw []byte
		var x model.LeaseReviewPolicy
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal(raw, &x)
		}
		if err != nil {
			break
		}
		policies = append(policies, x)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return err
	}
	byID := map[string]model.Lease{}
	for _, l := range ls {
		byID[l.ID] = l
	}
	type due struct {
		policy model.LeaseReviewPolicy
		lease  model.Lease
		at     time.Time
	}
	duePolicies := []due{}
	for _, p := range policies {
		l, ok := byID[p.Lease]
		if !ok || !reviewEligible(l, now) {
			p.Enabled = false
			p.Next = time.Time{}
			p.Updated = now
			if ok {
				p.Revision = l.Revision
			}
			if p.Generation < math.MaxInt64 {
				p.Generation++
			}
			raw, e := json.Marshal(p)
			if e != nil {
				return e
			}
			tx, e := m.db.Begin()
			if e != nil {
				return e
			}
			_, e = tx.Exec(`UPDATE lease_review_policies SET generation=?,enabled=0,data=? WHERE lease=?`, p.Generation, string(raw), p.Lease)
			if e == nil {
				e = event(tx, "lease_review.policy_retired", p.Lease, p)
			}
			if e == nil {
				e = tx.Commit()
			}
			tx.Rollback()
			if e != nil {
				return e
			}
			continue
		}
		if !p.Enabled {
			continue
		}
		next := p.Next
		if p.ContentionInterval > 0 && reviewDemand(l, ls) > 0 {
			earlier := p.Next.Add(-p.Interval).Add(p.ContentionInterval)
			if earlier.Before(next) {
				next = earlier
			}
		}
		if now.Before(p.Updated) {
			next = now
		} // review timing only; never modifies lease lifetime.
		if !now.Before(next) {
			duePolicies = append(duePolicies, due{p, l, next})
		}
	}
	// Oldest persisted due time first; issued policies move to now+interval,
	// unselected members retain their old time. This rotates contacts and batches.
	sort.Slice(duePolicies, func(i, j int) bool {
		if duePolicies[i].at.Equal(duePolicies[j].at) {
			return duePolicies[i].lease.ID < duePolicies[j].lease.ID
		}
		return duePolicies[i].at.Before(duePolicies[j].at)
	})
	made := 0
	for _, first := range duePolicies {
		agent := first.lease.Request.Agent
		if busy[agent] || len(busy) >= reviewMaxPending || made >= 8 {
			continue
		}
		c, e := m.contact(agent)
		if e != nil {
			return e
		}
		x := model.LeaseReview{ID: local.ID(), Host: m.host, Agent: c.ID, Session: c.Session, Registration: c.Registration, Message: local.ID(), Created: now, Deadline: now.Add(first.policy.ReplyTimeout), State: "pending", Context: []model.LeaseReviewContext{}}
		selected := []model.LeaseReviewPolicy{}
		for _, d := range duePolicies {
			if d.lease.Request.Agent != agent || len(x.Context) >= reviewMaxBatch {
				continue
			}
			l := d.lease
			m.attachUsage(&l)
			ctx := reviewContext(l, now, reviewDemand(l, ls))
			ctx.PolicyGeneration = d.policy.Generation
			trial := append(append([]model.LeaseReviewContext{}, x.Context...), ctx)
			raw, marshalErr := json.Marshal(trial)
			if marshalErr != nil {
				return marshalErr
			}
			if len(raw) > reviewMaxContext {
				continue
			}
			x.Context = trial
			selected = append(selected, d.policy)
			end := now.Add(d.policy.ReplyTimeout)
			if end.Before(x.Deadline) {
				x.Deadline = end
			}
			if ctx.Expires.Before(x.Deadline) {
				x.Deadline = ctx.Expires
			}
		}
		if len(selected) == 0 {
			continue
		}
		msg := Message{ID: x.Message, Owner: m.owner, OwnerUsername: m.ownerUsername, Sender: "arm-lease-review", Audience: c.ID, Created: now, Body: fmt.Sprintf("Lease review %s: are these %d reservations still needed, and why? Inspect this checkpoint with arm reviews inspect %s. Reply using arm reviews reply with your exact session and registration. This is optional: acknowledgement, intent and silence never renew a lease, prove compliance or authorize a resource action. Use ordinary revision-checked lease actions separately.", x.ID, len(x.Context), x.ID), Deliveries: map[string]agents.Delivery{c.ID: {State: "available", Mode: "poll", Registration: c.Registration}}}
		tx, e := m.db.Begin()
		if e != nil {
			return e
		}
		raw, marshalErr := json.Marshal(x)
		if marshalErr != nil {
			tx.Rollback()
			return marshalErr
		}
		if _, e = tx.Exec(`INSERT INTO lease_reviews(id,agent,registration,state,data) VALUES(?,?,?,'pending',?)`, x.ID, x.Agent, x.Registration, string(raw)); e == nil {
			e = insertMessage(tx, msg, []agents.Contact{c})
		}
		for _, p := range selected {
			if e != nil {
				break
			}
			p.Next = now.Add(p.Interval)
			p.Updated = now
			raw, marshalErr := json.Marshal(p)
			if marshalErr != nil {
				e = marshalErr
				break
			}
			_, e = tx.Exec(`UPDATE lease_review_policies SET data=? WHERE lease=?`, string(raw), p.Lease)
		}
		if e == nil {
			e = event(tx, "lease_review.created", x.ID, x)
		}
		if e == nil {
			e = tx.Commit()
		}
		tx.Rollback()
		if e != nil {
			return e
		}
		busy[agent] = true
		made++
		m.notifyInbox()
	}
	return nil
}
func reviewDemand(l model.Lease, ls []model.Lease) int {
	n := 0
	for _, q := range ls {
		if q.State != "queued" {
			continue
		}
		a, b := l.Request.Resources, q.Request.Resources
		match := (a.CPU > 0 && b.CPU > 0) || (a.Memory > 0 && b.Memory > 0) || (a.Disk > 0 && b.Disk > 0)
		for _, p := range l.Request.Pools {
			for _, v := range q.Request.Pools {
				if p.Pool == v.Pool {
					match = true
				}
			}
		}
		if len(l.Request.Ports) > 0 && len(q.Request.Ports) > 0 {
			match = true
		}
		if match {
			n++
		}
	}
	return n
}

func (m *Manager) LeaseReviewPolicy(id string) (model.LeaseReviewPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.reviewAvailabilityError(); err != nil {
		return model.LeaseReviewPolicy{}, err
	}
	if _, err := m.get(id); err != nil {
		return model.LeaseReviewPolicy{}, err
	}
	return m.reviewPolicy(id)
}
