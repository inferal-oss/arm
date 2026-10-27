package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/model"
)

func peerFixture(now time.Time) model.PeerOverview {
	expiry := now.Add(2 * time.Minute)
	estimate := now.Add(time.Minute)
	return model.PeerOverview{
		At: now, TotalsComplete: true,
		Local:           model.PeerSnapshot{Version: 1, Host: "local", Held: model.Resources{CPU: .5}, HeldCount: 1, Items: []model.PeerItem{{State: "active", Resources: model.Resources{CPU: .5}, Expires: &expiry}}},
		PeerHeld:        model.Resources{Memory: 700},
		PeerQueued:      model.Resources{Memory: 600},
		PeerHeldCount:   1,
		PeerQueuedCount: 1,
		Instances:       []model.PeerInstance{{UID: 65534, Host: "peer\x1b]52;unsafe\a", URL: "ws://127.0.0.1:48100/v1/peers", Snapshot: &model.PeerSnapshot{Version: 1, Host: "peer", Held: model.Resources{Memory: 700}, Queued: model.Resources{Memory: 600}, HeldCount: 1, QueuedCount: 1, Items: []model.PeerItem{{State: "active", Resources: model.Resources{Memory: 700}, Expires: &expiry}, {State: "queued", Resources: model.Resources{Memory: 600}, QueuePosition: 1, RequestedTTL: time.Minute, EstimatedStart: &estimate, EstimateKind: "earliest\x1b[31m", StartDeadline: &expiry}}}}},
		Advisory:        "Peer reports are advisory.",
	}
}

func TestPeerResourcesSummaryDetailsAndNarrowLayout(t *testing.T) {
	m, _ := fixtureDashboard(t)
	now := time.Now()
	m.view = 1
	m.data.status.Resources = []model.ResourceView{{ID: "memory", Kind: "memory", Unit: "bytes", BudgetTotal: "1024", Reserved: "0", Available: "1024"}}
	m.peers.report = new(model.PeerOverview)
	*m.peers.report = peerFixture(now)
	m.peers.received = now
	m.timeNow = now
	summary := ansi.Strip(m.resourcesText())
	for _, want := range []string{"NEARBY ARM", "Peer held (1): 700 B RAM", "Peer queued (1): 600 B RAM", "MEMORY · LIVE BUDGET"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
	for _, width := range []int{45, 70, 140} {
		m.width, m.height = width, 24
		m.syncViewport()
		screen := ansi.Strip(m.View().Content)
		lines := strings.Split(screen, "\n")
		if len(lines) != m.height {
			t.Fatalf("%d-col screen escaped height: %d", width, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Fatalf("%d-col screen escaped width: %q", width, line)
			}
		}
		if width == 45 && !strings.Contains(screen, "MEMORY") {
			t.Fatal("narrow layout hid native budget behind peer summary")
		}
	}
	press(m, "v")
	if !m.peerOpen {
		t.Fatal("peer details did not open")
	}
	details := ansi.Strip(m.peerDetailsText())
	for _, want := range []string{"700 B RAM", "600 B RAM", "queue #1", "requested for 1m0s", "estimated start", "expires", "start by", `\u001b]52`, `\u001b[31m`} {
		if !strings.Contains(details, want) {
			t.Fatalf("details missing %q: %s", want, details)
		}
	}
	if strings.Contains(details, "\x1b") {
		t.Fatal("peer terminal control reached renderer")
	}
	press(m, "esc")
	if m.peerOpen {
		t.Fatal("Esc did not return to budgets")
	}
	m.nodeUI = &nodeDashboard{selection: model.NodeSelection{Host: "remote", Source: "local"}}
	if m.readPeers(true) != nil || strings.Contains(m.bodyText(), "NEARBY ARM") {
		t.Fatal("local peer view leaked into remote-selected workspace")
	}
}

func TestPeerReadCadenceRetirementAndSessionReplacement(t *testing.T) {
	m, first := fixtureDashboard(t)
	m.view = 1
	first.reply = func(op string, _, result any) error {
		if op == "peers_status" {
			*result.(*model.PeerOverview) = peerFixture(time.Now())
		}
		return nil
	}
	cmd := m.readPeers(false)
	if cmd == nil || m.readPeers(false) != nil {
		t.Fatal("overlapping peer read allowed")
	}
	msg := cmd().(peerReadMsg)
	m.acceptPeerRead(msg)
	if m.peers.report == nil || m.readPeers(false) != nil {
		t.Fatal("snapshot missing or cadence ignored")
	}
	if m.readPeers(true) == nil {
		t.Fatal("explicit refresh did not bypass cadence")
	}
	// The unexecuted explicit read is retired when Resources is hidden.
	m.Update(tea.KeyPressMsg{Code: '3'})
	if m.peers.cancel != nil || m.peerOpen {
		t.Fatal("hidden peer read retained")
	}
	if m.acceptPeerRead(peerReadMsg{ui: m.peers, session: first, pending: m.peers.pending, generation: m.peers.generation - 1, report: model.PeerOverview{PeerHeldCount: 999}}) != nil || m.peers.report.PeerHeldCount == 999 {
		t.Fatal("retired result replaced visible snapshot")
	}
	old := m.peers
	m.Update(connectedMsg{session: newTestTransport(), data: snapshot{status: model.Status{Host: "replacement"}}})
	if m.peers == old || m.peers.report != nil {
		t.Fatal("old peer snapshot survived session replacement")
	}
	m.Update(peerReadMsg{ui: old, session: first, pending: old.pending, report: model.PeerOverview{PeerHeldCount: 999}})
	if m.peers.report != nil {
		t.Fatal("old-session result reached replacement session")
	}
}

type blockingPeerTransport struct {
	*testTransport
	started chan struct{}
}

func (s *blockingPeerTransport) Call(ctx context.Context, op string, _, _ any) error {
	if op != "peers_status" {
		return errors.New("unexpected operation")
	}
	close(s.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestPeerReadCancellationOnHide(t *testing.T) {
	m, _ := fixtureDashboard(t)
	blocked := &blockingPeerTransport{testTransport: newTestTransport(), started: make(chan struct{})}
	m.session = blocked
	m.view = 1
	cmd := m.readPeers(false)
	if cmd == nil {
		t.Fatal("peer query was not scheduled")
	}
	done := make(chan peerReadMsg, 1)
	go func() { done <- cmd().(peerReadMsg) }()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("peer query did not start")
	}
	m.Update(tea.KeyPressMsg{Code: '2'}) // Same Resources view keeps the read.
	if !m.peers.busy {
		t.Fatal("visible read was canceled")
	}
	m.Update(tea.KeyPressMsg{Code: '3'})
	select {
	case msg := <-done:
		if !errors.Is(msg.err, context.Canceled) {
			t.Fatalf("hidden read returned %v", msg.err)
		}
		m.Update(msg)
	case <-time.After(time.Second):
		t.Fatal("hidden read was not canceled")
	}
	if m.peers.busy || m.peers.report != nil {
		t.Fatal("retired peer result was retained")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: '2'})
	if cmd == nil || !m.peers.busy {
		t.Fatal("returning to Resources did not start a fresh read")
	}
	m.retirePeerRead()
}

func TestPeerQuitFencesQueuedCompletion(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{{Code: 'q'}, {Code: 'c', Mod: tea.ModCtrl}} {
		t.Run(key.String(), func(t *testing.T) {
			m, _ := fixtureDashboard(t)
			m.view = 1
			cmd := m.readPeers(false)
			if cmd == nil {
				t.Fatal("initial peer read was not scheduled")
			}
			// Completion is already queued when Quit is handled by the dashboard.
			msg := cmd().(peerReadMsg)
			m.Update(key)
			if !m.peerStopped || m.peers.cancel != nil || m.peerVisible() {
				t.Fatal("quit left peer admission open")
			}
			_, next := m.Update(msg)
			if next != nil || m.peers.busy || m.peers.report != nil || m.readPeers(true) != nil {
				t.Fatal("queued completion scheduled or accepted peer work after quit")
			}
		})
	}
}

func TestPeerPartialStaleAndTruncatedLabels(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.view = 1
	if strings.Contains(m.peerSummaryText(), "Peer held (0)") {
		t.Fatal("unavailable report rendered as zero")
	}
	now := time.Now()
	r := peerFixture(now)
	r.TotalsComplete = false
	r.DiscoveryTruncated = true
	r.Instances[0].Snapshot.Truncated = true
	m.peers.report = &r
	m.peers.received = now
	m.timeNow = now
	if m.peerStateLabel() != "PARTIAL" || !strings.Contains(m.peerDetailsText(), "Itemized view truncated") {
		t.Fatal("partial or itemization gap hidden")
	}
	if !strings.Contains(ansi.Strip(m.peerSummaryText()), "ITEMS TRUNCATED") {
		t.Fatal("summary hid itemized truncation")
	}
	m.timeNow = now.Add(peerStaleAfter + time.Second)
	if m.peerStateLabel() != "STALE" {
		t.Fatal("held peer data did not age to stale")
	}
	m.peers.err = "broken\x1b[2J"
	summary := ansi.Strip(m.peerSummaryText())
	if !strings.Contains(summary, `\u001b[2J`) || strings.Contains(summary, "\x1b") {
		t.Fatal("untrusted error was not rendered safely")
	}
}
