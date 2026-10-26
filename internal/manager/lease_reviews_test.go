package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/database"
	"github.com/inferal-oss/arm/internal/model"
)

func reviewLease(t *testing.T, m *Manager, c agents.Contact, key string) model.Lease {
	t.Helper()
	q := request(key)
	q.Agent = c.ID
	q.Resources.CPU = .01
	q.TTL = time.Hour
	l, err := m.Acquire(q, nil)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func dueReview(t *testing.T, m *Manager, l model.Lease) model.LeaseReviewPolicy {
	t.Helper()
	oldPolicy, err := m.LeaseReviewPolicy(l.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: reviewGen(oldPolicy.Generation), Lease: l.ID, Revision: l.Revision, Enabled: true, Interval: time.Minute, ContentionInterval: 15 * time.Second, ReplyTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	p.Next = time.Now().Add(-time.Second)
	p.Updated = p.Next.Add(-p.Interval)
	raw, _ := json.Marshal(p)
	m.mu.Lock()
	_, err = m.db.Exec(`UPDATE lease_review_policies SET data=? WHERE lease=?`, string(raw), l.ID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func reviewTick(t *testing.T, m *Manager, now time.Time) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, err := m.list()
	if err == nil {
		err = m.tickLeaseReviews(now.UTC(), ls)
	}
	if err != nil {
		t.Fatal(err)
	}
}
func reviews(t *testing.T, m *Manager, q model.LeaseReviewQuery) model.LeaseReviewPage {
	t.Helper()
	p, err := m.LeaseReviews(q)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func answer(c agents.Contact, x model.LeaseReview) model.LeaseReviewReply {
	return model.LeaseReviewReply{ID: x.ID, Agent: c.ID, Session: c.Session, Registration: c.Registration, Intent: "needed", Explanation: "Still compiling; linking remains. $(not executable)"}
}

func TestLeaseReviewOptInCoalescesExactPollingAndInertReply(t *testing.T) {
	m := newManager(t)
	a := pollContact(t, m, "review-a")
	b := pollContact(t, m, "review-b")
	l1 := reviewLease(t, m, a, "review-first")
	l2 := reviewLease(t, m, a, "review-second")
	reviewTick(t, m, time.Now())
	if len(reviews(t, m, model.LeaseReviewQuery{}).Items) != 0 {
		t.Fatal("reviews enabled implicitly")
	}
	dueReview(t, m, l1)
	dueReview(t, m, l2)
	reviewTick(t, m, time.Now())
	page := reviews(t, m, model.LeaseReviewQuery{Pending: true})
	if len(page.Items) != 1 || len(page.Items[0].Context) != 2 {
		t.Fatalf("not coalesced: %+v", page)
	}
	x := page.Items[0]
	if x.Agent != a.ID || x.Registration != a.Registration || x.Session != a.Session {
		t.Fatal("wrong session")
	}
	for _, v := range x.Context {
		if v.Purpose == "" || v.Revision <= 0 || v.Remaining <= 0 || v.Usage.CPUQuality != "unavailable" {
			t.Fatalf("bad context: %+v", v)
		}
	}
	inbox, err := m.AgentInbox(context.Background(), inboxRequest(a))
	if err != nil || len(inbox.Items) != 1 || inbox.Items[0].Message.ID != x.Message || inbox.Items[0].Message.Deliveries[a.ID].Mode != "poll" {
		t.Fatalf("poll: %+v %v", inbox, err)
	}
	if wrong, err := m.AgentInbox(context.Background(), inboxRequest(b)); err != nil || len(wrong.Items) != 0 {
		t.Fatal("cross-recipient delivery", err)
	}
	bad := answer(b, x)
	if _, err = m.ReplyLeaseReview(bad); err == nil {
		t.Fatal("other recipient replied")
	}
	ack := receiptRequest(a, x.Message)
	ack.Acknowledge = true
	ack.Text = "acknowledged"
	if _, err = m.AgentReceipt(ack); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.InspectLeaseReview(x.ID); got.State != "pending" {
		t.Fatal("ack became review response")
	}
	q := answer(a, x)
	for i := 0; i < 2; i++ {
		got, err := m.ReplyLeaseReview(q)
		if err != nil || got.State != "replied" {
			t.Fatal(got, err)
		}
	}
	q.Explanation = "different"
	if _, err = m.ReplyLeaseReview(q); err == nil {
		t.Fatal("conflicting duplicate reply accepted")
	}
	for _, l := range []model.Lease{l1, l2} {
		got, err := m.Inspect(l.ID)
		if err != nil || got.Revision != l.Revision || !got.Expires.Equal(*l.Expires) || got.State != l.State {
			t.Fatal("review altered entitlement", got, err)
		}
	}
}
func TestLeaseReviewConcurrentTickAndReplyDeduplicate(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "concurrent-review")
	l := reviewLease(t, m, c, "concurrent-review")
	dueReview(t, m, l)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Go(func() { reviewTick(t, m, time.Now()) })
	}
	wg.Wait()
	p := reviews(t, m, model.LeaseReviewQuery{})
	if len(p.Items) != 1 {
		t.Fatal("duplicate checkpoints", len(p.Items))
	}
	q := answer(c, p.Items[0])
	for i := 0; i < 16; i++ {
		wg.Go(func() {
			if _, err := m.ReplyLeaseReview(q); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	var n int
	m.mu.Lock()
	err := m.db.QueryRow(`SELECT count(*) FROM events WHERE type='lease_review.replied'`).Scan(&n)
	m.mu.Unlock()
	if err != nil || n != 1 {
		t.Fatal("reply duplicated", n, err)
	}
}
func TestLeaseReviewLateRevisionExpiryAndClockRollbackAreInert(t *testing.T) {
	for _, scenario := range []string{"deadline", "revision", "release", "disable", "clock"} {
		t.Run(scenario, func(t *testing.T) {
			m := newManager(t)
			c := pollContact(t, m, "late-review")
			l := reviewLease(t, m, c, "late-review")
			dueReview(t, m, l)
			reviewTick(t, m, time.Now())
			x := reviews(t, m, model.LeaseReviewQuery{}).Items[0]
			switch scenario {
			case "deadline":
				reviewTick(t, m, x.Deadline)
			case "revision":
				if _, err := m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "renew", TTL: 2 * time.Hour}); err != nil {
					t.Fatal(err)
				}
			case "release":
				if _, err := m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "release"}); err != nil {
					t.Fatal(err)
				}
			case "disable":
				if _, err := m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: reviewGen(x.Context[0].PolicyGeneration), Lease: l.ID, Revision: l.Revision}); err != nil {
					t.Fatal(err)
				}
			case "clock":
				reviewTick(t, m, x.Created.Add(-time.Second))
			}
			before, _ := m.Inspect(l.ID)
			if _, err := m.ReplyLeaseReview(answer(c, x)); err == nil {
				t.Fatal("stale answer accepted")
			}
			after, _ := m.Inspect(l.ID)
			if before.Revision != after.Revision || before.State != after.State || !before.Expires.Equal(*after.Expires) {
				t.Fatal("late response changed lease")
			}
		})
	}
}
func TestLeaseReviewRestartKeepsPendingAndSessionFence(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	c := pollContact(t, m, "restart-review")
	l := reviewLease(t, m, c, "restart-review")
	dueReview(t, m, l)
	reviewTick(t, m, time.Now())
	x := reviews(t, m, model.LeaseReviewQuery{}).Items[0]
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	reviewTick(t, m, time.Now())
	p := reviews(t, m, model.LeaseReviewQuery{Pending: true})
	if len(p.Items) != 1 || p.Items[0].ID != x.ID {
		t.Fatal("restart duplicated/lost review", p)
	}
	q := answer(c, x)
	q.Registration = "stale-registration"
	if _, err = m.ReplyLeaseReview(q); err == nil {
		t.Fatal("changed registration accepted")
	}
	if _, err = m.ReplyLeaseReview(answer(c, x)); err != nil {
		t.Fatal(err)
	}
}
func TestLeaseReviewBatchFairnessAndContention(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "batch-review")
	for i := 0; i < 20; i++ {
		dueReview(t, m, reviewLease(t, m, c, fmt.Sprint("batch-", i)))
	}
	reviewTick(t, m, time.Now())
	p := reviews(t, m, model.LeaseReviewQuery{Pending: true})
	if len(p.Items) != 1 || len(p.Items[0].Context) != 16 {
		t.Fatal("batch bound", p)
	}
	seen := map[string]bool{}
	for _, v := range p.Items[0].Context {
		seen[v.Lease] = true
	}
	if _, err := m.ReplyLeaseReview(answer(c, p.Items[0])); err != nil {
		t.Fatal(err)
	}
	reviewTick(t, m, time.Now())
	p = reviews(t, m, model.LeaseReviewQuery{Pending: true})
	if len(p.Items) != 1 || len(p.Items[0].Context) != 4 {
		t.Fatal("later members starved", p)
	}
	for _, v := range p.Items[0].Context {
		if seen[v.Lease] {
			t.Fatal("earlier members repeated before overdue members")
		}
	}
}

func TestLeaseReviewGlobalBoundPagingAndLaterContactFairness(t *testing.T) {
	m := newManager(t)
	contacts := map[string]agents.Contact{}
	for i := 0; i < 65; i++ {
		c := pollContact(t, m, fmt.Sprint("review-contact-", i))
		contacts[c.ID] = c
		dueReview(t, m, reviewLease(t, m, c, fmt.Sprint("bound-", i)))
	}
	for i := 0; i < 10; i++ {
		reviewTick(t, m, time.Now())
	}
	p := reviews(t, m, model.LeaseReviewQuery{Pending: true})
	if len(p.Items) != 64 || p.More {
		t.Fatal("global pending bound", len(p.Items), p.More)
	}
	old := p.Items[0]
	if _, err := m.ReplyLeaseReview(answer(contacts[old.Agent], old)); err != nil {
		t.Fatal(err)
	}
	reviewTick(t, m, time.Now())
	all := reviews(t, m, model.LeaseReviewQuery{})
	if len(all.Items) != 64 || !all.More {
		t.Fatal("first page", len(all.Items), all.More)
	}
	next := reviews(t, m, model.LeaseReviewQuery{After: all.Cursor})
	if len(next.Items) != 1 || next.More || next.Items[0].Agent == old.Agent {
		t.Fatal("later contact starved or pagination repeated", next)
	}
}
func TestLeaseReviewContentionAndDeniedRenewalKeepEntitlement(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "contended-review")
	l := reviewLease(t, m, c, "contended-review")
	p, err := m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: reviewGen(0), Lease: l.ID, Revision: l.Revision, Enabled: true, Interval: time.Hour, ContentionInterval: 15 * time.Second, ReplyTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	q := request("queued-behind-review")
	q.Queue = true
	if _, err = m.Acquire(q, nil); err != nil {
		t.Fatal(err)
	}
	reviewTick(t, m, p.Updated.Add(16*time.Second))
	page := reviews(t, m, model.LeaseReviewQuery{Pending: true})
	if len(page.Items) != 1 || page.Items[0].Context[0].QueuedDemand != 1 {
		t.Fatal("contention not captured", page)
	}
	if _, err = m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "renew", TTL: 2 * time.Hour}); err == nil {
		t.Fatal("queued demand did not deny renewal")
	}
	after, _ := m.Inspect(l.ID)
	if after.Revision != l.Revision || !after.Expires.Equal(*l.Expires) {
		t.Fatal("denied renewal altered entitlement")
	}
}
func TestLeaseReviewSilenceThroughActualExpiry(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "expiry-review")
	q := request("expiry-review")
	q.Agent = c.ID
	q.TTL = time.Second
	l, err := m.Acquire(q, nil)
	if err != nil {
		t.Fatal(err)
	}
	dueReview(t, m, l)
	reviewTick(t, m, time.Now())
	x := reviews(t, m, model.LeaseReviewQuery{}).Items[0]
	if x.Deadline.After(*l.Expires) {
		t.Fatal("review outlives entitlement")
	}
	eventually(t, func() bool { m.tick(); a, _ := m.Inspect(l.ID); return a.State == "terminal" })
	after, _ := m.Inspect(l.ID)
	if after.Outcome != "expired" || !after.Expires.Equal(*l.Expires) {
		t.Fatal("silence changed expiry", after)
	}
	if _, err = m.ReplyLeaseReview(answer(c, x)); err == nil {
		t.Fatal("late reply revived expired lease")
	}
}

func reviewGen(v int64) *int64 { return &v }

func TestLeaseReviewPolicyGenerationConcurrentAndABA(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "policy-generation")
	l := reviewLease(t, m, c, "policy-generation")
	p := dueReview(t, m, l)
	reviewTick(t, m, time.Now())
	checkpoint := reviews(t, m, model.LeaseReviewQuery{}).Items[0]
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, interval := range []time.Duration{2 * time.Minute, 3 * time.Minute} {
		wg.Go(func() {
			_, err := m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: reviewGen(p.Generation), Lease: l.ID, Revision: l.Revision, Enabled: true, Interval: interval})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if e, ok := err.(*model.Error); ok && e.Code == "policy_conflict" {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent schedule edits both committed", success, conflict)
	}
	current, err := m.LeaseReviewPolicy(l.ID)
	if err != nil || current.Generation != p.Generation+1 {
		t.Fatal(current, err)
	}
	disabled, err := m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: reviewGen(current.Generation), Lease: l.ID, Revision: l.Revision})
	if err != nil || disabled.Enabled || disabled.Generation != current.Generation+1 {
		t.Fatal(disabled, err)
	}
	stale := model.LeaseReviewConfigure{Generation: reviewGen(0), Lease: l.ID, Revision: l.Revision, Enabled: true, Interval: time.Minute}
	if _, err = m.ConfigureLeaseReview(stale); err == nil {
		t.Fatal("absent-policy ABA reenabled disabled policy")
	}
	stale.Generation = reviewGen(p.Generation)
	if _, err = m.ConfigureLeaseReview(stale); err == nil {
		t.Fatal("old enabled generation reenabled disabled policy")
	}
	stale.Generation = reviewGen(disabled.Generation)
	enabled, err := m.ConfigureLeaseReview(stale)
	if err != nil || enabled.Generation != disabled.Generation+1 {
		t.Fatal(enabled, err)
	}
	if _, err = m.ReplyLeaseReview(answer(c, checkpoint)); err == nil {
		t.Fatal("old checkpoint survived policy disable-reenable ABA")
	}
}
func TestLeaseReviewPolicyTombstoneRestartAndExhaustion(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := pollContact(t, m, "policy-restart")
	l := reviewLease(t, m, c, "policy-restart")
	p := dueReview(t, m, l)
	off, err := m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: reviewGen(p.Generation), Lease: l.ID, Revision: l.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	read, err := m.LeaseReviewPolicy(l.ID)
	if err != nil || read.Generation != off.Generation || read.Enabled {
		t.Fatal("disabled generation lost on restart", read, err)
	}
	if _, err = m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: reviewGen(0), Lease: l.ID, Revision: l.Revision, Enabled: true}); err == nil {
		t.Fatal("restart lost generation fence")
	}
	read.Generation = int64(^uint64(0) >> 1)
	raw, _ := json.Marshal(read)
	m.mu.Lock()
	_, err = m.db.Exec(`UPDATE lease_review_policies SET generation=?,data=? WHERE lease=?`, read.Generation, string(raw), l.ID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: reviewGen(read.Generation), Lease: l.ID, Revision: l.Revision, Enabled: true}); err == nil {
		t.Fatal("exhausted generation wrapped")
	}
	after, _ := m.LeaseReviewPolicy(l.ID)
	if after.Generation != read.Generation || after.Enabled {
		t.Fatal("overflow changed policy")
	}
}
func TestLeaseReviewFinishedPolicyRetainsGeneration(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "finished-policy")
	l := reviewLease(t, m, c, "finished-policy")
	p := dueReview(t, m, l)
	ended, err := m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "release"})
	if err != nil {
		t.Fatal(err)
	}
	reviewTick(t, m, time.Now())
	after, err := m.LeaseReviewPolicy(l.ID)
	if err != nil || after.Enabled || after.Generation != p.Generation+1 || after.Revision != ended.Revision {
		t.Fatal("finished policy lost tombstone", after, err)
	}
	if _, err = m.ConfigureLeaseReview(model.LeaseReviewConfigure{Generation: reviewGen(p.Generation), Lease: l.ID, Revision: ended.Revision}); err == nil {
		t.Fatal("old configuration overwrote finished tombstone")
	}
}

func TestLeaseReviewInvalidObservationRefusesCheckpointWithoutBlockingLease(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "invalid-review-usage")
	l := reviewLease(t, m, c, "invalid-review-usage")
	dueReview(t, m, l)
	bad := math.NaN()
	m.mu.Lock()
	m.usages[l.ID] = workloadSample{observed: time.Now(), view: model.WorkloadUsage{Observed: time.Now(), CPUCores: &bad, CPUQuality: "invalid fixture"}}
	m.mu.Unlock()
	m.tick()
	page := reviews(t, m, model.LeaseReviewQuery{})
	if len(page.Items) != 0 || page.SchedulerError == "" {
		t.Fatal("invalid sample became a checkpoint or error was hidden", page)
	}
	after, _ := m.Inspect(l.ID)
	if after.Revision != l.Revision || !after.Expires.Equal(*l.Expires) {
		t.Fatal("review failure changed entitlement")
	}
}

func TestLeaseReviewRunsAfterExpiryAndQueuedAdmission(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "lifecycle-first")
	q := request("expires-before-review")
	q.Resources.CPU = .9
	q.TTL = 500 * time.Millisecond
	expiring, err := m.Acquire(q, nil)
	if err != nil {
		t.Fatal(err)
	}
	kept := reviewLease(t, m, c, "review-after-lifecycle")
	dueReview(t, m, kept)
	wait := request("admitted-before-review")
	wait.Resources.CPU = .9
	wait.Queue = true
	queued, err := m.Acquire(wait, nil)
	if err != nil || queued.State != "queued" {
		t.Fatal(queued, err)
	}
	time.Sleep(max(0, time.Until(*expiring.Expires)) + 5*time.Millisecond)
	m.tick()
	after, err := m.Inspect(queued.ID)
	if err != nil || after.State != "active" {
		t.Fatal("queue did not admit after expiry", after, err)
	}
	page := reviews(t, m, model.LeaseReviewQuery{Pending: true})
	if len(page.Items) != 1 || page.Items[0].Context[0].QueuedDemand != 0 {
		t.Fatal("review used pre-expiry/pre-admission demand", page)
	}
	var expiredSeq, reviewSeq int64
	m.mu.Lock()
	e1 := m.db.QueryRow(`SELECT sequence FROM events WHERE type='lease.expired' AND entity=?`, expiring.ID).Scan(&expiredSeq)
	e2 := m.db.QueryRow(`SELECT sequence FROM events WHERE type='lease_review.created' AND entity=?`, page.Items[0].ID).Scan(&reviewSeq)
	m.mu.Unlock()
	if e1 != nil || e2 != nil || expiredSeq >= reviewSeq {
		t.Fatal("optional work preceded expiry", expiredSeq, reviewSeq, e1, e2)
	}
}
func TestLeaseReviewSchedulingErrorDoesNotPreventActualExpiry(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "expiry-despite-review-error")
	bad := reviewLease(t, m, c, "invalid-policy")
	dueReview(t, m, bad)
	m.mu.Lock()
	_, err := m.db.Exec(`UPDATE lease_review_policies SET data='{' WHERE lease=?`, bad.ID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	q := request("expire-despite-policy")
	q.Resources.CPU = .01
	q.TTL = 50 * time.Millisecond
	l, err := m.Acquire(q, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(max(0, time.Until(*l.Expires)) + 5*time.Millisecond)
	m.tick()
	after, err := m.Inspect(l.ID)
	if err != nil || after.Outcome != "expired" {
		t.Fatal("optional review failure prevented expiry", after, err)
	}
	p := reviews(t, m, model.LeaseReviewQuery{})
	if p.SchedulerError == "" {
		t.Fatal("optional scheduler error hidden")
	}
}
func TestLeaseReviewWholeLedgerClassificationPreservesResourceAuthority(t *testing.T) {
	for _, replied := range []bool{false, true} {
		t.Run(fmt.Sprint("replied-", replied), func(t *testing.T) {
			m, stop := reviewProofManager(t)
			c := pollContact(t, m, "proof-review")
			l := reviewLease(t, m, c, "proof-review")
			dueReview(t, m, l)
			reviewTick(t, m, time.Now())
			x := reviews(t, m, model.LeaseReviewQuery{}).Items[0]
			if replied {
				if _, err := m.ReplyLeaseReview(answer(c, x)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.LeaseAction(model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: "release"}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(m.home, "daemon.lock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := stop(); err != nil {
				t.Fatal(err)
			}
			s, err := database.Offline(context.Background(), m.home)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			proof, err := database.ProveDatabaseSettled(context.Background(), s, m.owner)
			if err != nil || !proof.Complete || !proof.Settled {
				t.Fatalf("conversation broke settled proof: %+v %v", proof, err)
			}
		})
	}
}
func TestLeaseReviewLedgerRefusesAlteredSchemaAndEvidence(t *testing.T) {
	changes := map[string]string{
		"unknown-schema":       `UPDATE metadata SET value='99' WHERE key='lease_review_schema'`,
		"missing-marker":       `DELETE FROM metadata WHERE key='lease_review_schema'`,
		"partial-schema":       `DROP INDEX lease_reviews_pending`,
		"unknown-field":        `UPDATE lease_reviews SET data=json_set(data,'$.unknown',1)`,
		"policy-generation":    `UPDATE lease_review_policies SET generation=generation+1`,
		"context-generation":   `UPDATE lease_reviews SET data=json_set(data,'$.leases[0].policy_generation',9999)`,
		"context-claims":       `UPDATE lease_reviews SET data=json_set(data,'$.leases[0].resources.cpu',9999)`,
		"recipient-generation": `UPDATE lease_reviews SET registration='changed'`,
		"delivery-native":      `UPDATE messages SET data=json_set(data,'$.deliveries.proof-review.mode','native') WHERE json_extract(data,'$.sender')='arm-lease-review'`,
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			m, stop := reviewProofManager(t)
			c := pollContact(t, m, "proof-review")
			l := reviewLease(t, m, c, "proof-review")
			dueReview(t, m, l)
			reviewTick(t, m, time.Now())
			if _, err := m.End(l.ID, "released"); err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			_, err := m.db.Exec(change)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(m.home, "daemon.lock"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err = stop(); err != nil {
				t.Fatal(err)
			}
			s, err := database.Offline(context.Background(), m.home)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			proof, err := database.ProveDatabaseSettled(context.Background(), s, m.owner)
			if err == nil || proof.Complete || proof.Settled {
				t.Fatal("altered ledger accepted", proof, err)
			}
		})
	}
}

func reviewProofManager(t *testing.T) (*Manager, func() error) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "arm")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	var closeErr error
	stop := func() error { once.Do(func() { closeErr = m.Close() }); return closeErr }
	t.Cleanup(func() {
		if err := stop(); err != nil {
			t.Error(err)
		}
	})
	return m, stop
}

func TestLeaseReviewReleaseIntentCannotProveLeaseSettled(t *testing.T) {
	m, stop := reviewProofManager(t)
	c := pollContact(t, m, "release-intent-only")
	l := reviewLease(t, m, c, "release-intent-only")
	dueReview(t, m, l)
	reviewTick(t, m, time.Now())
	x := reviews(t, m, model.LeaseReviewQuery{}).Items[0]
	q := answer(c, x)
	q.Intent = "release"
	if _, err := m.ReplyLeaseReview(q); err != nil {
		t.Fatal(err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	s, err := database.Offline(context.Background(), m.home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	proof, err := database.ProveDatabaseSettled(context.Background(), s, m.owner)
	if err != nil || !proof.Complete || proof.Settled {
		t.Fatal("conversational release became settlement authority", proof, err)
	}
	blocked := false
	for _, b := range proof.Blockers {
		if b.Kind == "lease" && b.Entity == l.ID {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("live lease blocker missing", proof.Blockers)
	}
}
func TestLeaseReviewOptionalAbsentSchemaPreservesLegacyProof(t *testing.T) {
	m, stop := reviewProofManager(t)
	c := pollContact(t, m, "legacy-review-group")
	l := reviewLease(t, m, c, "legacy-review-group")
	if _, err := m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	for _, q := range []string{`DROP TABLE lease_reviews`, `DROP TABLE lease_review_policies`, `DELETE FROM metadata WHERE key='lease_review_schema'`} {
		if _, err := m.db.Exec(q); err != nil {
			m.mu.Unlock()
			t.Fatal(err)
		}
	}
	m.mu.Unlock()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	s, err := database.Offline(context.Background(), m.home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	proof, err := database.ProveDatabaseSettled(context.Background(), s, m.owner)
	if err != nil || !proof.Settled {
		t.Fatal("absent optional group broke legacy proof", proof, err)
	}
}

func TestLeaseReviewLedgerTerminalClosureTimeline(t *testing.T) {
	for _, tc := range []struct {
		name, state, detail string
		at                  func(model.LeaseReview) time.Time
		valid               bool
	}{
		{"timeout-at-creation-plus-one-nanosecond", "timed_out", "reply deadline passed; no response changes lease expiry", func(x model.LeaseReview) time.Time { return x.Created.Add(time.Nanosecond) }, false},
		{"timeout-at-deadline", "timed_out", "reply deadline passed; no response changes lease expiry", func(x model.LeaseReview) time.Time { return x.Deadline }, true},
		{"timeout-after-deadline", "timed_out", "reply deadline passed; no response changes lease expiry", func(x model.LeaseReview) time.Time { return x.Deadline.Add(time.Nanosecond) }, true},
		{"clock-rollback-before-creation", "superseded", "clock moved before the captured review time", func(x model.LeaseReview) time.Time { return x.Created.Add(-time.Nanosecond) }, true},
		{"clock-detail-at-creation", "superseded", "clock moved before the captured review time", func(x model.LeaseReview) time.Time { return x.Created }, false},
		{"nonclock-superseded-before-creation", "superseded", "registered recipient changed", func(x model.LeaseReview) time.Time { return x.Created.Add(-time.Nanosecond) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, stop := reviewProofManager(t)
			c := pollContact(t, m, "timeline-review")
			l := reviewLease(t, m, c, "timeline-review")
			dueReview(t, m, l)
			reviewTick(t, m, time.Now())
			x := reviews(t, m, model.LeaseReviewQuery{}).Items[0]
			if _, err := m.End(l.ID, "released"); err != nil {
				t.Fatal(err)
			}
			at := tc.at(x)
			x.State = tc.state
			x.Detail = tc.detail
			x.Closed = &at
			raw, err := json.Marshal(x)
			if err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			_, err = m.db.Exec(`UPDATE lease_reviews SET state=?,data=? WHERE id=?`, x.State, string(raw), x.ID)
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if err = stop(); err != nil {
				t.Fatal(err)
			}
			session, err := database.Offline(context.Background(), m.home)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			proof, err := database.ProveDatabaseSettled(context.Background(), session, m.owner)
			if tc.valid {
				if err != nil || !proof.Complete || !proof.Settled {
					t.Fatal("valid terminal timeline refused", proof, err)
				}
			} else if err == nil || proof.Complete || proof.Settled {
				t.Fatal("invalid terminal timeline accepted", proof, err)
			}
		})
	}
}
