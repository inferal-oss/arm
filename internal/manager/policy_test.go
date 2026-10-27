package manager

import (
	"encoding/json"
	"math"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/model"
)

func policyFixture(t *testing.T, mode, delivery string) (*Manager, model.Lease, agents.Contact) {
	t.Helper()
	return policyFixtureWithReason(t, mode, delivery, "owned policy workload")
}
func policyFixtureWithReason(t *testing.T, mode, delivery, reason string) (*Manager, model.Lease, agents.Contact) {
	t.Helper()
	m := newManager(t)
	c := agents.Contact{ID: "policy-agent", Adapter: "generic", Session: uuid.NewString(), CWD: t.TempDir(), Delivery: "poll"}
	if delivery == "native" {
		c.Adapter, c.Delivery = "codex", "native"
	}
	var err error
	c, err = m.RegisterContact(c)
	if err != nil {
		t.Fatal(err)
	}
	r := request("policy-owned-job")
	r.Reason = reason
	r.Resources.Memory = 1024
	r.Agent = c.ID
	r.UsagePolicy = &model.UsagePolicy{Mode: mode, Window: time.Second, Grace: time.Second, Warnings: 3}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	sleep, err = filepath.Abs(sleep)
	if err != nil {
		t.Fatal(err)
	}
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{sleep, "60"}, CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return m, l, c
}

func TestPolicyWarningBoundsAndQuotesUntrustedPurpose(t *testing.T) {
	m, l, c := policyFixtureWithReason(t, "warn", "poll", strings.Repeat("\x00", 4096))
	at := time.Now()
	policySample(t, m, l, policyEvidence(l, 2), at)
	policySample(t, m, l, policyEvidence(l, 2), at.Add(time.Second))
	page, err := m.AgentInbox(t.Context(), InboxRequest{ID: c.ID, Session: c.Session, Registration: c.Registration})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("warning unavailable", page, err)
	}
	body := page.Items[0].Message.Body
	if len(body) > 16384 || strings.ContainsRune(body, 0) || !strings.Contains(body, "caller-supplied data") || !strings.Contains(body, "full purpose in lease") {
		t.Fatal("untrusted purpose exceeded or escaped its bounded quotation")
	}
}

func policyEvidence(l model.Lease, cores float64) model.WorkloadUsage {
	return model.WorkloadUsage{Root: *l.Process, Live: true, IdentityVerified: true, Complete: true, CPUCores: &cores, CPUQuality: "lower-bound-overlap-average", Window: time.Second, Method: "test-owned group evidence"}
}
func policySample(t *testing.T, m *Manager, l model.Lease, view model.WorkloadUsage, at time.Time) {
	t.Helper()
	view.Observed = at.UTC()
	m.mu.Lock()
	err := m.evaluateUsage(l.ID, view, at)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}
func oneIncident(t *testing.T, m *Manager, id string) model.UsageIncident {
	t.Helper()
	all, err := m.Incidents(id)
	if err != nil || len(all) != 1 {
		t.Fatalf("incidents %+v: %v", all, err)
	}
	return all[0]
}

func TestPolicyWarnGraceAckRenewAndOwnedRevocation(t *testing.T) {
	m, l, c := policyFixture(t, "revoke", "poll")
	// An unrelated job and cooperative permit remain untouched by enforcement.
	r := request("unrelated")
	r.Resources = model.Resources{Disk: 1}
	unrelated, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	over := policyEvidence(l, 2)
	policySample(t, m, l, over, at)
	if i := oneIncident(t, m, l.ID); i.WarningCount != 0 || i.State != "observing" {
		t.Fatalf("instant warning: %+v", i)
	}
	for n := 1; n <= 3; n++ {
		policySample(t, m, l, over, at.Add(time.Duration(n)*time.Second))
		i := oneIncident(t, m, l.ID)
		if i.WarningCount != n || i.State != "warning" || len(i.Messages) != n {
			t.Fatalf("warning step %d: %+v", n, i)
		}
		page, err := m.AgentInbox(t.Context(), InboxRequest{ID: c.ID, Session: c.Session, Registration: c.Registration})
		if err != nil || len(page.Items) != n {
			t.Fatalf("atomic inbox: %+v %v", page, err)
		}
		msg := page.Items[n-1].Message
		if msg.Sender != "arm-policy" || msg.Deliveries[c.ID].State != "available" || !strings.Contains(msg.Body, "Remediation deadline") {
			t.Fatalf("warning attribution: %+v", msg)
		}
		if _, err = m.AgentReceipt(ReceiptRequest{ID: c.ID, Session: c.Session, Registration: c.Registration, Message: i.Messages[n-1], Acknowledge: true, Text: "I saw this"}); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			if _, err = m.Renew(l.ID, 2*time.Minute); err != nil {
				t.Fatal(err)
			}
		}
	}
	still, _ := m.Inspect(l.ID)
	if still.State != "active" {
		t.Fatal("revoked before final grace")
	}
	policySample(t, m, l, over, at.Add(4*time.Second))
	eventually(t, func() bool { x, _ := m.Inspect(l.ID); return x.State == "terminal" })
	m.tick()
	ended, _ := m.Inspect(l.ID)
	i := oneIncident(t, m, l.ID)
	if ended.Outcome != "revoked" || i.Outcome != "revoked" || i.State != "closed" {
		t.Fatalf("no settled revocation: %+v %+v", ended, i)
	}
	other, _ := m.Inspect(unrelated.ID)
	if other.State != "active" {
		t.Fatal("unrelated lease revoked")
	}
}

func TestPolicyUnknownStaleGapAndScopedRecovery(t *testing.T) {
	m, l, _ := policyFixture(t, "revoke", "poll")
	at := time.Now()
	over := policyEvidence(l, 2)
	policySample(t, m, l, over, at)
	policySample(t, m, l, over, at.Add(time.Second))
	unknown := over
	unknown.Stale = true
	policySample(t, m, l, unknown, at.Add(2*time.Second))
	i := oneIncident(t, m, l.ID)
	if i.WarningCount != 1 || i.BreachSince != nil {
		t.Fatalf("stale evidence continued streak: %+v", i)
	}
	policySample(t, m, l, over, at.Add(3*time.Second))
	if oneIncident(t, m, l.ID).WarningCount != 1 {
		t.Fatal("immediate escalation after observation gap")
	}
	policySample(t, m, l, over, at.Add(4*time.Second))
	if oneIncident(t, m, l.ID).WarningCount != 2 {
		t.Fatal("fresh sustained overage did not resume")
	}
	// Replayed and backwards observations cannot advance warnings.
	policySample(t, m, l, over, at.Add(4*time.Second))
	policySample(t, m, l, over, at.Add(3*time.Second))
	if oneIncident(t, m, l.ID).WarningCount != 2 {
		t.Fatal("replayed sample advanced policy")
	}
	policySample(t, m, l, over, at.Add(10*time.Second))
	if oneIncident(t, m, l.ID).WarningCount != 2 {
		t.Fatal("long collection gap advanced policy")
	}
	within := policyEvidence(l, .1)
	within.Complete = false
	policySample(t, m, l, within, at.Add(11*time.Second))
	if oneIncident(t, m, l.ID).State == "closed" {
		t.Fatal("partial small observation established compliance")
	}
	within.Complete = true
	policySample(t, m, l, within, at.Add(12*time.Second))
	i = oneIncident(t, m, l.ID)
	if i.Outcome != "observed_within_threshold" || i.State != "closed" {
		t.Fatalf("scoped observation did not close incident: %+v", i)
	}
	policySample(t, m, l, over, at.Add(13*time.Second))
	all, _ := m.Incidents(l.ID)
	if len(all) != 2 || all[0].ID == i.ID || all[0].WarningCount != 0 {
		t.Fatalf("repeat breach lost incident history: %+v", all)
	}
}

func TestPolicyWarningTransactionRollbackAndWarnOnlyBound(t *testing.T) {
	m, l, _ := policyFixture(t, "warn", "poll")
	at := time.Now()
	over := policyEvidence(l, 2)
	policySample(t, m, l, over, at)
	if _, err := m.db.Exec(`CREATE TRIGGER reject_policy_message BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT,'injected warning failure'); END`); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	err := m.evaluateUsage(l.ID, over, at.Add(time.Second))
	m.mu.Unlock()
	if err == nil {
		t.Fatal("injected notification failure ignored")
	}
	if i := oneIncident(t, m, l.ID); i.WarningCount != 0 || len(i.Messages) != 0 {
		t.Fatal("incident escaped warning rollback")
	}
	msgs, err := m.Messages()
	if err != nil || len(msgs) != 0 {
		t.Fatal("partial warning committed", msgs, err)
	}
	if _, err = m.db.Exec(`DROP TRIGGER reject_policy_message`); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 6; n++ {
		policySample(t, m, l, over, at.Add(time.Duration(n)*time.Second))
	}
	i := oneIncident(t, m, l.ID)
	current, _ := m.Inspect(l.ID)
	if i.WarningCount != 3 || current.State != "active" {
		t.Fatalf("warn-only policy exceeded bounds: %+v %+v", i, current)
	}
}

func TestPolicyRejectsForeignIdentityAndMissingCounters(t *testing.T) {
	m, l, _ := policyFixture(t, "revoke", "poll")
	at := time.Now()
	v := policyEvidence(l, 2)
	v.Root.Start += "reused"
	policySample(t, m, l, v, at)
	v = policyEvidence(l, 2)
	v.CPUCores = nil
	policySample(t, m, l, v, at.Add(time.Second))
	v = policyEvidence(l, 2)
	v.Window = 4 * time.Second
	policySample(t, m, l, v, at.Add(2*time.Second))
	all, _ := m.Incidents(l.ID)
	if len(all) != 0 {
		t.Fatal("unattributed or overlong observation opened incident")
	}
}

func TestPolicyMemoryLowerBoundAndUnknownMetric(t *testing.T) {
	m, l, _ := policyFixture(t, "warn", "poll")
	at := time.Now()
	v := policyEvidence(l, .1)
	memory := uint64(2048)
	v.AccountedMemoryBytes, v.MemoryMetric, v.Complete = &memory, "sum-private-resident", false
	v.MemoryQuality = "partial-reported-sum"
	policySample(t, m, l, v, at)
	policySample(t, m, l, v, at.Add(time.Second))
	i := oneIncident(t, m, l.ID)
	if i.Resource != "memory" || i.WarningCount != 1 || i.Evidence.AccountedMemoryBytes == nil || *i.Evidence.AccountedMemoryBytes != memory || i.Evidence.Live {
		t.Fatalf("partial lower-bound overage not recorded with scoped evidence: %+v", i)
	}
	v.MemoryMetric = "unknown"
	policySample(t, m, l, v, at.Add(2*time.Second))
	if oneIncident(t, m, l.ID).BreachSince != nil {
		t.Fatal("unknown memory metric advanced continuity")
	}
	memory = 512
	v.MemoryMetric, v.Complete = "sum-private-resident", true
	v.MemoryQuality = "reported-sum"
	policySample(t, m, l, v, at.Add(3*time.Second))
	i = oneIncident(t, m, l.ID)
	if i.State != "closed" || i.Evidence.AccountedMemoryBytes == nil || *i.Evidence.AccountedMemoryBytes != memory {
		t.Fatal("closing evidence was not recorded", i)
	}
}

func TestPolicyIntegralMemoryThresholdBoundary(t *testing.T) {
	limit := int64(math.MaxInt64 - 1)
	if memoryExceeds(uint64(limit), limit, 0) || !memoryExceeds(uint64(limit)+1, limit, 0) {
		t.Fatal("large integral memory threshold lost a one-byte boundary")
	}
	if memoryExceeds(150, 100, .5) || !memoryExceeds(151, 100, .5) {
		t.Fatal("tolerance boundary was not exact")
	}
}

func TestPolicyResolutionValidationAndImmutableSnapshot(t *testing.T) {
	m, l, _ := policyFixture(t, "warn", "poll")
	for _, p := range []model.UsagePolicy{{Mode: "kill"}, {Mode: "warn", Window: time.Nanosecond}, {Mode: "warn", Grace: -1}, {Mode: "warn", Warnings: 11}, {Mode: "warn", Tolerance: 2}} {
		r := l.Request
		r.UsagePolicy = &p
		if err := m.resolveUsagePolicy(&r, true); err == nil {
			t.Fatalf("invalid policy accepted: %+v", p)
		}
	}
	r := l.Request
	p := &model.UsagePolicy{Mode: "warn"}
	r.UsagePolicy = p
	if err := m.resolveUsagePolicy(&r, true); err != nil {
		t.Fatal(err)
	}
	p.Mode = "revoke"
	if r.UsagePolicy.Mode != "warn" || r.UsagePolicy.Window != 5*time.Second || r.UsagePolicy.Warnings != 3 {
		t.Fatal("policy defaults or snapshot not resolved")
	}
	if err := m.resolveUsagePolicy(&r, false); err == nil {
		t.Fatal("raw lease acquired process enforcement")
	}
	r.Agent = ""
	if err := m.resolveUsagePolicy(&r, true); err == nil {
		t.Fatal("warning policy accepted no recipient")
	}
}

func TestPolicyOutboxAttemptRecoveryAndExactRecipient(t *testing.T) {
	m, l, c := policyFixture(t, "warn", "native")
	at := time.Now()
	for n := 0; n <= 1; n++ {
		policySample(t, m, l, policyEvidence(l, 2), at.Add(time.Duration(n)*time.Second))
	}
	i := oneIncident(t, m, l.ID)
	message := i.Messages[0]
	id, item, body, ok := m.claimPolicyDelivery()
	if !ok || id != message || item.Target != c || body == "" {
		t.Fatal("durable exact recipient not claimed", id, item, ok)
	}
	if _, _, _, ok = m.claimPolicyDelivery(); ok {
		t.Fatal("native warning attempted twice")
	}
	// Simulate recovery at the native side-effect boundary without invoking an
	// external CLI. It must preserve the polling entry and never replay.
	if err := m.recoverDeliveries(); err != nil {
		t.Fatal(err)
	}
	if err := m.recoverPolicyOutbox(); err != nil {
		t.Fatal(err)
	}
	msgs, err := m.Messages()
	if err != nil || len(msgs) != 1 || msgs[0].Deliveries[c.ID].State != "unknown" {
		t.Fatal("interrupted delivery not unknown", msgs, err)
	}
	if _, _, _, ok = m.claimPolicyDelivery(); ok {
		t.Fatal("uncertain delivery replayed")
	}
	page, err := m.AgentInbox(t.Context(), InboxRequest{ID: c.ID, Session: c.Session, Registration: c.Registration})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("durable fallback lost", page, err)
	}
}

func TestPolicyUnattemptedOutboxSurvivesRecoveryAndReplyIsIdempotent(t *testing.T) {
	m, l, c := policyFixture(t, "warn", "native")
	at := time.Now()
	policySample(t, m, l, policyEvidence(l, 2), at)
	policySample(t, m, l, policyEvidence(l, 2), at.Add(time.Second))
	if err := m.recoverDeliveries(); err != nil {
		t.Fatal(err)
	}
	msgs, _ := m.Messages()
	if msgs[0].Deliveries[c.ID].State != "pending" {
		t.Fatal("unattempted warning marked unknown")
	}
	id, _, _, ok := m.claimPolicyDelivery()
	if !ok {
		t.Fatal("unattempted warning was lost")
	}
	d := agents.Delivery{State: "responded", Mode: c.Delivery, Registration: c.Registration, Reply: "I will reduce concurrency"}
	for range 2 {
		if err := m.finishPolicyDelivery(id, d, "done"); err != nil {
			t.Fatal(err)
		}
	}
	msgs, _ = m.Messages()
	if len(msgs) != 2 || msgs[0].ReplyTo != id {
		t.Fatal("native reply missing or duplicated", msgs)
	}
}

func TestPolicyNativeFailureRetainsPollingAndDeadline(t *testing.T) {
	m, l, c := policyFixture(t, "warn", "native")
	at := time.Now()
	policySample(t, m, l, policyEvidence(l, 2), at)
	policySample(t, m, l, policyEvidence(l, 2), at.Add(time.Second))
	before := oneIncident(t, m, l.ID)
	id, item, body, ok := m.claimPolicyDelivery()
	if !ok {
		t.Fatal("no warning to deliver")
	}
	// No executable can be resolved in this isolated environment; never invoke
	// an unrelated real CLI or borrow the user's authentication for a unit test.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	d := agents.Deliver(t.Context(), item.Target, body)
	d.Mode, d.Registration = item.Target.Delivery, item.Target.Registration
	if d.State != "failed" {
		t.Fatal("missing CLI did not report failure", d)
	}
	if err := m.finishPolicyDelivery(id, d, "done"); err != nil {
		t.Fatal(err)
	}
	page, err := m.AgentInbox(t.Context(), InboxRequest{ID: c.ID, Session: c.Session, Registration: c.Registration})
	if err != nil || len(page.Items) != 1 || page.Items[0].Message.Deliveries[c.ID].State != "failed" {
		t.Fatal("fallback lost after native failure", page, err)
	}
	after := oneIncident(t, m, l.ID)
	if !after.Deadline.Equal(*before.Deadline) || after.WarningCount != before.WarningCount {
		t.Fatal("native failure changed remediation entitlement")
	}
	if _, _, _, ok = m.claimPolicyDelivery(); ok {
		t.Fatal("failed invocation automatically retried")
	}
}

func TestPolicySupersededWarningNotInvoked(t *testing.T) {
	m, l, c := policyFixture(t, "warn", "native")
	at := time.Now()
	policySample(t, m, l, policyEvidence(l, 2), at)
	policySample(t, m, l, policyEvidence(l, 2), at.Add(time.Second))
	policySample(t, m, l, policyEvidence(l, .1), at.Add(2*time.Second))
	if _, _, _, ok := m.claimPolicyDelivery(); ok {
		t.Fatal("closed incident invoked native delivery")
	}
	msgs, err := m.Messages()
	if err != nil || len(msgs) != 1 || msgs[0].Deliveries[c.ID].State != "superseded" {
		t.Fatal("superseded outcome not recorded", msgs, err)
	}
}

func TestPolicyDurableDecisionWithoutJobNeverSignals(t *testing.T) {
	m, l, _ := policyFixture(t, "warn", "poll")
	at := time.Now()
	policySample(t, m, l, policyEvidence(l, 2), at)
	i := oneIncident(t, m, l.ID)
	i.State = "revocation_pending"
	m.mu.Lock()
	if err := m.saveIncident(i, "incident.revocation_requested", nil, nil); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	j := m.jobs[l.ID]
	delete(m.jobs, l.ID)
	err := m.reconcileIncidents()
	m.jobs[l.ID] = j // Restore test-owned handle for normal safe cleanup.
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	current, _ := m.Inspect(l.ID)
	i = oneIncident(t, m, l.ID)
	if current.State != "active" || i.State != "closed" || i.Outcome != "supervision_unavailable" {
		t.Fatal("saved evidence restored control authority", current, i)
	}
	// The incident and resolved policy really live in SQLite, not only a map.
	var raw []byte
	if err = m.db.QueryRow(`SELECT data FROM usage_incidents WHERE id=?`, i.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var durable model.UsageIncident
	if err = json.Unmarshal(raw, &durable); err != nil || durable.Policy.Mode != "warn" {
		t.Fatal("policy evidence missing", err)
	}
}
