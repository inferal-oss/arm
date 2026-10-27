package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"getarm.org/internal/model"
	"github.com/google/uuid"
)

// This independent catalog classifies conversation/history only. It never
// derives release, renewal, resource settlement or cleanup authority from replies.
var leaseReviewResetSchema = []string{
	`CREATE TABLE lease_review_policies(lease TEXT PRIMARY KEY,generation INTEGER NOT NULL,enabled INTEGER NOT NULL,data TEXT NOT NULL)`,
	`CREATE INDEX lease_review_policy_active ON lease_review_policies(enabled,lease)`,
	`CREATE TABLE lease_reviews(sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT UNIQUE NOT NULL,agent TEXT NOT NULL,registration TEXT NOT NULL,state TEXT NOT NULL,data TEXT NOT NULL)`,
	`CREATE UNIQUE INDEX lease_reviews_pending ON lease_reviews(agent) WHERE state='pending'`,
}

var leaseReviewObjectIdentity = [][3]string{
	{"table", "lease_review_policies", "lease_review_policies"},
	{"index", "lease_review_policy_active", "lease_review_policies"},
	{"table", "lease_reviews", "lease_reviews"},
	{"index", "lease_reviews_pending", "lease_reviews"},
}

// ErrLeaseReviewSchema marks only recognized optional namespace evidence. SQL
// transport, transaction and I/O errors remain ordinary errors for startup.
var ErrLeaseReviewSchema = errors.New("unsupported or incomplete optional lease review schema")

// CheckLeaseReviewSchema is read-only and uses the same physical catalog and
// token comparison as whole-ledger classification. It does not classify rows,
// repair a partial group, or make any resource-settlement decision.
func CheckLeaseReviewSchema(ctx context.Context, tx *sql.Tx) (present bool, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_master WHERE sql IS NOT NULL AND (name IN ('lease_review_policies','lease_review_policy_active','lease_reviews','lease_reviews_pending') OR tbl_name IN ('lease_review_policies','lease_reviews'))`)
	if err != nil {
		return false, err
	}
	defer func() {
		// A real cursor/driver failure is core failure, even if malformed optional
		// evidence was already found. Do not join the optional sentinel into it:
		// Manager.Open deliberately downgrades only errors.Is(that sentinel).
		if closeErr := rows.Close(); closeErr != nil {
			present, err = false, closeErr
		}
	}()
	seen := map[int]bool{}
	for rows.Next() {
		var kind, name, table, raw string
		if err := rows.Scan(&kind, &name, &table, &raw); err != nil {
			return false, err
		}
		found := false
		for i, q := range leaseReviewResetSchema {
			identity := leaseReviewObjectIdentity[i]
			if kind == identity[0] && name == identity[1] && table == identity[2] && normalizedSchema(raw) == normalizedSchema(q) && !seen[i] {
				seen[i], found = true, true
				break
			}
		}
		if !found {
			return false, ErrLeaseReviewSchema
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(seen) != 0 && len(seen) != len(leaseReviewResetSchema) {
		return false, ErrLeaseReviewSchema
	}
	return len(seen) != 0, nil
}

func reviewDuration(d time.Duration, low, high time.Duration) bool { return d >= low && d <= high }
func (p *proofReader) readLeaseReviews() error {
	_, present := p.catalog["lease_reviews"]
	if present != p.leaseReviewSchema {
		return fmt.Errorf("lease review schema group/marker differ")
	}
	if !present {
		return nil
	}
	policies := map[string]model.LeaseReviewPolicy{}
	active := 0
	if err := p.walk("lease_review_policies", func(row proofRow) error {
		id, e := row.text("lease")
		if e != nil {
			return e
		}
		generation, e := row.integer("generation")
		if e != nil {
			return e
		}
		enabled, e := row.integer("enabled")
		if e != nil {
			return e
		}
		v, e := rowJSON[model.LeaseReviewPolicy](row, "data")
		if e != nil {
			return e
		}
		l, ok := p.leases[id]
		if !ok || v.Lease != id || generation <= 0 || v.Generation != generation || enabled < 0 || enabled > 1 || v.Enabled != (enabled == 1) || v.Revision <= 0 || v.Revision > l.Revision || v.Updated.IsZero() {
			return fmt.Errorf("invalid lease review policy identity/generation")
		}
		if v.Enabled {
			active++
			if active > 1024 || v.Next.IsZero() || !v.Next.After(v.Updated) || !reviewDuration(v.Interval, 15*time.Second, 7*24*time.Hour) || !reviewDuration(v.ReplyTimeout, 5*time.Second, time.Hour) || v.ContentionInterval < 0 || (v.ContentionInterval != 0 && !reviewDuration(v.ContentionInterval, 15*time.Second, v.Interval)) {
				return fmt.Errorf("invalid active review cadence or bound")
			}
		} else if !v.Next.IsZero() || (v.Interval != 0 && !reviewDuration(v.Interval, 15*time.Second, 7*24*time.Hour)) || (v.ReplyTimeout != 0 && !reviewDuration(v.ReplyTimeout, 5*time.Second, time.Hour)) || (v.ContentionInterval != 0 && !reviewDuration(v.ContentionInterval, 15*time.Second, v.Interval)) {
			return fmt.Errorf("disabled review tombstone has invalid cadence or scheduled work")
		}
		if _, exists := policies[id]; exists {
			return fmt.Errorf("duplicate review policy")
		}
		policies[id] = v
		return nil
	}); err != nil {
		return err
	}
	replyIndex := map[string][]string{}
	for id, msg := range p.messages {
		if msg.Audience == "operator" && msg.ReplyTo != "" {
			replyIndex[msg.ReplyTo] = append(replyIndex[msg.ReplyTo], id)
		}
	}
	seenMessage := map[string]bool{}
	pending := map[string]bool{}
	closed := 0
	return p.walk("lease_reviews", func(row proofRow) error {
		seq, e := row.integer("sequence")
		if e != nil {
			return e
		}
		id, e := row.text("id")
		if e != nil {
			return e
		}
		agent, e := row.text("agent")
		if e != nil {
			return e
		}
		registration, e := row.text("registration")
		if e != nil {
			return e
		}
		state, e := row.text("state")
		if e != nil {
			return e
		}
		v, e := rowJSON[model.LeaseReview](row, "data")
		if e != nil {
			return e
		}
		session, sessionErr := uuid.Parse(v.Session)
		if seq <= 0 || (v.Sequence != 0 && v.Sequence != seq) || !evidenceID(id) || v.ID != id || v.Agent != agent || !evidenceID(agent) || v.Registration != registration || !evidenceID(registration) || sessionErr != nil || session.String() != v.Session || v.Host != p.result.Host || v.State != state || !oneOf(state, "pending", "replied", "timed_out", "superseded") || v.Created.IsZero() || !v.Deadline.After(v.Created) || len(v.Context) < 1 || len(v.Context) > 16 || len(v.Detail) > 4096 {
			return fmt.Errorf("invalid checkpoint identity/shape")
		}
		raw, e := json.Marshal(v.Context)
		if e != nil || len(raw) > 64<<10 {
			return fmt.Errorf("checkpoint context exceeds bounded whole record")
		}
		if seenMessage[v.Message] {
			return fmt.Errorf("checkpoints share an original message")
		}
		seenMessage[v.Message] = true
		msg, ok := p.messages[v.Message]
		delivery, has := msg.Deliveries[agent]
		if !ok || !has || len(msg.Deliveries) != 1 || msg.Sender != "arm-lease-review" || msg.Audience != agent || msg.Lease != "" || delivery.Mode != "poll" || delivery.Registration != registration || !msg.Created.Equal(v.Created) {
			return fmt.Errorf("checkpoint lacks exact polling message")
		}
		seen := map[string]bool{}
		for _, c := range v.Context {
			l, ok := p.leases[c.Lease]
			policy, has := policies[c.Lease]
			if !ok || !has || seen[c.Lease] || c.Owner != p.result.Owner || l.Owner != c.Owner || l.Request.Agent != agent || c.Revision <= 0 || c.Revision > l.Revision || c.PolicyGeneration <= 0 || c.PolicyGeneration > policy.Generation || c.Purpose != l.Request.Reason || !equalEvidence(c.Resources, l.Request.Resources) || !equalEvidence(c.Pools, l.Request.Pools) || !equalEvidence(c.Ports, l.Request.Ports) || !c.Expires.After(v.Created) || c.Remaining != c.Expires.Sub(v.Created) || v.Deadline.After(c.Expires) || c.QueuedDemand < 0 {
				return fmt.Errorf("checkpoint captured lease/policy/context differs")
			}
			seen[c.Lease] = true
			u := c.Usage
			if u.CPUCores != nil && (math.IsNaN(*u.CPUCores) || math.IsInf(*u.CPUCores, 0) || *u.CPUCores < 0) || len(u.Detail) > 512 || len(u.CPUQuality) > 512 || len(u.MemoryQuality) > 512 {
				return fmt.Errorf("invalid captured usage quality")
			}
		}
		if state == "pending" {
			if v.Closed != nil || v.Reply != nil || pending[agent] || len(pending) >= 64 {
				return fmt.Errorf("pending review duplicate or bound")
			}
			pending[agent] = true
		} else {
			closed++
			if closed > 1088 || v.Closed == nil || v.Closed.IsZero() {
				return fmt.Errorf("closed review lacks bounded history/time")
			}
			clockRollback := state == "superseded" && v.Detail == "clock moved before the captured review time"
			if v.Closed.Before(v.Created) != clockRollback {
				return fmt.Errorf("review closure time differs from clock rollback state")
			}
			if state == "timed_out" && v.Closed.Before(v.Deadline) {
				return fmt.Errorf("review timeout precedes reply deadline")
			}
		}
		if state == "replied" {
			q := v.Reply
			if q == nil || q.ID != id || q.Agent != agent || q.Session != v.Session || q.Registration != registration || !oneOf(q.Intent, "needed", "release", "renew", "resize") || strings.TrimSpace(q.Explanation) == "" || len(q.Explanation) > 4096 || !v.Closed.Before(v.Deadline) {
				return fmt.Errorf("invalid exact review reply")
			}
			wanted := fmt.Sprintf("Lease review %s — %s: %s\nIntent only; inspect current leases and use ordinary guarded actions.", id, q.Intent, q.Explanation)
			found := 0
			for _, replyID := range replyIndex[v.Message] {
				reply := p.messages[replyID]
				if reply.ReplyTo == v.Message && reply.Sender == agent && reply.Audience == "operator" && reply.Body == wanted && reply.Created.Equal(*v.Closed) {
					found++
				}
			}
			if found != 1 {
				return fmt.Errorf("review reply lost its exact operator message")
			}
		} else if v.Reply != nil {
			return fmt.Errorf("nonreplied checkpoint carries response")
		}
		// No p.block or accounting result is emitted from optional conversation state.
		return nil
	})
}
