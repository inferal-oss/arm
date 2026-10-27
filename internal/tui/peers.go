package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/inferal-oss/arm/internal/model"
)

const peerReadInterval = 10 * time.Second
const peerStaleAfter = 20 * time.Second

// Nearby instances provide advisory promises only. This state never feeds
// local admission or the native resource budget cards.
type peerDashboard struct {
	report         *model.PeerOverview
	err            string
	received       time.Time
	lastAttempt    time.Time
	generation     uint64
	pending        uint64
	pendingSession transport
	busy           bool
	cancel         context.CancelFunc
}

type peerReadMsg struct {
	ui         *peerDashboard
	session    transport
	pending    uint64
	generation uint64
	report     model.PeerOverview
	err        error
}

func (m *dashboardModel) peerVisible() bool {
	return !m.peerStopped && m.view == 1 && m.nodeUI == nil && m.connected && m.session != nil &&
		m.resourceID == "" && m.poolUI == nil && m.detail == nil && m.record == nil &&
		m.recovery == nil && m.inputMode == "" && m.checkins == nil && m.review == nil &&
		m.composer == nil && !m.feedFocus
}

func (m *dashboardModel) stopPeerReads() {
	m.peerStopped = true
	m.retirePeerRead()
}

func (m *dashboardModel) retirePeerRead() {
	if m.peers == nil {
		return
	}
	if m.peers.cancel != nil {
		m.peers.cancel()
		m.peers.cancel = nil
		m.peers.generation++
		m.peers.lastAttempt = time.Time{}
	}
}

func (m *dashboardModel) retirePeerIfHidden() {
	if !m.peerVisible() {
		m.retirePeerRead()
		m.peerOpen = false
	}
}

func (m *dashboardModel) readPeers(force bool) tea.Cmd {
	if !m.peerVisible() {
		return nil
	}
	if m.peers == nil {
		m.peers = &peerDashboard{}
	}
	ui := m.peers
	if ui.busy || (!force && !ui.lastAttempt.IsZero() && time.Since(ui.lastAttempt) < peerReadInterval) {
		return nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, 12*time.Second)
	ui.cancel = cancel
	ui.busy = true
	ui.pending++
	ui.pendingSession = m.session
	ui.lastAttempt = time.Now()
	session, pending, generation := m.session, ui.pending, ui.generation
	return func() tea.Msg {
		defer cancel()
		msg := peerReadMsg{ui: ui, session: session, pending: pending, generation: generation}
		msg.err = session.Call(ctx, "peers_status", nil, &msg.report)
		return msg
	}
}

func (m *dashboardModel) acceptPeerRead(msg peerReadMsg) tea.Cmd {
	ui := m.peers
	if ui == nil || msg.ui != ui || !ui.busy || msg.pending != ui.pending || msg.session != ui.pendingSession {
		return nil
	}
	ui.busy = false
	ui.cancel = nil
	ui.pendingSession = nil
	if msg.generation != ui.generation || msg.session != m.session || !m.peerVisible() {
		if m.peerVisible() && msg.session == m.session {
			return m.readPeers(true)
		}
		return nil
	}
	if msg.err != nil {
		ui.err = msg.err.Error()
		return nil
	}
	ui.report = &msg.report
	ui.received = time.Now()
	ui.err = ""
	return nil
}

func peerQuantity(r model.Resources) string {
	parts := make([]string, 0, 3)
	if r.CPU != 0 {
		parts = append(parts, fmt.Sprintf("%.3g CPU", r.CPU))
	}
	if r.Memory != 0 {
		parts = append(parts, byteCount(fmt.Sprint(r.Memory))+" RAM")
	}
	if r.Disk != 0 {
		parts = append(parts, byteCount(fmt.Sprint(r.Disk))+" disk")
	}
	if len(parts) == 0 {
		return "0 scalar resources"
	}
	return strings.Join(parts, " · ")
}

func peerItemsTruncated(r model.PeerOverview) bool {
	if r.Local.Truncated || len(r.Local.Items) < r.Local.HeldCount+r.Local.QueuedCount {
		return true
	}
	for _, instance := range r.Instances {
		if s := instance.Snapshot; s != nil && (s.Truncated || len(s.Items) < s.HeldCount+s.QueuedCount) {
			return true
		}
	}
	return false
}

func (m *dashboardModel) peerAge() time.Duration {
	if m.peers == nil || m.peers.received.IsZero() {
		return 0
	}
	now := m.timeNow
	if now.IsZero() {
		now = time.Now()
	}
	return max(time.Duration(0), now.Sub(m.peers.received))
}

func (m *dashboardModel) peerStateLabel() string {
	ui := m.peers
	if ui == nil || ui.report == nil {
		if ui != nil && ui.err != "" {
			return "UNAVAILABLE"
		}
		return "CHECKING"
	}
	if !m.connected || ui.err != "" || m.peerAge() > peerStaleAfter {
		return "STALE"
	}
	if !ui.report.TotalsComplete || ui.report.DiscoveryTruncated || ui.report.DiscoveryUnavailable != "" {
		return "PARTIAL"
	}
	return "RECENT"
}

func (m *dashboardModel) peerSummaryText() string {
	width := max(12, m.mainWidth()-4)
	ui := m.peers
	lines := []string{m.peerStateLabel() + " · peer-reported promises are advisory"}
	if ui == nil || ui.report == nil {
		if ui != nil && ui.err != "" {
			lines = append(lines, "Peer read unavailable: "+safe(ui.err))
		} else {
			lines = append(lines, "Checking local instance announcements…")
		}
	} else {
		r := ui.report
		verified, unreachable := 0, 0
		for _, instance := range r.Instances {
			if instance.Snapshot == nil {
				unreachable++
			} else {
				verified++
			}
		}
		lines = append(lines, fmt.Sprintf("%d verified · %d unavailable · %ds old", verified, unreachable, int(m.peerAge().Seconds())))
		lines = append(lines, fmt.Sprintf("Local held (%d): %s", r.Local.HeldCount, peerQuantity(r.Local.Held)))
		lines = append(lines, fmt.Sprintf("Local queued (%d): %s", r.Local.QueuedCount, peerQuantity(r.Local.Queued)))
		lines = append(lines, fmt.Sprintf("Peer held (%d): %s", r.PeerHeldCount, peerQuantity(r.PeerHeld)))
		lines = append(lines, fmt.Sprintf("Peer queued (%d): %s", r.PeerQueuedCount, peerQuantity(r.PeerQueued)))
		if r.DiscoveryTruncated {
			lines = append(lines, "PARTIAL · announcement scan truncated")
		}
		if r.DiscoveryUnavailable != "" {
			lines = append(lines, "Discovery unavailable: "+safe(r.DiscoveryUnavailable))
		}
		if peerItemsTruncated(*r) {
			lines = append(lines, "ITEMS TRUNCATED · totals include all reported claims")
		}
		if ui.err != "" {
			lines = append(lines, "STALE · refresh failed: "+safe(ui.err))
		}
	}
	lines = append(lines, "[v] Inspect queues and lease expiries · separate from local budgets")
	return window("NEARBY ARM INSTANCES · ADVISORY", strings.Join(lines, "\n"), width, 0, false, "blue")
}

func peerItemText(item model.PeerItem) string {
	line := safe(item.State) + " · " + peerQuantity(item.Resources)
	if item.OtherClaims {
		line += " · other claims omitted"
	}
	if item.State == "queued" {
		if item.QueuePosition > 0 {
			line += fmt.Sprintf(" · queue #%d", item.QueuePosition)
		} else {
			line += " · queue position unknown"
		}
		if item.RequestedTTL > 0 {
			line += " · requested for " + item.RequestedTTL.String()
		}
		if item.EstimatedStart != nil {
			kind := item.EstimateKind
			if kind == "" {
				kind = "advisory"
			}
			line += " · " + safe(kind) + " estimated start " + date(item.EstimatedStart)
		} else if item.EstimateKind != "" {
			line += " · estimate " + safe(item.EstimateKind)
		} else {
			line += " · start estimate unavailable"
		}
		if item.StartDeadline != nil {
			line += " · start by " + date(item.StartDeadline)
		}
	} else if item.Expires != nil {
		line += " · expires " + date(item.Expires)
	} else {
		line += " · expiry unknown"
	}
	return line
}

func appendPeerItems(lines *[]string, snapshot model.PeerSnapshot, perPeer int, remaining *int) {
	shown := min(len(snapshot.Items), perPeer, *remaining)
	for _, item := range snapshot.Items[:shown] {
		*lines = append(*lines, "  "+peerItemText(item))
	}
	*remaining -= shown
	if shown < len(snapshot.Items) || snapshot.Truncated || len(snapshot.Items) < snapshot.HeldCount+snapshot.QueuedCount {
		*lines = append(*lines, "  Itemized view truncated; reported totals include all claims. Use arm peers --json for more.")
	}
}

func (m *dashboardModel) peerDetailsText() string {
	ui := m.peers
	lines := []string{m.peerStateLabel() + " · nearby instance reports", "Read-only promises; local admission uses the budget cards."}
	if ui == nil || ui.report == nil {
		if ui != nil && ui.err != "" {
			lines = append(lines, "Peer read unavailable: "+safe(ui.err))
		} else {
			lines = append(lines, "Checking local instance announcements…")
		}
		return strings.Join(lines, "\n")
	}
	r := ui.report
	if ui.err != "" {
		lines = append(lines, "STALE · refresh failed: "+safe(ui.err))
	}
	if r.DiscoveryUnavailable != "" {
		lines = append(lines, "PARTIAL · discovery unavailable: "+safe(r.DiscoveryUnavailable))
	}
	if r.DiscoveryTruncated {
		lines = append(lines, "PARTIAL · announcement scan truncated")
	}
	if !r.TotalsComplete {
		lines = append(lines, "PARTIAL · peer totals omit unavailable reports")
	}
	lines = append(lines, "", fmt.Sprintf("LOCAL · held %s (%d) · queued %s (%d)", peerQuantity(r.Local.Held), r.Local.HeldCount, peerQuantity(r.Local.Queued), r.Local.QueuedCount))
	remaining := 128
	appendPeerItems(&lines, r.Local, 16, &remaining)
	lines = append(lines, "", fmt.Sprintf("DISCOVERED · held %s (%d) · queued %s (%d)", peerQuantity(r.PeerHeld), r.PeerHeldCount, peerQuantity(r.PeerQueued), r.PeerQueuedCount))
	if len(r.Instances) == 0 {
		lines = append(lines, "No other current announcements found.")
	}
	for _, instance := range r.Instances {
		lines = append(lines, "", fmt.Sprintf("UID %d · %s · %s", instance.UID, safe(instance.Host), safe(instance.URL)))
		if instance.Snapshot == nil {
			lines = append(lines, "  UNAVAILABLE · "+safe(instance.Error))
			continue
		}
		s := instance.Snapshot
		lines = append(lines, fmt.Sprintf("  Held %s (%d) · queued %s (%d)", peerQuantity(s.Held), s.HeldCount, peerQuantity(s.Queued), s.QueuedCount))
		appendPeerItems(&lines, *s, 8, &remaining)
	}
	lines = append(lines, "", safe(r.Advisory), "[v/Esc] Back to local budgets")
	return strings.Join(lines, "\n")
}
