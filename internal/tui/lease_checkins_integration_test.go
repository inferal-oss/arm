package tui

import (
	"context"
	"github.com/google/uuid"
	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/server"
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
	var l model.Lease
	if err := client.Call(ctx, home, "acquire", model.Request{Agent: c.ID, Reason: "private terminal check-in test", Resources: model.Resources{CPU: .1}, TTL: time.Hour}, &l); err != nil {
		t.Fatal(err)
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
	if err := client.Call(ctx, home, "inspect", map[string]string{"id": l.ID}, &after); err != nil || after.Revision != l.Revision || !after.Expires.Equal(*l.Expires) || after.State != "active" {
		t.Fatal("check-in settings changed resource entitlement", after, err)
	}
}
