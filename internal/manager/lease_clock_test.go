package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/leaseclock"
	"github.com/yrashk/arm/internal/model"
)

type controlledLeaseClock struct {
	sample leaseclock.Sample
	err    error
}

func (c *controlledLeaseClock) read() (leaseclock.Sample, error) { return c.sample, c.err }
func clockManager(t *testing.T) (*Manager, *controlledLeaseClock) {
	t.Helper()
	c := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "controlled-boot", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	m, err := openWithLeaseClock(filepath.Join(t.TempDir(), "arm"), model.Resources{Memory: 1024}, c.read)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	return m, c
}
func clockRequest(key string, ttl time.Duration) model.Request {
	return model.Request{Key: key, Reason: "lease clock adversarial fixture", Resources: model.Resources{Memory: 1}, TTL: ttl}
}
func clockGrant(t *testing.T, m *Manager, key string, ttl time.Duration) model.Lease {
	t.Helper()
	l, err := m.Acquire(clockRequest(key, ttl), nil)
	if err != nil || l.State != "active" || l.Clock == nil {
		t.Fatal(l, err)
	}
	return l
}
func clockGet(t *testing.T, m *Manager, id string) model.Lease {
	t.Helper()
	l, err := m.get(id)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLeaseClockWallDiscontinuitiesDoNotAlterEntitlement(t *testing.T) {
	for _, jump := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		t.Run(jump.String(), func(t *testing.T) {
			m, c := clockManager(t)
			l := clockGrant(t, m, "live", time.Minute)
			c.sample.Wall = c.sample.Wall.Add(jump)
			c.sample.Tick += int64(20 * time.Second)
			m.tick()
			got := clockGet(t, m, l.ID)
			if got.State != "active" || got.Revision != l.Revision || !got.Expires.Equal(*l.Expires) {
				t.Fatal(got)
			}
			renewed, err := m.Renew(l.ID, 2*time.Minute)
			if err != nil || renewed.Clock.End != c.sample.Tick+int64(2*time.Minute) {
				t.Fatal(renewed, err)
			}
			c.sample.Tick = renewed.Clock.End
			if _, err = m.Renew(l.ID, 3*time.Minute); err == nil {
				t.Fatal("expired entitlement renewed")
			}
			if got = clockGet(t, m, l.ID); got.State != "terminal" || got.Outcome != "expired" {
				t.Fatal(got)
			}
		})
	}
}
func TestLeaseClockSuspendAndReconcileBeforeAdmission(t *testing.T) {
	m, c := clockManager(t)
	l := clockGrant(t, m, "expires", time.Minute)
	c.sample.Tick += int64(time.Minute) // wall deliberately frozen; models suspend
	next := clockGrant(t, m, "next", time.Minute)
	if got := clockGet(t, m, l.ID); got.State != "terminal" || got.Outcome != "expired" {
		t.Fatal(got)
	}
	if next.Clock.Anchor != c.sample.Tick {
		t.Fatal(next)
	}
}
func TestLeaseClockUncertaintyRetainsWithoutSignals(t *testing.T) {
	for _, kind := range []string{"read-error", "regressed", "scope-changed", "missing-deadline", "changed-wall", "unknown-version"} {
		t.Run(kind, func(t *testing.T) {
			m, c := clockManager(t)
			l := clockGrant(t, m, "kept", time.Minute)
			switch kind {
			case "read-error":
				c.err = errors.New("clock unavailable")
			case "regressed":
				c.sample.Tick--
			case "scope-changed":
				c.sample.Boot = "other"
			case "missing-deadline":
				l.Clock = nil
			case "changed-wall":
				x := l.Expires.Add(time.Hour)
				l.Expires = &x
			case "unknown-version":
				l.Clock.Version++
			}
			if err := m.put(l, "fixture.altered"); err != nil {
				t.Fatal(err)
			}
			m.tick()
			got := clockGet(t, m, l.ID)
			if !consuming(got.State) || got.Outcome == "expired" || got.Ended != nil {
				t.Fatal(got)
			}
			if _, err := m.Renew(l.ID, 2*time.Minute); err == nil {
				t.Fatal("uncertain renewed")
			}
			c.err = nil
			c.sample.Boot = "controlled-boot"
			c.sample.Tick += int64(time.Second)
			if _, err := m.Renew(l.ID, 2*time.Minute); err == nil {
				t.Fatal("uncertain silently restored")
			}
			s, err := m.Status()
			if err != nil || s.Reserved.Memory != 1 {
				t.Fatal(s, err)
			}
		})
	}
}
func TestLeaseClockRestartSameScopeAndOtherBoot(t *testing.T) {
	for _, kind := range []string{"live-wall-rollback", "overdue-frozen-wall", "other-boot", "regressed", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			c := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "boot-one", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
			home := filepath.Join(t.TempDir(), "arm")
			m, err := openWithLeaseClock(home, model.Resources{Memory: 1024}, c.read)
			if err != nil {
				t.Fatal(err)
			}
			l := clockGrant(t, m, "persisted", time.Minute)
			if kind == "legacy" {
				l.Clock = nil
				if err = m.put(l, "fixture.legacy"); err != nil {
					t.Fatal(err)
				}
			}
			if err = m.Close(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "live-wall-rollback":
				c.sample.Wall = c.sample.Wall.Add(-24 * time.Hour)
				c.sample.Tick += int64(10 * time.Second)
			case "overdue-frozen-wall":
				c.sample.Tick += int64(time.Minute)
			case "other-boot":
				c.sample.Boot = "boot-two"
				c.sample.Tick = 1
			case "regressed":
				c.sample.Tick--
			}
			m, err = openWithLeaseClock(home, model.Resources{Memory: 1024}, c.read)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			got := clockGet(t, m, l.ID)
			switch kind {
			case "live-wall-rollback":
				if got.State != "active" || got.Clock.End != l.Clock.End {
					t.Fatal(got)
				}
			case "overdue-frozen-wall":
				if got.State != "terminal" || got.Outcome != "expired" {
					t.Fatal(got)
				}
			default:
				if got.State != "needs_attention" || got.RecoveryReason != "lease_clock_uncertain" || got.Ended != nil {
					t.Fatal(got)
				}
			}
		})
	}
}
func TestLeaseClockRenewalRaceCannotReviveExpiry(t *testing.T) {
	m, c := clockManager(t)
	l := clockGrant(t, m, "race", time.Minute)
	c.sample.Tick = l.Clock.End
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			if _, err := m.Renew(l.ID, 2*time.Minute); err == nil {
				t.Error("late renewal succeeded")
			}
		})
	}
	wg.Wait()
	if got := clockGet(t, m, l.ID); got.State != "terminal" || got.Outcome != "expired" {
		t.Fatal(got)
	}
}

// Existing lifecycle fixtures that deliberately make entitlement overdue must
// change both audit wall time and elapsed provenance. Changing only Expires is
// separately tested as an inconsistent-record refusal, never an expiry witness.
func expireLeaseClockForTest(t *testing.T, l *model.Lease, wall time.Time) {
	t.Helper()
	sample, err := leaseclock.Read()
	if err != nil || sample.Tick < 2 {
		t.Fatal(sample, err)
	}
	l.Expires = &wall
	l.Clock = &leaseclock.Deadline{Version: 1, Boot: sample.Boot, Anchor: sample.Tick - 2, End: sample.Tick - 1, Wall: wall}
}

func TestLeaseClockReviewContextUsesElapsedRemaining(t *testing.T) {
	for _, jump := range []time.Duration{-24 * time.Hour, 24 * time.Hour} {
		t.Run(jump.String(), func(t *testing.T) {
			m, c := clockManager(t)
			contact := pollContact(t, m, "elapsed-review")
			q := clockRequest("elapsed-review", time.Minute)
			q.Agent = contact.ID
			l, err := m.Acquire(q, nil)
			if err != nil {
				t.Fatal(err)
			}
			dueReview(t, m, l)
			c.sample.Wall = c.sample.Wall.Add(jump)
			c.sample.Tick += int64(10 * time.Second)
			m.mu.Lock()
			eligible := m.reviewEligible(l)
			ctx, err := m.reviewContext(l, c.sample.Wall, 0)
			m.mu.Unlock()
			if !eligible || err != nil || ctx.Remaining != 50*time.Second || !ctx.Expires.Equal(*l.Expires) {
				t.Fatal(eligible, ctx, err)
			}
			if jump > 0 {
				reviewTick(t, m, c.sample.Wall)
				page := reviews(t, m, model.LeaseReviewQuery{Pending: true})
				if len(page.Items) != 1 || len(page.Items[0].Context) != 1 || page.Items[0].Context[0].Remaining != 50*time.Second || !page.Items[0].Deadline.Equal(c.sample.Wall.Add(50*time.Second)) {
					t.Fatal(page)
				}
			}
			c.sample.Tick = l.Clock.End
			m.mu.Lock()
			eligible = m.reviewEligible(l)
			_, err = m.reviewContext(l, c.sample.Wall, 0)
			m.mu.Unlock()
			if eligible || err == nil {
				t.Fatal("expired lease admitted to checkpoint")
			}
		})
	}
}

func TestLeaseClockUncertainRelayKeepsChargeUntilOriginalJoin(t *testing.T) {
	m := newManager(t)
	h, join := acquireTestRelay(t, m, "uncertain-clock-original-join")
	l, err := h.Lease()
	if err != nil {
		t.Fatal(err)
	}
	l.Clock.Version++
	m.mu.Lock()
	err = m.put(l, "fixture.clock_provenance_lost")
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	m.tick()
	select {
	case <-h.Context().Done():
	default:
		t.Fatal("exact original relay was not cancelled")
	}
	if _, err = h.FinishAfterJoin(); errorCode(err) != "relay_not_joined" {
		t.Fatal("uncertainty became reclamation", err)
	}
	assertRelayCharge(t, m, true)
	join()
	result, err := h.FinishAfterJoin()
	if err != nil || result.State != "terminal" || result.RecoveryReason != "lease_clock_uncertain" {
		t.Fatal(result, err)
	}
	assertRelayCharge(t, m, false)
}

func TestLeaseClockCheckpointFailureRefusesNewAuthority(t *testing.T) {
	m, c := clockManager(t)
	l := clockGrant(t, m, "retained-on-journal-failure", time.Minute)
	_, err := m.db.Exec(`CREATE TRIGGER fail_lease_clock_checkpoint BEFORE UPDATE ON metadata WHEN NEW.key='lease_clock' BEGIN SELECT RAISE(ABORT,'fixture checkpoint denied'); END`)
	if err != nil {
		t.Fatal(err)
	}
	c.sample.Tick += int64(2 * time.Second)
	if _, err = m.Acquire(clockRequest("must-not-grant", time.Minute), nil); err == nil {
		t.Fatal("granted despite failed clock checkpoint")
	}
	if _, err = m.Renew(l.ID, 2*time.Minute); err == nil {
		t.Fatal("renewed despite failed clock checkpoint")
	}
	got := clockGet(t, m, l.ID)
	if got.State != "active" || got.Revision != l.Revision || got.Clock.End != l.Clock.End {
		t.Fatal(got)
	}
	leases, err := m.list()
	if err != nil || len(leases) != 1 {
		t.Fatal(leases, err)
	}
	if _, err = m.db.Exec(`DROP TRIGGER fail_lease_clock_checkpoint`); err != nil {
		t.Fatal(err)
	}
	renewed, err := m.Renew(l.ID, 2*time.Minute)
	if err != nil || renewed.Clock.End != c.sample.Tick+int64(2*time.Minute) {
		t.Fatal(renewed, err)
	}
}

func TestLeaseClockCorruptDurableMarkerDoesNotRebaseline(t *testing.T) {
	c := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "boot-one", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	home := filepath.Join(t.TempDir(), "arm")
	m, err := openWithLeaseClock(home, model.Resources{Memory: 1024}, c.read)
	if err != nil {
		t.Fatal(err)
	}
	l := clockGrant(t, m, "corrupt-marker", time.Minute)
	if _, err = m.db.Exec(`UPDATE metadata SET value='{}' WHERE key='lease_clock'`); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	c.sample.Tick += int64(time.Second)
	m, err = openWithLeaseClock(home, model.Resources{Memory: 1024}, c.read)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err = m.Renew(l.ID, 2*time.Minute); errorCode(err) != "lease_clock_unavailable" {
		t.Fatal(err)
	}
	if _, err = m.Acquire(clockRequest("corrupt-new", time.Minute), nil); errorCode(err) != "lease_clock_unavailable" {
		t.Fatal(err)
	}
	got := clockGet(t, m, l.ID)
	if got.State != "needs_attention" || got.RecoveryReason != "lease_clock_uncertain" || got.Clock.End != l.Clock.End || got.Ended != nil {
		t.Fatal(got)
	}
}

func TestLeaseClockSpecializedListenerRecoverySurvivesUncertainDeadline(t *testing.T) {
	for _, mode := range []string{"missing", "other-boot"} {
		t.Run(mode, func(t *testing.T) {
			c := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "boot-one", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
			home := filepath.Join(t.TempDir(), "arm")
			m, err := openWithLeaseClock(home, model.Resources{CPU: 1, Memory: 16 << 20}, c.read)
			if err != nil {
				t.Fatal(err)
			}
			cpuReading(m, 64, 64, 0, 64, 0) // controlled admission test; no native CPU claim
			joined := make(chan struct{})
			binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "clock-recovery", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
			h, err := m.AcquireForwardListener(context.Background(), strings.Repeat("d", 32), "controlled lost deadline recovery", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined)
			if err != nil {
				m.Close()
				t.Fatal(err)
			}
			original, err := h.Lease()
			if err != nil {
				h.CloseOriginal()
				close(joined)
				m.Close()
				t.Fatal(err)
			}
			// Positively close and join the original fixture. Restoring its durable
			// pre-crash row below models a lost settlement record, without abandoning
			// an actual socket/worker or treating a cancellation request as closure.
			h.CloseOriginal()
			close(joined)
			if settled, e := h.FinishAfterJoin(); e != nil || settled.State != "terminal" {
				m.Close()
				t.Fatal(settled, e)
			}
			<-h.workerDone
			if mode == "missing" {
				original.Clock = nil
			}
			m.mu.Lock()
			err = m.put(original, "fixture.lost_original_settlement")
			m.mu.Unlock()
			if err != nil {
				m.Close()
				t.Fatal(err)
			}
			if err = m.Close(); err != nil {
				t.Fatal(err)
			}
			if mode == "other-boot" {
				c.sample.Boot = "boot-two"
				c.sample.Tick = 1
			}
			m, err = openWithLeaseClock(home, model.Resources{CPU: 1, Memory: 16 << 20}, c.read)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			got := clockGet(t, m, original.ID)
			if got.State != "needs_attention" || got.Outcome != "forward_listener_supervision_lost" || got.RecoveryReason != "lease_clock_uncertain" {
				t.Fatal(got)
			}
			status, err := m.Status()
			if err != nil || status.Reserved.Memory != 1<<20 {
				t.Fatal(status.Reserved, err)
			}
			recovered, err := m.RecoverForwardListener(original.ID, "original fixture descriptors closed and original workers positively joined before database reopen")
			if err != nil || recovered.State != "terminal" || recovered.Outcome != "reconciled" {
				t.Fatal(recovered, err)
			}
		})
	}
}

func TestLeaseClockDatabaseProofPreservesActiveAndSettledBoundaries(t *testing.T) {
	c := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "controlled-boot", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	home := filepath.Join(t.TempDir(), "arm")
	m, err := openWithLeaseClock(home, model.Resources{Memory: 1024}, c.read)
	if err != nil {
		t.Fatal(err)
	}
	lease := clockGrant(t, m, "clock-proof", time.Minute)
	owner := m.owner
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	inspect := func(settled bool) {
		t.Helper()
		source, e := database.Offline(t.Context(), home)
		if e != nil {
			t.Fatal(e)
		}
		proof, e := database.ProveDatabaseSettled(t.Context(), source, owner)
		source.Close()
		if e != nil || !proof.Complete || proof.Settled != settled {
			t.Fatal(proof, e)
		}
	}
	inspect(false)
	m, err = openWithLeaseClock(home, model.Resources{Memory: 1024}, c.read)
	if err != nil {
		t.Fatal(err)
	}
	final, err := m.End(lease.ID, "released")
	if err != nil || final.State != "terminal" {
		m.Close()
		t.Fatal(final, err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	inspect(true)
}

func TestLeaseClockDatabaseProofRejectsMalformedDeadlineAudit(t *testing.T) {
	c := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "controlled-boot", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	home := filepath.Join(t.TempDir(), "arm")
	m, err := openWithLeaseClock(home, model.Resources{Memory: 1024}, c.read)
	if err != nil {
		t.Fatal(err)
	}
	l := clockGrant(t, m, "clock-proof-malformed", time.Minute)
	final, err := m.End(l.ID, "released")
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	owner := m.owner
	original, err := json.Marshal(final)
	if err != nil {
		m.Close()
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, kind := range []string{"unknown", "null-object", "missing-anchor", "null-end", "version", "mismatch", "zero-wall", "span"} {
		t.Run(kind, func(t *testing.T) {
			var body, clock map[string]json.RawMessage
			if err := json.Unmarshal(original, &body); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(body["elapsed_deadline"], &clock); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "unknown":
				clock["future"] = json.RawMessage(`true`)
			case "null-object":
			case "missing-anchor":
				delete(clock, "anchor_ns")
			case "null-end":
				clock["end_ns"] = json.RawMessage(`null`)
			case "version":
				clock["version"] = json.RawMessage(`2`)
			case "mismatch":
				clock["wall_expires"], _ = json.Marshal(final.Expires.Add(time.Nanosecond))
			case "zero-wall":
				clock["wall_expires"], _ = json.Marshal(time.Time{})
				body["expires_at"] = clock["wall_expires"]
			case "span":
				clock["end_ns"], _ = json.Marshal(final.Clock.Anchor + int64(8*24*time.Hour))
			}
			body["elapsed_deadline"], _ = json.Marshal(clock)
			if kind == "null-object" {
				body["elapsed_deadline"] = json.RawMessage(`null`)
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), l.ID); err != nil {
				t.Fatal(err)
			}
			source, err := database.Offline(t.Context(), home)
			if err != nil {
				t.Fatal(err)
			}
			proof, err := database.ProveDatabaseSettled(t.Context(), source, owner)
			source.Close()
			if err == nil && proof.Complete && proof.Settled {
				t.Fatal("malformed clock authorized settled database proof", kind, proof)
			}
		})
	}
}

func TestLeaseClockHeldStartFencesWithoutPeriodicTick(t *testing.T) {
	c := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "held-start-boot", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	m, err := openWithLeaseClock(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1, Memory: 64 << 20}, c.read)
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	defer m.Close()
	relayJoined, listenerJoined := make(chan struct{}), make(chan struct{})
	relay, err := m.AcquireRelayTransit(t.Context(), model.Request{Key: "held-start-relay", Reason: "native start fence", Resources: model.Resources{CPU: .01, Memory: 1 << 20}, TTL: time.Minute}, relayJoined)
	if err != nil {
		t.Fatal(err)
	}
	defer close(relayJoined)
	binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "fence", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
	listener, err := m.AcquireForwardListener(t.Context(), strings.Repeat("d", 32), "native listener fence", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, listenerJoined)
	if err != nil {
		t.Fatal(err)
	}
	defer close(listenerJoined)
	if err = relay.CheckActive(t.Context()); err != nil {
		t.Fatal(err)
	}
	if remaining, err := listener.Remaining(t.Context()); err != nil || remaining != time.Minute {
		t.Fatal(remaining, err)
	}
	// A structurally valid later deadline on a replaced row cannot borrow this
	// original holder's native capability, even if revision/request are retained.
	for _, id := range []string{relay.id, listener.id} {
		original := clockGet(t, m, id)
		changed := original
		deadline := *original.Clock
		deadline.End += int64(time.Minute)
		deadline.Wall = deadline.Wall.Add(time.Minute)
		changed.Clock, changed.Expires = &deadline, &deadline.Wall
		if err := m.put(changed, "test.rebased_hold"); err != nil {
			t.Fatal(err)
		}
		if id == relay.id {
			if err := relay.CheckActive(t.Context()); err == nil {
				t.Fatal("rebased row lent relay authority")
			}
		} else if _, err := listener.Remaining(t.Context()); err == nil {
			t.Fatal("rebased row lent listener authority")
		}
		if err := m.put(original, "test.restore_hold"); err != nil {
			t.Fatal(err)
		}
	}
	c.sample.Tick += int64(time.Minute)
	if relay.Context().Err() != nil || listener.Context().Err() != nil {
		t.Fatal("fixture must leave original contexts live")
	}
	if err = relay.CheckActive(t.Context()); err == nil {
		t.Fatal("expired relay admitted new work before tick")
	}
	if _, err = listener.Remaining(t.Context()); err == nil {
		t.Fatal("expired listener admitted new work before tick")
	}
	status, err := m.Status()
	if err != nil || status.Reserved.Memory < 2<<20 {
		t.Fatal("failed check certified reclamation", status.Reserved, err)
	}
}
