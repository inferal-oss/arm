package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/cli"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/server"
)

func TestCLIPortReservationsActivationAndProtectedBackend(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1}) }()
	defer func() {
		cancel()
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
			t.Fatal("backend not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	call := func(args ...string) []byte {
		t.Helper()
		var out, stderr bytes.Buffer
		code := cli.Main(append([]string{"--home", home}, args...), bytes.NewReader(nil), &out, &stderr)
		if code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out.String(), stderr.String())
		}
		return out.Bytes()
	}
	var lease model.Lease
	for _, args := range [][]string{
		{"ports", "reserve", "-reason", "testing"},
		{"ports", "reserve", "0", "--reason", "positional port"},
		{"ports", "reserve", "--reason", "positional port", "0"},
		{"ports", "reserve", "--port", "web=tcp4://127.0.0.1:0", "--reason", "explicit port"},
	} {
		var port model.Lease
		if err := json.Unmarshal(call(args...), &port); err != nil {
			t.Fatal(err)
		}
		if len(port.Ports) != 1 || port.Ports[0].Port == 0 || port.Ports[0].Address != "127.0.0.1" || port.Request.Resources != (model.Resources{}) {
			t.Fatalf("default/explicit port: %+v", port)
		}
		call("release", "--json", port.ID)
	}
	if err := json.Unmarshal(call("ports", "reserve", "--port", "web=tcp4://127.0.0.1:0", "--reason", "reserve web fixture", "--ttl", "1m"), &lease); err != nil {
		t.Fatal(err)
	}
	defer call("release", lease.ID)
	if lease.Request.Resources != (model.Resources{}) || lease.Ports[0].State != "held" || lease.Ports[0].Port == 0 {
		t.Fatalf("port-only reservation: %+v", lease)
	}
	var rows []model.ResourceView
	if err := json.Unmarshal(call("ports", "list"), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("port and infrastructure rows: %+v", rows)
	}
	for _, r := range rows {
		if r.Accounting == "protected-infrastructure" && r.Users[0].Lease != "" {
			t.Fatal("backend can be reclaimed as a workload lease")
		}
	}
	var inventory model.PortInventory
	if err := json.Unmarshal(call("ports", "discover"), &inventory); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range inventory.Sockets {
		if s.Network == "tcp4" && s.Port == lease.Ports[0].Port {
			found = true
		}
	}
	if !found {
		t.Fatalf("native inventory omitted reserved listener: %+v", inventory)
	}
	var run model.Lease
	if err := json.Unmarshal(call("run", "--port-lease", lease.ID, "--ttl", "10s", "--reason", "consume reserved socket", "--json", "--", "/bin/sh", "-c", `test -n "$ARM_PORTS" && test -n "$ARM_PORT_LEASE"`), &run); err != nil {
		t.Fatal(err)
	}
	if run.State != "terminal" || len(run.Ports) != 0 {
		t.Fatalf("attachment double-charged or failed: %+v", run)
	}
	if err := json.Unmarshal(call("run", "--port", "dns=udp4://127.0.0.1:0", "--reason", "short datagram fixture", "--json", "--", "/bin/sh", "-c", ":"), &run); err != nil {
		t.Fatal(err)
	}
	if run.State != "terminal" || run.Ports[0].State != "observed_available" {
		t.Fatalf("run port not reconciled: %+v", run)
	}
}
