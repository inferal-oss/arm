package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func TestReviewPeerRetiredCompletionCannotClearSuccessor(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 1
	calls := 0
	s.reply = func(op string, _ any, result any) error {
		if op != "peers_status" {
			t.Fatalf("peer panel requested unexpected operation %q", op)
		}
		calls++
		*result.(*model.PeerOverview) = model.PeerOverview{PeerHeldCount: calls, TotalsComplete: true}
		return nil
	}
	first := m.readPeers(false)().(peerReadMsg)
	m.Update(tea.KeyPressMsg{Code: '3'})
	m.Update(tea.KeyPressMsg{Code: '2'})
	if m.readPeers(true) != nil || !m.peers.busy {
		t.Fatal("retired original still running but successor overlapped it")
	}
	_, second := m.Update(first)
	if second == nil || !m.peers.busy || m.peers.report != nil {
		t.Fatal("retired response was accepted or did not schedule its visible successor")
	}
	pending := m.peers.pending
	_, duplicate := m.Update(first)
	if duplicate != nil || !m.peers.busy || m.peers.pending != pending {
		t.Fatal("duplicate old completion cleared/replaced successor ownership")
	}
	m.Update(second())
	if calls != 2 || m.peers.report == nil || m.peers.report.PeerHeldCount != 2 || m.peers.busy {
		t.Fatal("successor result lost", calls, m.peers)
	}
}

func TestReviewPeerRemoteAndModalScopesNeverPollLocalPeers(t *testing.T) {
	cases := map[string]func(*dashboardModel){
		"remote-selected": func(m *dashboardModel) {
			m.nodeUI = &nodeDashboard{selection: model.NodeSelection{Host: "remote", Source: "local"}}
		},
		"native-resource-detail": func(m *dashboardModel) { m.resourceID = "memory" },
		"pool-controls":          func(m *dashboardModel) { m.poolUI = &poolDashboard{} },
		"feed-focus":             func(m *dashboardModel) { m.feedFocus = true },
		"input":                  func(m *dashboardModel) { m.inputMode = "search" },
		"hidden-view":            func(m *dashboardModel) { m.view = 2 },
	}
	for name, hide := range cases {
		t.Run(name, func(t *testing.T) {
			m, _ := fixtureDashboard(t)
			m.view = 1
			cmd := m.readPeers(false)
			hide(m)
			m.retirePeerIfHidden()
			if m.peers.cancel != nil || m.peerOpen || m.readPeers(true) != nil {
				t.Fatal("hidden/remote context retained a local peer poll")
			}
			m.Update(cmd())
			if m.peers.report != nil || m.peers.busy {
				t.Fatal("late local report entered hidden/remote context")
			}
		})
	}
}

func TestReviewPeerDetailItemBudgetAndTerminalGeometry(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.view = 1
	now := time.Now()
	r := peerFixture(now)
	r.Local.Items = make([]model.PeerItem, 128)
	r.Local.HeldCount = 128
	for i := range r.Local.Items {
		r.Local.Items[i] = model.PeerItem{State: "review-item-local", Resources: model.Resources{Memory: 1}}
	}
	r.Instances = nil
	for i := 0; i < 32; i++ {
		snapshot := r.Local
		snapshot.Items = append([]model.PeerItem(nil), r.Local.Items...)
		for j := range snapshot.Items {
			snapshot.Items[j].State = fmt.Sprintf("review-item-%02d", i)
		}
		r.Instances = append(r.Instances, model.PeerInstance{UID: uint32(i), Host: "peer\u202e\x1b]52;unsafe\a", URL: "ws://127.0.0.1:1", Snapshot: &snapshot})
	}
	m.peers.report = &r
	m.peers.received = now
	m.timeNow = now
	m.peerOpen = true
	details := m.peerDetailsText()
	if count := strings.Count(details, "review-item-"); count != 128 {
		t.Fatalf("itemized render escaped aggregate budget: %d", count)
	}
	if strings.Contains(details, "\x1b") || strings.Contains(details, "\u202e") {
		t.Fatal("terminal or bidi controls reached peer detail")
	}
	if !strings.Contains(details, "Itemized view truncated") {
		t.Fatal("bounded detail concealed omitted items")
	}
	for _, width := range []int{20, 45, 70, 140} {
		m.width, m.height = width, 24
		m.syncViewport()
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		if len(lines) != 24 {
			t.Fatalf("%d-column detail escaped height: %d", width, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > width {
				t.Fatalf("%d-column detail escaped width: %q", width, line)
			}
		}
	}
}
