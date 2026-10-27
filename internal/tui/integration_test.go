package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/server"
)

func TestDashboardUsesRealBackendForReviewedActionsAndReconnect(t *testing.T) {
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
			t.Error("private backend did not stop")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("private backend unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	m := newDashboard(ctx, home)
	msg := connect(ctx, home)().(connectedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	m.Update(msg)
	defer m.session.Close()
	if !m.connected || m.page.Epoch == "" {
		t.Fatal("shared backend snapshot/monitor missing")
	}
	if m.page.Latest == nil {
		select {
		case page := <-m.session.Telemetry():
			m.acceptMonitor(page)
		case <-time.After(5 * time.Second):
			t.Fatal("first shared host sample missing")
		}
	}
	var l model.Lease
	if err := client.Call(ctx, home, "acquire", model.Request{Reason: "terminal dashboard acceptance", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, &l); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Call(context.Background(), home, "release", map[string]string{"id": l.ID}, nil) }()
	m.Update(m.loadDetail(l.ID)())
	m.startReview("renew")
	m.inputMode = ""
	m.reviewTTL("3m")
	if m.review == nil {
		t.Fatal("extension review unavailable")
	}
	var changed model.Lease
	if err := client.Call(ctx, home, "renew", map[string]any{"id": l.ID, "ttl_ns": int64(2 * time.Minute)}, &changed); err != nil {
		t.Fatal(err)
	}
	m.Update(press(m, "y")())
	if !strings.Contains(m.notice, "revision_conflict") {
		t.Fatal("real stale revision accepted", m.notice)
	}
	m.Update(m.loadDetail(l.ID)())
	m.startReview("release")
	press(m, "n")
	var inspected model.Lease
	if err := client.Call(ctx, home, "inspect", map[string]string{"id": l.ID}, &inspected); err != nil || inspected.State != "active" {
		t.Fatal("decline changed live lease", err, inspected.State)
	}
	m.startReview("release")
	m.Update(press(m, "y")())
	if m.detail.State != "terminal" || m.pending {
		t.Fatal("reviewed release failed", m.notice)
	}
	old := m.session
	old.Close()
	m.Update(streamMsg{session: old, err: old.Err()})
	if m.connected {
		t.Fatal("lost session remained live")
	}
	newMsg := connect(ctx, home)().(connectedMsg)
	if newMsg.err != nil {
		t.Fatal(newMsg.err)
	}
	m.Update(newMsg)
	defer m.session.Close()
	if !m.connected || m.data.status.Reserved.CPU != 0 {
		t.Fatal("reconnect failed to load authoritative state")
	}
	var events []model.Event
	if err := client.Call(ctx, home, "events", map[string]int64{"after": 0}, &events); err != nil {
		t.Fatal(err)
	}
	terminalEvents := 0
	for _, event := range events {
		if event.Entity == l.ID && event.Type == "lease.released" {
			terminalEvents++
		}
	}
	if terminalEvents != 1 {
		t.Fatal("unexpected duplicate mutation events", terminalEvents, events)
	}
}
