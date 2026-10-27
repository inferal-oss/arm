package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/inferal-oss/arm/internal/server"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/presence"
)

func peerReport(t *testing.T, home, host string) model.PeerOverview {
	return peerReportMatching(t, home, host, func(*model.PeerSnapshot) bool { return true })
}
func peerReportMatching(t *testing.T, home, host string, match func(*model.PeerSnapshot) bool) model.PeerOverview {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var report model.PeerOverview
		err := client.Call(ctx, home, "peers_status", nil, &report)
		cancel()
		if err == nil {
			for _, item := range report.Instances {
				if item.Host == host && item.Snapshot != nil && match(item.Snapshot) {
					return report
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("peer snapshot unavailable")
	return model.PeerOverview{}
}
func TestLocalPeerHeldQueueExpiryAndOptOut(t *testing.T) {
	source := backend(t)
	viewer := backend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var sourceStatus model.Status
	if err := client.Call(ctx, source, "status", nil, &sourceStatus); err != nil {
		t.Fatal(err)
	}
	var held, queued model.Lease
	if err := client.Call(ctx, source, "acquire", model.Request{Reason: "private held reason", Resources: model.Resources{Memory: 700}, TTL: 2 * time.Minute}, &held); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, source, "acquire", model.Request{Reason: "private queued reason", Resources: model.Resources{Memory: 600}, TTL: time.Minute, Queue: true}, &queued); err != nil {
		t.Fatal(err)
	}
	if held.State != "active" || queued.State != "queued" {
		t.Fatalf("unexpected leases: %s %s", held.State, queued.State)
	}
	report := peerReportMatching(t, viewer, sourceStatus.Host, func(s *model.PeerSnapshot) bool { return s.Held.Memory == 700 && s.Queued.Memory == 600 })
	var found *model.PeerSnapshot
	for _, item := range report.Instances {
		if item.Host == sourceStatus.Host {
			found = item.Snapshot
		}
	}
	if found == nil || found.Held.Memory != 700 || found.Queued.Memory != 600 || found.HeldCount != 1 || found.QueuedCount != 1 || len(found.Items) != 2 {
		t.Fatalf("wrong peer picture: %+v", found)
	}
	if found.Items[0].Expires == nil || !found.Items[0].Expires.Equal(*held.Expires) || found.Items[1].QueuePosition != 1 || found.Items[1].RequestedTTL != time.Minute {
		t.Fatalf("missing expiry or queue timing: %+v", found.Items)
	}
	raw, _ := json.Marshal(report)
	for _, private := range []string{held.ID, queued.ID, "private held reason", "private queued reason"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("leaked private metadata %q", private)
		}
	}
	ep, err := local.ReadEndpoint(source)
	if err != nil {
		t.Fatal(err)
	}
	peerURL := strings.Replace(ep.URL, "/v1/ws", "/v1/peers", 1)
	c, response, err := websocket.Dial(ctx, peerURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{"http://unrelated.invalid"}}})
	if c != nil {
		c.CloseNow()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("browser origin accepted: %v %+v", err, response)
	}
	if err = os.WriteFile(filepath.Join(source, "presence-disabled"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, response, err = websocket.Dial(ctx, peerURL, nil)
	if err == nil || response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("opted-out endpoint accepted: %v %+v", err, response)
	}
	d, err := presence.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	removed := false
	for until := time.Now().Add(7 * time.Second); time.Now().Before(until); time.Sleep(50 * time.Millisecond) {
		found := false
		for _, candidate := range d.Scan().Candidates {
			if candidate.Record.Host == sourceStatus.Host {
				found = true
			}
		}
		if !found {
			removed = true
			break
		}
	}
	if !removed {
		t.Fatal("opted-out announcement remains")
	}
}

func TestPeerWebSocketRejectsUnprovedPublisher(t *testing.T) {
	home := backend(t)
	ep, err := local.ReadEndpoint(home)
	if err != nil {
		t.Fatal(err)
	}
	d, err := presence.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	fake, err := presence.Publish(d, strings.Replace(ep.URL, "/v1/ws", "/v1/peers", 1), strings.Repeat("a", 32), strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer fake.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(ep.URL, "/v1/ws", "/v1/peers", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var challenge map[string]json.RawMessage
	if err = wsjson.Read(ctx, c, &challenge); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Write(ctx, c, map[string]any{"record": fake.Name, "nonce": make([]byte, 32), "signature": make([]byte, 64)}); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err = wsjson.Read(ctx, c, &reply); err == nil {
		t.Fatalf("unproved publisher received peer summary: %+v", reply)
	}
}

func TestPeerServeHelperProcess(t *testing.T) {
	if os.Getenv("ARM_PEER_SERVE_HELPER") != "1" {
		return
	}
	home := os.Getenv("ARM_PEER_SERVE_HOME")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1024, Disk: 2048}); err != nil {
		t.Fatal(err)
	}
}
func TestLocalPresenceAcrossIndependentProcesses(t *testing.T) {
	source := filepath.Join(t.TempDir(), "child-arm")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPeerServeHelperProcess$", "-test.v")
	cmd.Env = append(os.Environ(), "ARM_PEER_SERVE_HELPER=1", "ARM_PEER_SERVE_HOME="+source)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("child backend: %v: %s", err, output.String())
			}
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
			t.Error("child backend did not stop")
		}
	})
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(source, "endpoint.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("child backend unavailable: %s", output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	viewer := backend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var status model.Status
	if err := client.Call(ctx, source, "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	report := peerReport(t, viewer, status.Host)
	matched := false
	for _, entry := range report.Instances {
		if entry.Host == status.Host && entry.Snapshot != nil && entry.UID == uint32(os.Geteuid()) {
			matched = true
		}
	}
	if !matched {
		t.Fatalf("child process not authenticated in report: %+v", report.Instances)
	}
}
