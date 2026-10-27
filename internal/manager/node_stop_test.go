package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
	"getarm.org/internal/storage"
)

func stopManagerFixture(t *testing.T) (*Manager, nodestop.Actor, nodestop.Impact) {
	t.Helper()
	m, actor, impact := newStopManagerFixture(t)
	t.Cleanup(func() { _ = m.Close() })
	return m, actor, impact
}

// The caller owns exactly one Close. Reopen controls cannot also register the
// ordinary automatic-cleanup wrapper for that same Manager instance.
func newStopManagerFixture(t *testing.T) (*Manager, nodestop.Actor, nodestop.Impact) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	m, err := Open(home, model.Resources{CPU: 4})
	if err != nil {
		t.Fatal(err)
	}
	transferred := false
	defer func() {
		if !transferred {
			_ = m.Close()
		}
	}()
	f, err := os.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	id, err := storage.DirectoryIdentity(int(f.Fd()))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	target := nodestop.Target{Host: m.host, Owner: m.owner, Home: home, Directory: id, Generation: strings.Repeat("3", 32), Certificate: strings.Repeat("a", 64), SPKI: strings.Repeat("b", 64), Startup: nodestop.StartupContract}
	b, err := nodestop.NewBoundary(target)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ConfigureNodeStop(b); err != nil {
		t.Fatal(err)
	}
	actor := nodestop.Actor{Host: strings.Repeat("2", 32), Certificate: strings.Repeat("c", 64), SPKI: strings.Repeat("d", 64), Pair: strings.Repeat("4", 64), GrantRevision: 1}
	external := nodestop.Impact{Complete: true, SHA256: strings.Repeat("5", 64), Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{actor.Host}}
	transferred = true
	return m, actor, external
}

func stopApplication(t *testing.T, m *Manager, actor nodestop.Actor, external nodestop.Impact, policy model.ShutdownRequest) nodestop.Apply {
	t.Helper()
	p, err := m.PlanNodeStop("actual-ledger-stop", actor, policy, "review private authority stop", external)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return nodestop.Apply{Key: p.Key, Plan: p, ReviewSHA256: hash}
}

func TestNodeStopAtomicIntentAndSameKeyLookupNeverReplay(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	q := stopApplication(t, m, actor, external, model.ShutdownRequest{})
	r, err := m.BeginNodeStop(q, actor, true, external)
	if err != nil {
		t.Fatal(err)
	}
	s, present, err := nodestop.ReadState(m.db)
	if err != nil || !present || s.Running || s.Revision != 2 || s.Key != q.Key {
		t.Fatal(s, present, err)
	}
	// A subsequent changed scope cannot redispatch a known request. It returns
	// the original receipt without re-cancelling/releasing or bumping revision.
	external.Blockers = []nodestop.Blocker{{Class: "unknown", ID: "later", Detail: "unrelated later scope"}}
	again, err := m.BeginNodeStop(q, actor, true, external)
	if err != nil || again.Digest != r.Digest || again.Shutdown != r.Shutdown {
		t.Fatal(again, err)
	}
	q.Plan.Policy.Force = true
	q.ReviewSHA256, _ = q.Plan.Digest()
	if _, err = m.BeginNodeStop(q, actor, true, external); err == nil {
		t.Fatal("known key borrowed a new force policy")
	}
	var count int
	if err = m.db.QueryRow(`SELECT count(*) FROM events WHERE type='node_stop.accepted'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate acceptance", count, err)
	}
	report, err := m.AdvanceShutdown()
	if err != nil || report.Phase != "ready" {
		t.Fatal(report, err)
	}
	ready, present, err := m.NodeStopDrainReady(report)
	if err != nil || !present || ready.Phase != "drain-ready" {
		t.Fatal(ready, present, err)
	}
	if ready.Phase == "stopped" {
		t.Fatal("ordinary readiness fabricated native absence")
	}
}

func TestNodeStopPrecommitFailureRollsBackEveryIntentWithoutEffects(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	lease, err := m.Acquire(model.Request{Reason: "retained fixture reservation", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := stopApplication(t, m, actor, external, model.ShutdownRequest{Wait: true})
	if _, err = m.db.Exec(`CREATE TRIGGER fail_stop BEFORE INSERT ON metadata WHEN NEW.key='shutdown' BEGIN SELECT RAISE(ABORT,'fixture precommit refusal'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.BeginNodeStop(q, actor, true, external); err == nil {
		t.Fatal("precommit SQL failure lost")
	}
	if m.draining || m.nodeStopUnknown {
		t.Fatal("known precommit failure masqueraded as accepted/unknown commit")
	}
	if _, present, err := nodestop.ReadState(m.db); err != nil || present {
		t.Fatal("partial schema/intention committed", present, err)
	}
	got, err := m.get(lease.ID)
	if err != nil || got.State != "active" {
		t.Fatal("precommit failure changed reservation", got.State, err)
	}
}

func TestNodeStopActualCommitErrorLatchesUnknownAndRefusesProviderDispatch(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	lease, err := m.Acquire(model.Request{Reason: "must remain charged", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	q := stopApplication(t, m, actor, external, model.ShutdownRequest{Force: true})
	// The deferred foreign-key violation is detected by actual SQLite COMMIT,
	// rather than a mirror helper or an injected caller Boolean. It is treated
	// conservatively as unknown even though this fixture knows its root cause.
	for _, query := range []string{`PRAGMA foreign_keys=ON`, `CREATE TABLE stop_test_parent(id INTEGER PRIMARY KEY)`, `CREATE TABLE stop_test_child(parent INTEGER REFERENCES stop_test_parent(id) DEFERRABLE INITIALLY DEFERRED)`, `CREATE TRIGGER deferred_stop_commit AFTER INSERT ON metadata WHEN NEW.key='shutdown' BEGIN INSERT INTO stop_test_child VALUES(99); END`} {
		if _, err = m.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = m.BeginNodeStop(q, actor, true, external); err == nil {
		t.Fatal("actual COMMIT refusal normalized")
	}
	if !m.draining || !m.nodeStopUnknown {
		t.Fatal("ambiguous commit resumed admission")
	}
	if _, err = m.AdvanceShutdown(); err == nil {
		t.Fatal("unknown commit dispatched ordinary force continuation")
	}
	if _, present, err := m.NodeStopDrainReady(model.ShutdownReport{ID: "unconfirmed", Phase: "ready"}); err == nil || !present || errorCode(err) != "node_stop_outcome_unknown" {
		t.Fatal("unknown admission was mistaken for ordinary historical shutdown", present, err)
	}
	got, err := m.get(lease.ID)
	if err != nil || got.State != "active" {
		t.Fatal("unknown admission freed accounting", got.State, err)
	}
	if _, err = m.Acquire(model.Request{Reason: "must be refused", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, nil); err == nil {
		t.Fatal("new work admitted after unknown commit")
	}
}

func TestNodeStopUnsupportedCensusAndUnknownLeaseDoNotBecomeEmpty(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	lease, err := m.Acquire(model.Request{Reason: "unknown retained fixture", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lease.State = "needs_attention"
	lease.Failure = "retained unknown ownership"
	raw, _ := json.Marshal(lease)
	if _, err = m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), lease.ID); err != nil {
		t.Fatal(err)
	}
	q := stopApplication(t, m, actor, external, model.ShutdownRequest{Force: true})
	if _, err = m.BeginNodeStop(q, actor, true, external); err == nil {
		t.Fatal("force erased unresolved lease")
	}
	if m.draining {
		t.Fatal("refused stop changed admission")
	}
	t.Run("independent incomplete reporter", func(t *testing.T) {
		clean, peer, scope := stopManagerFixture(t)
		scope.Complete = false
		q := stopApplication(t, clean, peer, scope, model.ShutdownRequest{})
		if _, e := clean.BeginNodeStop(q, peer, true, scope); e == nil {
			t.Fatal("incomplete reporter borrowed empty-scope admission")
		}
	})
}
