//go:build arm_devtrace

package manager

import (
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

// This control has a separate selected test/log from the sole exported actual
// eight-row scenario. It emits no arm-lease-review-trace line.
func TestLeaseReviewDevTraceSnapshotControls(t *testing.T) {
	t.Run("early-actual-scheduler-commit", func(t *testing.T) {
		m := newManager(t)
		c := pollContact(t, m, "early-trace-checkpoint")
		l := reviewLease(t, m, c, "early-trace-checkpoint")
		// Reproduce the original order: configure AND make due, then let the actual
		// ordinary backend tick commit before any local checkpoint ID is obtained.
		dueReview(t, m, l)
		done := make(chan struct{})
		go func() { m.tick(); close(done) }()
		select {
		case <-done:
		case <-t.Context().Done():
			t.Fatal("actual scheduler did not finish")
		}
		snapshot, err := readLeaseReviewTraceSnapshot(m, l.ID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.checkpoint == nil || snapshot.observation("configure").Phase != "pending" {
			t.Fatal("durable early checkpoint was hidden", snapshot)
		}
		if err = checkLeaseReviewTraceCut("configure", snapshot, ""); err == nil {
			t.Fatal("early checkpoint accepted as an invented none configure cut")
		}
		if _, err = checkpointLeaseReviewTrace(m, l.ID); err == nil {
			t.Fatal("explicit checkpoint shortcut ignored an earlier durable checkpoint")
		}
		after, err := readLeaseReviewTraceSnapshot(m, l.ID)
		if err != nil || after.checkpoint.ID != snapshot.checkpoint.ID || after.checkpoint.Sequence != snapshot.checkpoint.Sequence || !after.policy.Next.Equal(snapshot.policy.Next) {
			t.Fatal("refusal changed existing evidence", after, err)
		}
	})
	t.Run("additional-real-checkpoint-history", func(t *testing.T) {
		m := newManager(t)
		c := pollContact(t, m, "extra-trace-checkpoint")
		l := reviewLease(t, m, c, "extra-trace-checkpoint")
		dueReview(t, m, l)
		reviewTick(t, m, time.Now())
		first := reviews(t, m, model.LeaseReviewQuery{Lease: l.ID}).Items[0]
		if _, err := m.ReplyLeaseReview(answer(c, first)); err != nil {
			t.Fatal(err)
		}
		dueReview(t, m, l)
		reviewTick(t, m, time.Now())
		if page := reviews(t, m, model.LeaseReviewQuery{Lease: l.ID}); len(page.Items) != 2 {
			t.Fatal("fixture did not create two real durable checkpoints", page)
		}
		if _, err := readLeaseReviewTraceSnapshot(m, l.ID); err == nil {
			t.Fatal("additional durable checkpoint was projected away")
		}
	})
	changes := []struct{ name, sql string }{
		{"crossed-state-column", `UPDATE lease_reviews SET state='superseded'`},
		{"crossed-context-lease", `UPDATE lease_reviews SET data=json_set(data,'$.leases[0].lease','different-lease')`},
		{"pending-terminal-evidence", `UPDATE lease_reviews SET data=json_set(data,'$.closed_at','2026-01-01T00:00:00Z')`},
		{"unknown-state", `UPDATE lease_reviews SET state='unknown',data=json_set(data,'$.state','unknown')`},
		{"unknown-record-field", `UPDATE lease_reviews SET data=json_set(data,'$.unobserved',1)`},
		{"malformed-record", `UPDATE lease_reviews SET data='{'`},
	}

	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			m := newManager(t)
			c := pollContact(t, m, "shape-trace-checkpoint")
			l := reviewLease(t, m, c, "shape-trace-checkpoint")
			dueReview(t, m, l)
			reviewTick(t, m, time.Now())
			if snapshot, err := readLeaseReviewTraceSnapshot(m, l.ID); err != nil || snapshot.checkpoint == nil || snapshot.observation("checkpoint").Phase != "pending" {
				t.Fatal("valid durable control refused", snapshot, err)
			}
			m.mu.Lock()
			_, err := m.db.Exec(change.sql)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = readLeaseReviewTraceSnapshot(m, l.ID); err == nil {
				t.Fatal("malformed/crossed snapshot accepted as an observed projection")
			}
		})
	}
}
