package tui

import (
	"context"
	"getarm.org/internal/agents"
	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/server"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLeaseCheckinRealBackendRejectsStalePolicyAndPreservesEntitlement(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 2}) }()
	defer func() {
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("private fixture did not stop")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("private fixture unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var c agents.Contact
	if err := client.Call(ctx, home, "register", agents.Contact{ID: "tui-checkin", Session: uuid.NewString(), Adapter: "generic", Delivery: "poll", CWD: t.TempDir()}, &c); err != nil {
		t.Fatal(err)
	}
	// Policy configuration is independent of native CPU admission. Reserve an
	// actual finite ledger pool and prove its entitlement survives every edit.
	var status model.Status
	if err := client.Call(ctx, home, "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	capacity := int64(2)
	var created model.PoolChange
	if err := client.Call(ctx, home, "pool_mutate", model.PoolMutation{Operation: "create", Name: "terminal check-in fixture", Scope: "global", Unit: "permits", Capacity: &capacity, Reason: "real terminal check-in fixture", Key: local.ID(), Reviewed: &model.PoolReview{Authority: status.Host, Consumers: []model.LeaseRevision{}}}, &created); err != nil {
		t.Fatal(err)
	}
	var l model.Lease
	if err := client.Call(ctx, home, "acquire", model.Request{Agent: c.ID, Reason: "private terminal check-in test", Pools: []model.PoolClaim{{Pool: created.Pool.ID, Quantity: 1}}, TTL: time.Hour}, &l); err != nil {
		t.Fatal(err)
	}
	if l.State != "active" || l.Request.Agent != c.ID || l.Request.Resources.CPU != 0 || len(l.Request.Pools) != 1 || l.Request.Pools[0].Pool != created.Pool.ID || l.Request.Pools[0].Quantity != 1 || l.Expires == nil {
		t.Fatal("fixture did not acquire the exact real counted entitlement", l)
	}
	var charged model.PoolView
	if err := client.Call(ctx, home, "pool_inspect", map[string]string{"id": created.Pool.ID}, &charged); err != nil || charged.Reserved != 1 || charged.Available != 1 {
		t.Fatal("real check-in reservation missing", charged, err)
	}
	defer func() { _ = client.Call(context.Background(), home, "release", map[string]string{"id": l.ID}, nil) }()
	m := newDashboard(ctx, home)
	msg := connect(ctx, home)().(connectedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	m.Update(msg)
	defer m.session.Close()
	m.Update(m.loadDetail(l.ID)())
	openTestCheckin(t, m)
	press(m, "e")
	press(m, " ")
	press(m, "y")
	g := int64(0)
	var p model.LeaseReviewPolicy
	if err := client.Call(ctx, home, "lease_review_configure", model.LeaseReviewConfigure{Lease: l.ID, Revision: l.Revision, Generation: &g, Enabled: false}, &p); err != nil {
		t.Fatal(err)
	}
	cmd := press(m, "y")
	m.Update(cmd())
	if !strings.Contains(m.notice, "policy_conflict") || !strings.Contains(m.notice, "rejected") {
		t.Fatal("real stale policy not rejected", m.notice)
	}
	m.Update(press(m, "r")())
	press(m, "e")
	press(m, " ")
	press(m, "y")
	m.Update(press(m, "y")())
	if !strings.Contains(m.notice, "accepted") {
		t.Fatal(m.notice)
	}
	if err := client.Call(ctx, home, "lease_review_policy", map[string]string{"lease": l.ID}, &p); err != nil || !p.Enabled || p.Generation != 2 {
		t.Fatal(p, err)
	}
	// Reopen the actual accepted enabled policy. Do not replace the exact
	// Go-generated default strings with hand-written easy-to-parse values.
	m.Update(press(m, "r")())
	press(m, "e")
	if m.checkins.interval != "10m0s" || m.checkins.timeout != "1m0s" || !m.checkins.enabled {
		t.Fatal("actual enabled defaults not loaded", m.checkins)
	}
	press(m, "y")
	if !m.checkins.confirm || m.checkins.request == nil || m.checkins.request.Interval != 10*time.Minute || m.checkins.request.ReplyTimeout != time.Minute {
		t.Fatal("actual default policy cannot reach confirmation", m.checkins.note)
	}
	var beforeSave model.LeaseReviewPolicy
	if err := client.Call(ctx, home, "lease_review_policy", map[string]string{"lease": l.ID}, &beforeSave); err != nil || beforeSave.Generation != 2 {
		t.Fatal("opening review mutated the policy", beforeSave, err)
	}
	m.Update(press(m, "y")())
	if !strings.Contains(m.notice, "accepted") {
		t.Fatal("reviewed unchanged defaults not accepted", m.notice)
	}
	if err := client.Call(ctx, home, "lease_review_policy", map[string]string{"lease": l.ID}, &p); err != nil || p.Generation != 2 || p.Interval != 10*time.Minute || p.ReplyTimeout != time.Minute || !p.Enabled {
		t.Fatal("default form changed exact policy values", p, err)
	}
	var after model.Lease
	if err := client.Call(ctx, home, "inspect", map[string]string{"id": l.ID}, &after); err != nil || after.Revision != l.Revision || after.Expires == nil || !after.Expires.Equal(*l.Expires) || after.State != "active" || after.Request.Agent != c.ID || after.Request.Resources.CPU != 0 || len(after.Request.Pools) != 1 || after.Request.Pools[0] != l.Request.Pools[0] {
		t.Fatal("check-in settings changed resource entitlement", after, err)
	}
	if err := client.Call(ctx, home, "pool_inspect", map[string]string{"id": created.Pool.ID}, &charged); err != nil || charged.Reserved != 1 || charged.Available != 1 {
		t.Fatal("check-in settings changed counted-pool accounting", charged, err)
	}
}
