package client_test

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

func TestNativeNodeSessionNegotiatesActualLocalAuthority(t *testing.T) {
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
	var status model.Status
	if err := client.Call(ctx, home, "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	selection := model.NodeSelection{Host: status.Host, Source: status.Host}
	session, report, err := client.OpenNodeSession(ctx, home, selection)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !report.Context.Local || report.Context.Host != status.Host || report.ReadOnly {
		t.Fatal("local context misreported", report)
	}
	var observed model.Status
	if err = session.Call(ctx, "status", nil, &observed); err != nil || observed.Host != status.Host {
		t.Fatal(observed, err)
	}
	if err = session.Call(ctx, "subscribe", map[string]int64{"after": status.Cursor}, nil); err != nil {
		t.Fatal(err)
	}
	if err = session.Call(ctx, "node_context_open", selection, nil); err == nil {
		t.Fatal("late rebind admitted")
	}
	selection.Source = strings.Repeat("f", 32)
	if _, _, err = client.OpenNodeSession(ctx, home, selection); err == nil {
		t.Fatal("changed source silently rebound")
	}
}
