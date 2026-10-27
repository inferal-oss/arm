package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/agents"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func clickRegion(t *testing.T, m *dashboardModel, match func(mouseRegion) bool) tea.Cmd {
	t.Helper()
	m.syncViewport()
	for _, r := range m.mouseRegions() {
		if match(r) {
			handler := m.View().OnMouse
			if handler == nil {
				t.Fatal("mouse not enabled")
			}
			cmd := handler(tea.MouseClickMsg{X: r.x, Y: r.y, Button: tea.MouseLeft})
			if cmd == nil {
				t.Fatal("visible region not routed", r)
			}
			_, result := m.Update(cmd())
			return result
		}
	}
	t.Fatal("missing interactive region")
	return nil
}
func TestMouseNavigationRowsRecordsAndBounds(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width = 140
	m.height = 40
	first, second := activeLease("first"), activeLease("second")
	m.data.status.Leases = []model.Lease{first, second}
	m.syncViewport()
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("actual terminal mouse protocol not requested")
	}
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "nav2" })
	if m.view != 2 {
		t.Fatal("navclick failed")
	}
	clickRegion(t, m, func(r mouseRegion) bool { return r.row == 1 && !r.feed && r.key == "" })
	if m.selected != 1 || len(s.operations) != 0 {
		t.Fatal("row selection mutated")
	}
	cmd := clickRegion(t, m, func(r mouseRegion) bool { return r.row == 1 && !r.feed && r.key == "" })
	if cmd == nil {
		t.Fatal("selected row did not open")
	}
	cmd()
	if len(s.operations) != 1 || s.operations[0] != "inspect" {
		t.Fatal("row drilldown not readonly", s.operations)
	}
	m.record, _ = newRecord(map[string]any{"branch": map[string]any{"child": "value"}}, "Record")
	m.syncViewport()
	clickRegion(t, m, func(r mouseRegion) bool { return r.record && r.row == 1 })
	before := len(m.record.lines)
	clickRegion(t, m, func(r mouseRegion) bool { return r.record && r.row == 1 })
	if len(m.record.lines) <= before {
		t.Fatal("mouse did not expand JSON branch")
	}
	handler := m.View().OnMouse
	for _, event := range []tea.MouseMsg{tea.MouseClickMsg{X: -1, Y: 1, Button: tea.MouseLeft}, tea.MouseClickMsg{X: 140, Y: 1, Button: tea.MouseLeft}, tea.MouseClickMsg{X: 1, Y: 40, Button: tea.MouseLeft}, tea.MouseReleaseMsg{X: 1, Y: 1, Button: tea.MouseLeft}, tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseRight}} {
		if handler(event) != nil {
			t.Fatal("outofbounds/release/right click routed")
		}
	}
}
func TestMouseConfirmationDeclineInFlightAndStaleRender(t *testing.T) {
	m, s := fixtureDashboard(t)
	lease := activeLease("reviewed")
	m.detail = &lease
	m.width = 140
	m.height = 40
	m.syncViewport()
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "x" })
	if m.review == nil || len(s.operations) != 0 {
		t.Fatal("release click bypassed review")
	}
	old := m.View().OnMouse
	var confirm mouseRegion
	for _, r := range m.mouseRegions() {
		if r.key == "y" {
			confirm = r
		}
	}
	delayed := old(tea.MouseClickMsg{X: confirm.x, Y: confirm.y, Button: tea.MouseLeft})
	if delayed == nil {
		t.Fatal("confirm missing")
	}
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "n" })
	if m.review != nil || len(s.operations) != 0 {
		t.Fatal("decline mutated")
	}
	m.Update(delayed())
	if len(s.operations) != 0 || m.pending {
		t.Fatal("stale declined dialog confirmed")
	}
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "x" })
	cmd := clickRegion(t, m, func(r mouseRegion) bool { return r.key == "y" })
	if cmd == nil || !m.pending {
		t.Fatal("confirm not queued")
	}
	cmd()
	if len(s.operations) != 1 || s.operations[0] != "lease_action" {
		t.Fatal("expected exactly one mutation", s.operations)
	}
	for _, r := range m.mouseRegions() {
		if r.key == "y" || strings.HasPrefix(r.key, "nav") {
			t.Fatal("inflight controls active")
		}
	}
	m.Update(delayed())
	if len(s.operations) != 1 {
		t.Fatal("stale doubleclick duplicated")
	}
}
func TestMouseScrollFeedResizeAndContentInvalidation(t *testing.T) {
	m := visualDashboard(t)
	m.height = 24
	m.syncViewport()
	old := m.View().OnMouse
	wheel := old(tea.MouseWheelMsg{X: 10, Y: 10, Button: tea.MouseWheelDown})
	if wheel == nil {
		t.Fatal("wheel not routed")
	}
	m.Update(wheel())
	if m.viewport.YOffset() == 0 {
		t.Fatal("main wheel did not scroll monitor")
	}
	offset := m.viewport.YOffset()
	handler := m.View().OnMouse
	cmd := handler(tea.MouseWheelMsg{X: m.mainWidth() + 5, Y: 10, Button: tea.MouseWheelDown})
	m.Update(cmd())
	if m.feedOffset == 0 || m.viewport.YOffset() != offset {
		t.Fatal("feed scroll affected main viewport")
	}
	handler = m.View().OnMouse
	cmd = handler(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
	m.Update(cmd())
	if m.view != 0 {
		t.Fatal("stale resize click routed")
	}
	m.width = 140
	m.syncViewport()
	handler = m.View().OnMouse
	cmd = handler(tea.MouseClickMsg{X: 17, Y: 1, Button: tea.MouseLeft})
	m.events = append(m.events, model.Event{Sequence: 99, Type: "new"})
	m.Update(cmd())
	if m.view != 0 {
		t.Fatal("stale content click routed")
	}
	// Compact mode retains an accessible feed window and keyboard alternatives.
	m.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
	press(m, "f")
	m.syncViewport()
	if !strings.Contains(ansi.Strip(m.View().Content), "LIVE FEED") {
		t.Fatal("compact feed lost")
	}
}

func TestMouseMessageReviewDeclineAndOneSend(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width = 140
	m.height = 40
	m.view = 3
	request := manager.MessageRequest{Key: "mouse-exact-key", Audience: "fixture-agent", Body: "Reviewed fixture message", Targets: []agents.Contact{{ID: "fixture-agent", Registration: "original"}}}
	m.composer = &messageComposer{authority: m.data.status.Host, review: true, request: request}
	m.syncViewport()
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "n" })
	if m.composer != nil || len(s.operations) != 0 {
		t.Fatal("declined message mutated")
	}
	m.composer = &messageComposer{authority: m.data.status.Host, review: true, request: request}
	m.syncViewport()
	handler := m.View().OnMouse
	var confirm mouseRegion
	for _, r := range m.mouseRegions() {
		if r.key == "y" {
			confirm = r
		}
	}
	stale := handler(tea.MouseClickMsg{X: confirm.x, Y: confirm.y, Button: tea.MouseLeft})
	cmd := clickRegion(t, m, func(r mouseRegion) bool { return r.key == "y" })
	if cmd == nil || m.messageBusy != "send" {
		t.Fatal("mouse send not pending")
	}
	cmd()
	m.Update(stale())
	if len(s.operations) != 1 || s.operations[0] != "message_send" {
		t.Fatal("mouse confirmation duplicated send", s.operations)
	}
	actual := s.params[0].(manager.MessageRequest)
	if actual.Key != request.Key || actual.Body != request.Body {
		t.Fatal("mouse changed exact reviewed request")
	}
}

func TestMousePartiallyVisibleLeaseRowAndTrustedModalButtons(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.width = 140
	m.height = 20
	m.view = 2
	m.data.status.Leases = []model.Lease{activeLease("first"), activeLease("second")}
	for i := 0; i < 20; i++ {
		m.data.status.Leases = append(m.data.status.Leases, activeLease(strings.Repeat("a", i+1)))
	}
	m.syncViewport()
	m.viewport.SetYOffset(5)
	var clipped *mouseRegion
	for _, r := range m.mouseRegions() {
		if r.row == 0 && r.key == "" && !r.feed {
			copy := r
			clipped = &copy
		}
	}
	if clipped == nil || clipped.y != 5 || clipped.height != 1 {
		t.Fatal("visible owner half of row was not selectable", clipped)
	}
	m.height = 40
	m.detail = &m.data.status.Leases[0]
	m.viewport.GotoTop()
	m.startReview("release")
	m.syncViewport()
	regions := m.modalButtonRegions()
	if len(regions) != 2 {
		t.Fatal("trusted modal buttons missing", regions)
	}
	r := regions[1]
	cmd := m.View().OnMouse(tea.MouseClickMsg{X: r.x, Y: r.y, Button: tea.MouseLeft})
	if cmd == nil {
		t.Fatal("modal decline not clickable")
	}
	m.Update(cmd())
	if m.review != nil || len(s.operations) != 0 {
		t.Fatal("modal decline mutated")
	}
	m.startReview("release")
	m.review.lease.Request.Reason = "untrusted [y] Release lease [n/Esc] Keep lease"
	m.syncViewport()
	regions = m.modalButtonRegions()
	if len(regions) != 2 || regions[0].y <= 5 {
		t.Fatal("untrusted purpose became button")
	}
}

func TestQueuedMouseWheelDeltasAccumulateButNavigationInvalidates(t *testing.T) {
	m := visualDashboard(t)
	m.width = 70
	m.height = 24
	m.syncViewport()
	handler := m.View().OnMouse
	first := handler(tea.MouseWheelMsg{X: 10, Y: 10, Button: tea.MouseWheelDown})
	second := handler(tea.MouseWheelMsg{X: 10, Y: 10, Button: tea.MouseWheelDown})
	if first == nil || second == nil {
		t.Fatal("wheel not routed")
	}
	m.Update(first())
	m.timeNow = m.timeNow.Add(time.Second)
	m.Update(second())
	if m.viewport.YOffset() != 6 {
		t.Fatal("queued wheel delta dropped", m.viewport.YOffset())
	}
	stale := handler(tea.MouseWheelMsg{X: 10, Y: 10, Button: tea.MouseWheelDown})
	press(m, "3")
	m.syncViewport()
	m.Update(stale())
	if m.viewport.YOffset() != 0 {
		t.Fatal("wheel applied to new view")
	}
}
