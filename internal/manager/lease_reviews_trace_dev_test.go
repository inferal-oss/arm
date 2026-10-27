//go:build arm_devtrace

package manager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

type leaseReviewTraceRow struct {
	PolicyGeneration int64  `json:"policy_generation"`
	Action           string `json:"action"`
	Live             bool   `json:"live"`
	Revision         int    `json:"revision"`
	Expiry           string `json:"expiry"`
	Phase            string `json:"phase"`
	Enabled          bool   `json:"enabled"`
}

type leaseReviewTraceSnapshot struct {
	lease      model.Lease
	policy     model.LeaseReviewPolicy
	checkpoint *model.LeaseReview
}

// The selected scenario owns one lease/contact and at most one checkpoint.
// Observe actual rows atomically with respect to all manager API/tick writers.
// More/different records are an unexpected scenario, never projected away.
func readLeaseReviewTraceSnapshot(m *Manager, id string) (leaseReviewTraceSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return readLeaseReviewTraceSnapshotLocked(m, id)
}
func readLeaseReviewTraceSnapshotLocked(m *Manager, id string) (leaseReviewTraceSnapshot, error) {
	var out leaseReviewTraceSnapshot
	var err error
	out.lease, err = m.get(id)
	if err != nil {
		return out, err
	}
	if out.lease.Expires == nil {
		return out, fmt.Errorf("selected lease lacks an expiry")
	}
	out.policy, err = m.reviewPolicy(id)
	if err != nil {
		return out, err
	}
	contact, err := m.contact(out.lease.Request.Agent)
	if err != nil {
		return out, err
	}
	if contact.ID != out.lease.Request.Agent || contact.Session == "" || contact.Registration == "" {
		return out, fmt.Errorf("selected contact identity is incomplete")
	}
	// Agent OR lease scope detects crossed column/context attribution instead of
	// silently reporting none. LIMIT2 is enough to refuse a second scoped record.
	rows, err := m.db.Query(`SELECT sequence,id,agent,registration,state,
 CASE WHEN length(CAST(data AS BLOB))<=1048576 THEN data ELSE NULL END
 FROM lease_reviews WHERE agent=? OR EXISTS(
 SELECT 1 FROM json_each(lease_reviews.data,'$.leases') WHERE json_extract(value,'$.lease')=?)
 ORDER BY sequence LIMIT 2`, contact.ID, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		if out.checkpoint != nil {
			return out, fmt.Errorf("unexpected additional durable checkpoint in selected trace scope")
		}
		var seq int64
		var rowID, agent, registration, state string
		var raw []byte
		if err = rows.Scan(&seq, &rowID, &agent, &registration, &state, &raw); err != nil {
			return out, err
		}
		if len(raw) == 0 {
			return out, fmt.Errorf("checkpoint missing or exceeds bounded whole-row snapshot")
		}
		var x model.LeaseReview
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&x); err != nil {
			return out, err
		}
		if err = decoder.Decode(new(any)); err != io.EOF {
			return out, fmt.Errorf("checkpoint is not one closed JSON record")
		}
		if seq <= 0 || rowID == "" || x.ID != rowID || (x.Sequence != 0 && x.Sequence != seq) || agent != contact.ID || x.Agent != agent || registration != contact.Registration || x.Registration != registration || x.Session != contact.Session || x.Host != out.lease.Host || x.State != state || len(x.Context) != 1 || x.Context[0].Lease != id || x.Created.IsZero() || !x.Deadline.After(x.Created) {
			return out, fmt.Errorf("checkpoint columns or exact selected scope differ from durable record")
		}
		switch x.State {
		case "pending":
			if x.Closed != nil || x.Reply != nil {
				return out, fmt.Errorf("pending checkpoint carries terminal evidence")
			}
		case "replied":
			if x.Closed == nil || x.Reply == nil || x.Closed.Before(x.Created) || !x.Closed.Before(x.Deadline) || x.Reply.ID != x.ID || x.Reply.Agent != agent || x.Reply.Session != x.Session || x.Reply.Registration != registration {
				return out, fmt.Errorf("replied checkpoint lacks exact response evidence")
			}
		case "timed_out":
			if x.Closed == nil || x.Closed.Before(x.Deadline) || x.Reply != nil {
				return out, fmt.Errorf("timeout checkpoint lacks deadline closure evidence")
			}
		case "superseded":
			if x.Closed == nil || x.Reply != nil || x.Closed.Before(x.Created) != (x.Detail == "clock moved before the captured review time") {
				return out, fmt.Errorf("superseded checkpoint has invalid closure evidence")
			}
		default:
			return out, fmt.Errorf("unknown durable checkpoint state")
		}
		x.Sequence = seq
		out.checkpoint = &x
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}
func (snapshot leaseReviewTraceSnapshot) observation(action string) leaseReviewTraceRow {
	phase := "none"
	if snapshot.checkpoint != nil {
		phase = snapshot.checkpoint.State
	}
	return leaseReviewTraceRow{snapshot.policy.Generation, action, snapshot.lease.State == "active", snapshot.lease.Revision, snapshot.lease.Expires.UTC().Format(time.RFC3339Nano), phase, snapshot.policy.Enabled}
}

// Assertions restrict this chosen eight-action test scenario, not actual data.
// observation() always derives the phase from durable rows, never a local ID.
func checkLeaseReviewTraceCut(action string, snapshot leaseReviewTraceSnapshot, checkpointID string) error {
	switch action {
	case "initial", "configure", "checkpoint", "acknowledge", "reply", "duplicate_reply", "ordinary_end":
	default:
		return fmt.Errorf("unknown selected trace action")
	}
	if snapshot.lease.Expires == nil || snapshot.policy.Lease != snapshot.lease.ID {
		return fmt.Errorf("missing exact lease/policy snapshot")
	}
	if action == "initial" || action == "configure" {
		if snapshot.checkpoint != nil {
			return fmt.Errorf("unexpected durable checkpoint before explicit checkpoint action")
		}
	} else if snapshot.checkpoint == nil || checkpointID != "" && snapshot.checkpoint.ID != checkpointID {
		return fmt.Errorf("missing or changed durable checkpoint at selected trace cut")
	}
	phase := map[string]string{"checkpoint": "pending", "acknowledge": "pending", "reply": "replied", "duplicate_reply": "replied", "ordinary_end": "replied"}[action]
	if phase != "" && snapshot.checkpoint.State != phase {
		return fmt.Errorf("unexpected durable phase %s after %s", snapshot.checkpoint.State, action)
	}
	if action == "initial" {
		if snapshot.policy.Generation != 0 || snapshot.policy.Enabled {
			return fmt.Errorf("initial policy already changed")
		}
	} else if snapshot.policy.Generation != 1 || !snapshot.policy.Enabled {
		return fmt.Errorf("unexpected extra policy action in selected trace scenario")
	}
	return nil
}

// Set timing due and execute the same component scheduler as reviewTick while
// holding the writer mutex through the resulting durable snapshot. An already
// committed early checkpoint is refused before this shortcut changes anything.
func checkpointLeaseReviewTrace(m *Manager, id string) (leaseReviewTraceSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	before, err := readLeaseReviewTraceSnapshotLocked(m, id)
	if err != nil {
		return before, err
	}
	if err = checkLeaseReviewTraceCut("configure", before, ""); err != nil {
		return before, err
	}
	p := before.policy
	p.Next = time.Now().Add(-time.Second)
	p.Updated = p.Next.Add(-p.Interval)
	raw, err := json.Marshal(p)
	if err != nil {
		return before, err
	}
	result, err := m.db.Exec(`UPDATE lease_review_policies SET data=? WHERE lease=? AND generation=? AND enabled=1`, string(raw), id, p.Generation)
	if err != nil {
		return before, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return before, fmt.Errorf("selected due policy changed unexpectedly: %v", err)
	}
	leases, err := m.list()
	if err == nil {
		err = m.tickLeaseReviews(time.Now().UTC(), leases)
	}
	if err != nil {
		return before, err
	}
	return readLeaseReviewTraceSnapshotLocked(m, id)
}

func TestLeaseReviewDevTraceConversationIsInert(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "traced-review")
	l := reviewLease(t, m, c, "traced-review")
	rows := []leaseReviewTraceRow{}
	checkpointID := ""
	appendSnapshot := func(action string, snapshot leaseReviewTraceSnapshot) {
		t.Helper()
		if err := checkLeaseReviewTraceCut(action, snapshot, checkpointID); err != nil {
			t.Fatal(err)
		}
		row := snapshot.observation(action)
		if len(rows) > 0 && action != "ordinary_end" {
			prev := rows[len(rows)-1]
			if row.Live != prev.Live || row.Revision != prev.Revision || row.Expiry != prev.Expiry {
				t.Fatal("conversation changed entitlement")
			}
		}
		rows = append(rows, row)
	}
	capture := func(action string) {
		t.Helper()
		snapshot, err := readLeaseReviewTraceSnapshot(m, l.ID)
		if err != nil {
			t.Fatal(err)
		}
		appendSnapshot(action, snapshot)
	}
	capture("initial")
	generation := int64(0)
	if _, err := m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: &generation, Lease: l.ID, Revision: l.Revision, Enabled: true, Interval: time.Minute, ContentionInterval: 15 * time.Second, ReplyTimeout: time.Minute}); err != nil {
		t.Fatal(err)
	}
	// Configure is captured before the separate test-only timing shortcut.
	capture("configure")
	snapshot, err := checkpointLeaseReviewTrace(m, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	appendSnapshot("checkpoint", snapshot)
	x := *snapshot.checkpoint
	checkpointID = x.ID
	q := receiptRequest(c, x.Message)
	q.Acknowledge = true
	if _, err = m.AgentReceipt(q); err != nil {
		t.Fatal(err)
	}
	capture("acknowledge")
	if _, err = m.ReplyLeaseReview(answer(c, x)); err != nil {
		t.Fatal(err)
	}
	capture("reply")
	if _, err = m.ReplyLeaseReview(answer(c, x)); err != nil {
		t.Fatal(err)
	}
	capture("duplicate_reply")
	if _, err = m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "release"}); err != nil {
		t.Fatal(err)
	}
	capture("ordinary_end")
	if _, err = m.ReplyLeaseReview(answer(c, x)); err != nil {
		t.Fatal(err)
	}
	capture("duplicate_reply")
	raw, err := json.Marshal(struct {
		Schema string                `json:"schema"`
		Rows   []leaseReviewTraceRow `json:"rows"`
	}{"arm.lease-review-conversation/v2", rows})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("arm-lease-review-trace " + string(raw))
}
