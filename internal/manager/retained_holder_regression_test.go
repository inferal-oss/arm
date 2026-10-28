package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
)

// The real TCP listener and process-local relay are admitted through their public
// entry points. Their caller-owned completion witnesses stay open across every
// failed SQL cut: cancellation must interrupt IO without certifying settlement.
type retainedHolderFixture struct {
	m                          *Manager
	clock                      *controlledLeaseClock
	relay                      *RelayTransit
	forward                    *ForwardListener
	relayJoined, forwardJoined chan struct{}
	acceptDone                 chan error
	rows                       map[string]string
	checkpoint                 string
	once                       sync.Once
}

func newRetainedHolderFixture(t *testing.T) *retainedHolderFixture {
	t.Helper()
	c := &controlledLeaseClock{sample: leaseclock.Sample{Boot: "retained-holder-boot", Tick: int64(time.Hour), Wall: time.Now().UTC()}}
	m, err := openWithLeaseClock(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 2, Memory: 16 << 20, Disk: 16 << 20}, c.read)
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 64, 64, 0, 64, 0)
	x := &retainedHolderFixture{m: m, clock: c, relayJoined: make(chan struct{}), forwardJoined: make(chan struct{}), rows: map[string]string{}}
	t.Cleanup(func() { x.cleanup(t) })
	x.relay, err = m.AcquireRelayTransit(context.Background(), relayRequest("retained-relay"), x.relayJoined)
	if err != nil {
		t.Fatal(err)
	}
	b := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
	x.forward, err = m.AcquireForwardListener(context.Background(), strings.Repeat("d", 32), "retained original accept", b, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, x.forwardJoined)
	if err != nil {
		t.Fatal(err)
	}
	x.acceptDone = make(chan error, 1)
	go func() {
		conn, e := x.forward.Listener().Accept()
		if conn != nil {
			_ = conn.Close()
		}
		x.acceptDone <- e
	}()
	for _, id := range []string{x.relay.id, x.forward.id} {
		var raw string
		if err = m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		x.rows[id] = raw
	}
	if err = m.db.QueryRow(`SELECT value FROM metadata WHERE key='lease_clock'`).Scan(&x.checkpoint); err != nil {
		t.Fatal(err)
	}
	return x
}

// A lost native join retains the original handles; it cannot mint a closed
// completion witness or enter Manager.Close with an unresolved owner.
var retainedHolderQuarantine struct {
	sync.Mutex
	fixtures []*retainedHolderFixture
}

func (x *retainedHolderFixture) cleanup(t *testing.T) {
	t.Helper()
	x.once.Do(func() {
		x.m.mu.Lock()
		for _, name := range []string{"refuse_retained_checkpoint", "refuse_retained_ending"} {
			_, _ = x.m.db.Exec(`DROP TRIGGER IF EXISTS ` + name)
		}
		for id, raw := range x.rows {
			_, _ = x.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, raw, id)
		}
		x.m.mu.Unlock()
		if x.forward != nil {
			x.forward.CloseOriginal()
		}
		if x.relay != nil {
			x.relay.cancel()
		}
		if x.acceptDone != nil {
			select {
			case <-x.acceptDone:
			case <-time.After(5 * time.Second):
				t.Error("original TCP accept did not join during cleanup; original handles retained")
				retainedHolderQuarantine.Lock()
				retainedHolderQuarantine.fixtures = append(retainedHolderQuarantine.fixtures, x)
				retainedHolderQuarantine.Unlock()
				return
			}
		}
		close(x.relayJoined)
		close(x.forwardJoined)
		if x.relay != nil {
			awaitTransportSignal(t, x.relay.workerDone, "original relay worker did not join")
		}
		if x.forward != nil {
			awaitTransportSignal(t, x.forward.workerDone, "original listener worker did not join")
		}
		if err := x.m.Close(); err != nil {
			t.Error(err)
		}
	})
}

func (x *retainedHolderFixture) refuse(t *testing.T) {
	t.Helper()
	if _, err := x.m.db.Exec(`CREATE TRIGGER refuse_retained_checkpoint BEFORE UPDATE ON metadata WHEN NEW.key='lease_clock' BEGIN SELECT RAISE(ABORT,'retained checkpoint refused'); END`); err != nil {
		t.Fatal(err)
	}
}
func (x *retainedHolderFixture) expire(t *testing.T) {
	t.Helper()
	if x.relay.ctx.Err() != nil || x.forward.ctx.Err() != nil || !time.Now().Before(*x.relay.result.Expires) || !time.Now().Before(*x.forward.result.Expires) {
		t.Fatal("wall/caller context expired before native cut")
	}
	deadline, ok := x.forward.ctx.Deadline()
	if !ok || time.Until(deadline) < 15*time.Second {
		t.Fatal("independent Go timeout is not live")
	}
	x.clock.sample.Tick = max(x.relay.result.Clock.End, x.forward.result.Clock.End) + 1
}
func (x *retainedHolderFixture) reconcile() error {
	x.m.mu.Lock()
	defer x.m.mu.Unlock()
	return x.m.reconcileLeaseExpiries()
}
func (x *retainedHolderFixture) assertRaw(t *testing.T, want map[string]string) {
	t.Helper()
	x.m.mu.Lock()
	defer x.m.mu.Unlock()
	for id, raw := range want {
		var got string
		if err := x.m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&got); err != nil || got != raw {
			t.Fatalf("cancellation changed charged row %s: %v", id, err)
		}
	}
	var got string
	if err := x.m.db.QueryRow(`SELECT value FROM metadata WHERE key='lease_clock'`).Scan(&got); err != nil || got != x.checkpoint {
		t.Fatal("refused checkpoint advanced durable authority", err)
	}
}
func retainedCancelled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("known expired original retained owner was not cancelled")
	}
}
func retainedLive(t *testing.T, ctx context.Context) {
	t.Helper()
	if ctx.Err() != nil {
		t.Fatal("unproven retained binding cancelled its original")
	}
}

func TestRetainedHolderCheckpointRefusalCancelsOriginalsWithoutSettlement(t *testing.T) {
	x := newRetainedHolderFixture(t)
	x.refuse(t)
	x.expire(t)
	for i := 0; i < 3; i++ {
		if err := x.reconcile(); err == nil || !strings.Contains(err.Error(), "retained checkpoint refused") {
			t.Fatal("checkpoint refusal lost", err)
		}
	}
	retainedCancelled(t, x.relay.ctx)
	retainedCancelled(t, x.forward.ctx)
	select {
	case err := <-x.acceptDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatal("original accept ended without listener cancellation", err)
		}
		x.acceptDone = nil
	case <-time.After(5 * time.Second):
		t.Fatal("known expired original TCP accept did not return")
	}
	x.assertRaw(t, x.rows)
	if _, err := x.relay.FinishAfterJoin(); errorCode(err) != "relay_not_joined" {
		t.Fatal("relay settled without original join", err)
	}
	if _, err := x.forward.FinishAfterJoin(); errorCode(err) != "listener_not_joined" {
		t.Fatal("listener settled without original join", err)
	}
	if _, err := x.m.Acquire(clockRequest("denied-after-refusal", time.Minute), nil); err == nil {
		t.Fatal("checkpoint refusal restored admission")
	}
	x.assertRaw(t, x.rows)
}

func TestRetainedHolderCheckpointRefusalRejectsChangedBindings(t *testing.T) {
	changes := []struct {
		name   string
		change func(*model.Lease)
	}{
		{"physical-key", func(l *model.Lease) { l.ID = "unrelated-body" }},
		{"host", func(l *model.Lease) { l.Host += "-changed" }},
		{"owner", func(l *model.Lease) { l.Owner += "-changed" }},
		{"request", func(l *model.Lease) { l.Request.Reason += " changed" }},
		{"kind", func(l *model.Lease) { l.Request.Kind = "storage" }},
		{"pid", func(l *model.Lease) { l.PID = 12345 }},
	}
	for _, kind := range []string{"relay", "forward"} {
		for _, change := range changes {
			t.Run(kind+"/"+change.name, func(t *testing.T) {
				x := newRetainedHolderFixture(t)
				x.refuse(t)
				x.expire(t)
				id := x.relay.id
				ctx := x.relay.ctx
				sibling := x.forward.ctx
				if kind == "forward" {
					id = x.forward.id
					ctx = x.forward.ctx
					sibling = x.relay.ctx
				}
				var l model.Lease
				if err := json.Unmarshal([]byte(x.rows[id]), &l); err != nil {
					t.Fatal(err)
				}
				change.change(&l)
				raw, err := json.Marshal(l)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = x.m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), id); err != nil {
					t.Fatal(err)
				}
				if err = x.reconcile(); err == nil {
					t.Fatal("checkpoint refusal lost")
				}
				retainedLive(t, ctx)
				retainedCancelled(t, sibling)
				want := map[string]string{}
				for key, value := range x.rows {
					want[key] = value
				}
				want[id] = string(raw)
				x.assertRaw(t, want)
			})
		}
	}
}

func TestRetainedHolderCheckpointRefusalRequiresFreshCompleteAuthority(t *testing.T) {
	for _, mode := range []string{"below-deadline", "fresh-error", "fresh-boot", "fresh-regression", "roster-decode"} {
		t.Run(mode, func(t *testing.T) {
			x := newRetainedHolderFixture(t)
			x.expire(t)
			if mode == "roster-decode" {
				if _, err := x.m.db.Exec(`CREATE TRIGGER refuse_retained_checkpoint BEFORE UPDATE ON metadata WHEN NEW.key='lease_clock' BEGIN UPDATE leases SET data='{' WHERE id='` + x.forward.id + `'; SELECT RAISE(FAIL,'retained checkpoint refused'); END`); err != nil {
					t.Fatal(err)
				}
			} else {
				x.refuse(t)
			}
			if mode == "below-deadline" {
				x.clock.sample.Tick = min(x.relay.result.Clock.End, x.forward.result.Clock.End) - 1
			}
			reads := 0
			original := x.m.leaseClockRead
			x.m.leaseClockRead = func() (leaseclock.Sample, error) {
				reads++
				s, e := original()
				if reads == 2 {
					switch mode {
					case "fresh-error":
						return leaseclock.Sample{}, errors.New("fresh sample refused")
					case "fresh-boot":
						s.Boot += "-changed"
					case "fresh-regression":
						s.Tick--
					}
				}
				return s, e
			}
			defer func() { x.m.leaseClockRead = original }()
			if err := x.reconcile(); err == nil {
				t.Fatal("checkpoint/authority refusal lost")
			}
			retainedLive(t, x.relay.ctx)
			retainedLive(t, x.forward.ctx)
			if mode != "roster-decode" {
				x.assertRaw(t, x.rows)
			}
		})
	}
}

func TestRetainedHolderExplicitEndCancelsBeforeUnreadableRow(t *testing.T) {
	for _, kind := range []string{"relay", "forward"} {
		t.Run(kind, func(t *testing.T) {
			x := newRetainedHolderFixture(t)
			id := x.relay.id
			ctx := x.relay.ctx
			sibling := x.forward.ctx
			if kind == "forward" {
				id = x.forward.id
				ctx = x.forward.ctx
				sibling = x.relay.ctx
			}
			if _, err := x.m.db.Exec(`UPDATE leases SET data='{' WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			if _, err := x.m.End(id, "cancelled"); err == nil {
				t.Fatal("unreadable row was accepted")
			}
			retainedCancelled(t, ctx)
			retainedLive(t, sibling)
			var raw string
			if err := x.m.db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&raw); err != nil || raw != "{" {
				t.Fatal("explicit cancellation mutated unreadable row", err)
			}
		})
	}
}

func TestRetainedHolderExpiryVisitsSiblingAfterEndingWriteFailure(t *testing.T) {
	x := newRetainedHolderFixture(t)
	if _, err := x.m.db.Exec(`CREATE TRIGGER refuse_retained_ending BEFORE UPDATE ON leases WHEN NEW.id='` + x.relay.id + `' BEGIN SELECT RAISE(ABORT,'first retained ending refused'); END`); err != nil {
		t.Fatal(err)
	}
	x.expire(t)
	if err := x.reconcile(); err == nil || !strings.Contains(err.Error(), "first retained ending refused") {
		t.Fatal("ending refusal lost", err)
	}
	retainedCancelled(t, x.relay.ctx)
	retainedCancelled(t, x.forward.ctx)
	x.m.mu.Lock()
	l, err := x.m.get(x.forward.id)
	x.m.mu.Unlock()
	if err != nil || l.State != "ending" || l.Ended != nil {
		t.Fatal("later expired original was starved or settled before join", l, err)
	}
}
