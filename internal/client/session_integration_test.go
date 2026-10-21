package client_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/server"
)

func TestSessionUsesActualAuthenticatedARMBackend(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1}) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
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
	s, err := client.OpenSession(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var status model.Status
	if err := s.Call(ctx, "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	if err := s.Call(ctx, "subscribe", map[string]int64{"after": status.Cursor}, nil); err != nil {
		t.Fatal(err)
	}
	var monitor model.MonitorPage
	if err := s.Call(ctx, "monitor_subscribe", model.MonitorQuery{}, &monitor); err != nil || monitor.Host != status.Host || monitor.Epoch == "" {
		t.Fatal("shared monitor subscription did not return its atomic snapshot", err, monitor)
	}
	var lease model.Lease
	if err := s.Call(ctx, "acquire", model.Request{Reason: "multiplexed authenticated session", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, &lease); err != nil || lease.State != "active" {
		t.Fatal("native shared-session acquire", err, lease.State)
	}
	select {
	case event := <-s.Events():
		if event.Entity != lease.ID || event.Type != "lease.created" {
			t.Fatal("native event routed incorrectly", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native event missing")
	}
	if err := s.Call(ctx, "lease_action", model.LeaseAction{ID: lease.ID, Revision: lease.Revision, Action: "release", Consumers: []model.LeaseRevision{}}, &lease); err != nil || lease.State != "terminal" {
		t.Fatal("guarded action through shared session", err, lease.State)
	}
	if err := s.Call(ctx, "status", nil, &status); err != nil || status.Reserved.CPU != 0 {
		t.Fatal("native status response after event/action", err, status.Reserved)
	}
}
