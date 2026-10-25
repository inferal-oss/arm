package nodestop

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestElapsedReviewIncludesSuspendAndDoesNotFollowWallRollback(t *testing.T) {
	tick := int64(10)
	r, err := newElapsedReview(func() (int64, error) { return tick, nil })
	if err != nil {
		t.Fatal(err)
	}
	tick += int64(ReviewTTL) - 1
	if err = r.Check(); err != nil {
		t.Fatal(err)
	}
	// A suspend-inclusive jump expires the permit even if the wall is frozen.
	tick++
	if err = r.Check(); err == nil {
		t.Fatal("elapsed deadline accepted")
	}
	tick = 10
	if err = r.Check(); err == nil {
		t.Fatal("expired review revived after rollback")
	}
}
func TestElapsedReviewFailureAndRegressionAreSticky(t *testing.T) {
	for _, kind := range []string{"error", "regression"} {
		t.Run(kind, func(t *testing.T) {
			tick := int64(100)
			var readErr error
			r, err := newElapsedReview(func() (int64, error) { return tick, readErr })
			if err != nil {
				t.Fatal(err)
			}
			if kind == "error" {
				readErr = errors.New("unavailable")
			} else {
				tick--
			}
			if err = r.Check(); err == nil {
				t.Fatal("failed clock accepted")
			}
			readErr = nil
			tick = 101
			if err = r.Check(); err == nil {
				t.Fatal("failed clock silently recovered")
			}
		})
	}
}
func TestElapsedPermitExactDigestCannotRefreshOrReviveAfterPurge(t *testing.T) {
	tick := int64(100)
	wall := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := elapsedPermits{now: func() (int64, error) { return tick, nil }}
	if err := r.issue("original", wall); err != nil {
		t.Fatal(err)
	}
	tick += int64(ReviewTTL) - 1
	if err := r.issue("original", wall); err != nil {
		t.Fatal(err)
	}
	tick++
	if err := r.check("original"); err == nil {
		t.Fatal("same digest got refreshed")
	}
	if err := r.issue("original", wall); err == nil {
		t.Fatal("expired digest reissued")
	}
	if err := r.issue("next", wall.Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if len(r.issued) != 1 {
		t.Fatal("expired working set not purged")
	}
	if err := r.issue("original", wall); err == nil {
		t.Fatal("purged digest revived with frozen audit time")
	}
}
func TestElapsedPermitCapacityDoesNotEvictLiveReviews(t *testing.T) {
	tick := int64(100)
	wall := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	r := elapsedPermits{now: func() (int64, error) { return tick, nil }}
	for i := 0; i < MaxRecords; i++ {
		if err := r.issue(fmt.Sprint(i), wall.Add(time.Duration(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.issue("over", wall.Add(time.Second)); err == nil {
		t.Fatal("live review cap bypassed")
	}
	if err := r.check("0"); err != nil {
		t.Fatal("earlier live review evicted", err)
	}
	tick += int64(ReviewTTL)
	if err := r.issue("fresh", wall.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(r.issued) != 1 {
		t.Fatal(len(r.issued))
	}
}
func TestBoundaryRequiresActualPreparedDigestAndFreshGeneration(t *testing.T) {
	b, p, c, now := contractFixture(t)
	p.Policy.Wait = true
	if _, err := b.Admit(application(t, p), c, now); err == nil {
		t.Fatal("policy altered without fresh preparation")
	}
	other, err := NewBoundary(c.Target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.Admit(application(t, contractPlan(t, b, p, c, now)), c, now); err == nil {
		t.Fatal("review survived replacement boundary")
	}
}
func contractPlan(t *testing.T, b *Boundary, p Plan, c Current, now time.Time) Plan {
	t.Helper()
	p, err := b.Prepare(p.Key, p.Actor, p.DesiredRevision, p.Policy, p.Reason, p.Impact, now.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestNativeElapsedClockAndCheckedTimespecDomain(t *testing.T) {
	first, err := nativeReviewTick()
	if err != nil {
		t.Fatal(err)
	}
	next, err := nativeReviewTick()
	if err != nil || next < first {
		t.Fatal(first, next, err)
	}
	for _, pair := range [][2]int64{{-1, 0}, {0, -1}, {0, 1000000000}, {1<<63 - 1, 0}} {
		if _, err := clockTick(pair[0], pair[1]); err == nil {
			t.Fatal("invalid clock conversion", pair)
		}
	}
	if n, err := clockTick(1, 2); err != nil || n != 1000000002 {
		t.Fatal(n, err)
	}
}

func TestCommitAttemptRechecksRetainedAdmissionExpiry(t *testing.T) {
	b, p, c, now := contractFixture(t)
	admitted, err := b.Admit(application(t, p), c, now)
	if err != nil {
		t.Fatal(err)
	}
	tick := b.elapsed.last + int64(ReviewTTL)
	b.elapsed.now = func() (int64, error) { return tick, nil }
	if err = b.CheckAdmission(admitted); err == nil {
		t.Fatal("expired admission reached commit-attempt cut")
	}
	if err = b.CheckAdmission(Admission{}); err == nil {
		t.Fatal("empty admission gained authority")
	}
}
