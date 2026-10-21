package server_test

import (
	"context"
	"database/sql"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/client"
	"github.com/yrashk/arm/internal/model"
)

func TestShutdownTimeoutContinuesAfterRequestDisconnect(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var lease model.Lease
	if err := client.Call(ctx, home, "acquire", model.Request{Reason: "disconnect timeout fixture", Resources: model.Resources{CPU: 1}, TTL: time.Minute}, &lease); err != nil {
		t.Fatal(err)
	}
	requestCtx, disconnect := context.WithCancel(ctx)
	var report model.ShutdownReport
	if err := client.Call(requestCtx, home, "shutdown", model.ShutdownRequest{Wait: true, Timeout: 100 * time.Millisecond}, &report); err != nil {
		t.Fatal(err)
	}
	disconnect()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(home, "endpoint.json")); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("backend-owned timeout stopped with requesting socket")
}

func TestForcedShutdownRetainsOccupiedPortAndInspection(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var lease model.Lease
	r := model.Request{Kind: "ports", Reason: "retain foreign occupant", TTL: time.Minute, Ports: []model.PortClaim{{Network: "tcp4", Address: "127.0.0.1", Mode: "cooperative"}}}
	if err := client.Call(ctx, home, "acquire", r, &lease); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(lease.Ports[0].Address, strconv.Itoa(lease.Ports[0].Port)))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var report model.ShutdownReport
	if err = client.Call(ctx, home, "shutdown", model.ShutdownRequest{Force: true}, &report); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err = client.Call(ctx, home, "shutdown_status", nil, &report); err != nil {
			t.Fatal(err)
		}
		if report.Phase == "blocked" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if report.Phase != "blocked" || len(report.Remaining) != 1 {
		t.Fatalf("occupied port silently reclaimed: %+v", report)
	}
	var status model.Status
	if err = client.Call(ctx, home, "status", nil, &status); err != nil || !status.Draining {
		t.Fatalf("inspection unavailable: %+v %v", status, err)
	}
	if err = client.Call(ctx, home, "message", map[string]string{"audience": "operator", "body": "still available while drain is blocked"}, nil); err != nil {
		t.Fatal("operator communication unavailable", err)
	}
	if err = client.Call(ctx, home, "renew", map[string]any{"id": lease.ID, "ttl_ns": int64(2 * time.Minute)}, nil); err == nil {
		t.Fatal("renewal extended shutdown")
	}
	listener.Close()
	if err = client.Call(ctx, home, "reconcile", map[string]string{"id": lease.ID}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownWorkerContinuesAfterInitialReportingFailure(t *testing.T) {
	home := backend(t)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	var active, queued model.Lease
	request := model.Request{Reason: "initial reporting failure", Resources: model.Resources{CPU: 1}, TTL: time.Minute}
	if err := client.Call(ctx, home, "acquire", request, &active); err != nil {
		t.Fatal(err)
	}
	request.Queue = true
	request.Key = "queued-initial-failure"
	if err := client.Call(ctx, home, "acquire", request, &queued); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER reject_initial_report BEFORE INSERT ON events WHEN NEW.type='shutdown.queue_cancelled' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err = client.Call(ctx, home, "shutdown", model.ShutdownRequest{Wait: true, Timeout: 150 * time.Millisecond}, nil); err == nil {
		t.Fatal("reporting failure was hidden")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(home, "endpoint.json")); os.IsNotExist(err) {
			var count int
			if err = db.QueryRow(`SELECT count(*) FROM events WHERE type='shutdown.initialization_incomplete'`).Scan(&count); err != nil || count != 1 {
				t.Fatal("missing durable initialization failure", err)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("partial initialization error stranded backend-owned drain")
}
