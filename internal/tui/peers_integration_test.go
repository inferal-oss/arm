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

func peerBackend(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1024, Disk: 2048})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("peer fixture backend did not stop")
		}
	})
	until := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
			return home
		}
		if time.Now().After(until) {
			t.Fatal("peer fixture backend unavailable")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestCharmPeerResourcesReadsRealAdvisorySnapshot(t *testing.T) {
	source, viewer, bystander := peerBackend(t), peerBackend(t), peerBackend(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var sourceStatus, viewerStatus, bystanderStatus model.Status
	if err := client.Call(ctx, source, "status", nil, &sourceStatus); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, viewer, "status", nil, &viewerStatus); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, bystander, "status", nil, &bystanderStatus); err != nil {
		t.Fatal(err)
	}
	var unrelated model.Lease
	if err := client.Call(ctx, bystander, "acquire", model.Request{Reason: "private unrelated participant fixture", Resources: model.Resources{Memory: 13}, TTL: 2 * time.Minute}, &unrelated); err != nil {
		t.Fatal(err)
	}
	var held, queued model.Lease
	if err := client.Call(ctx, source, "acquire", model.Request{Reason: "private held fixture", Resources: model.Resources{Memory: 700}, TTL: 2 * time.Minute}, &held); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, source, "acquire", model.Request{Reason: "private queued fixture", Resources: model.Resources{Memory: 600}, TTL: time.Minute, Queue: true}, &queued); err != nil {
		t.Fatal(err)
	}
	if held.State != "active" || queued.State != "queued" {
		t.Fatalf("expected held and queued fixture, got %s and %s", held.State, queued.State)
	}
	session, err := client.OpenSession(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	m := newDashboard(ctx, viewer)
	m.session, m.connected, m.view = session, true, 1
	m.data.status = viewerStatus
	until := time.Now().Add(9 * time.Second)
	for {
		cmd := m.readPeers(true)
		if cmd == nil {
			t.Fatal("read-only peer query not scheduled")
		}
		msg := cmd().(peerReadMsg)
		m.acceptPeerRead(msg)
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		found, foundBystander := false, false
		for _, instance := range m.peers.report.Instances {
			if instance.Host == sourceStatus.Host && instance.Snapshot != nil && instance.Snapshot.Held.Memory == 700 && instance.Snapshot.Queued.Memory == 600 {
				found = true
			}
			if instance.Host == bystanderStatus.Host && instance.Snapshot != nil && instance.Snapshot.Held.Memory == 13 {
				foundBystander = true
			}
		}
		if found && foundBystander {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("source held/queued snapshot did not refresh: %+v", m.peers.report)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Discovery intentionally shares /tmp with other participating daemons.
	// Check this original fixture exactly, then verify global aggregates against
	// every accepted report rather than assuming our fixture is the only peer.
	var fixture *model.PeerSnapshot
	var allHeld, allQueued model.Resources
	heldCount, queuedCount := 0, 0
	for _, instance := range m.peers.report.Instances {
		if instance.Snapshot == nil {
			continue
		}
		snapshot := instance.Snapshot
		if instance.Host == sourceStatus.Host {
			fixture = snapshot
		}
		allHeld = allHeld.Add(snapshot.Held)
		allQueued = allQueued.Add(snapshot.Queued)
		heldCount += snapshot.HeldCount
		queuedCount += snapshot.QueuedCount
	}
	if fixture == nil || fixture.Host != sourceStatus.Host || fixture.Held.Memory != 700 || fixture.Queued.Memory != 600 || fixture.HeldCount != 1 || fixture.QueuedCount != 1 {
		t.Fatalf("wrong original fixture snapshot: %+v", fixture)
	}
	if m.peers.report.PeerHeld != allHeld || m.peers.report.PeerQueued != allQueued || m.peers.report.PeerHeldCount != heldCount || m.peers.report.PeerQueuedCount != queuedCount {
		t.Fatalf("global totals differ from all accepted reports: %+v; expected held=%+v (%d), queued=%+v (%d)", m.peers.report, allHeld, heldCount, allQueued, queuedCount)
	}
	details := m.peerDetailsText()
	for _, want := range []string{"700 B RAM", "600 B RAM", "queue #1", "requested for 1m0s", "expires"} {
		if !strings.Contains(details, want) {
			t.Fatalf("missing %q from terminal peer view: %s", want, details)
		}
	}
	for _, private := range []string{held.ID, queued.ID, held.Request.Reason, queued.Request.Reason, unrelated.ID, unrelated.Request.Reason} {
		if strings.Contains(details, private) {
			t.Fatalf("private lease metadata rendered to peer view: %q", private)
		}
	}
	if m.data.status.Host != viewerStatus.Host || m.data.status.Reserved.Memory != viewerStatus.Reserved.Memory {
		t.Fatal("peer data changed native local resource budgets")
	}
}
