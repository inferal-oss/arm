package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

type recoveryBrowser struct {
	records  []pendingRecord
	selected int
	err      string
}

func (m *dashboardModel) refreshPending() {
	records, err := loadPending(m.home)
	m.pendingRecords = records
	m.pendingStoreError = ""
	if err != nil {
		m.pendingStoreError = safe(err.Error())
	}
}
func (m *dashboardModel) canOpenRecovery() bool {
	return m.inputMode == "" && m.review == nil && m.ioPicker == nil && !m.pending && (m.composer == nil || m.composer.unknown) && !m.poolModal()
}
func (m *dashboardModel) openRecovery() tea.Cmd {
	if m.messageBusy != "" || (m.poolPending != nil && m.poolPending.busy != "") {
		m.notice = "Wait for the in-flight operation before selecting another retained request."
		return nil
	}
	m.refreshPending()
	m.composer = nil // the durable unknown request remains selectable in recovery
	m.detailGeneration++
	m.detail = nil
	m.record = nil
	m.recovery = &recoveryBrowser{records: m.pendingRecords, err: m.pendingStoreError}
	m.feedFocus = false
	m.feedOpen = false
	m.viewport.GotoTop()
	return nil
}
func (m *dashboardModel) blockPendingDraft(kind string) bool {
	m.refreshPending()
	if m.pendingStoreError != "" {
		m.notice = "Cannot verify private recovery storage: " + m.pendingStoreError + ". No send allowed; fix storage, then u Recovery."
		return true
	}
	for _, r := range m.pendingRecords {
		if r.Kind == kind && r.Authority == m.data.status.Host {
			m.openRecovery()
			m.notice = "A retained " + kind + " request is uncertain. Select it to inspect/retry the exact key before another change."
			return true
		}
	}
	return false
}
func (m *dashboardModel) prepareDurable(kind, authority string, request any, retry bool) (*pendingRecord, error) {
	r, err := newPendingRecord(kind, authority, request)
	if err == nil {
		r, err = reservePending(m.home, r, retry)
	}
	m.refreshPending()
	if err != nil {
		return nil, fmt.Errorf("nothing sent: private recovery intent was not confirmed durable: %w; inspect u Recovery", err)
	}
	return &r, nil
}
func (m *dashboardModel) finishDurable(r *pendingRecord, initialRejection ...bool) bool {
	if r == nil {
		m.notice = "Recovery identity is missing; retain the exact request/key and inspect its journal."
		return false
	}
	initial := len(initialRejection) > 0 && initialRejection[0]
	if err := settlePendingOutcome(m.home, *r, initial); err != nil {
		m.notice = "Backend outcome is known, but recovery storage could not be settled: " + safe(err.Error()) + ". Retain this key; fix storage and inspect again."
		m.refreshPending()
		return false
	}
	m.refreshPending()
	return true
}
func (m *dashboardModel) recoveryKey(key string) tea.Cmd {
	ui := m.recovery
	if ui == nil {
		return nil
	}
	if key >= "1" && key <= "6" {
		m.recovery = nil
		return m.key(mouseKey(key))
	}
	switch key {
	case "esc":
		m.recovery = nil
		m.viewport.GotoTop()
	case "r":
		m.refreshPending()
		ui.records = m.pendingRecords
		ui.err = m.pendingStoreError
		ui.selected = min(ui.selected, max(0, len(ui.records)-1))
	case "up", "k":
		ui.selected = max(0, ui.selected-1)
		m.keepRecoverySelectionVisible()
	case "down", "j":
		ui.selected = min(max(0, len(ui.records)-1), ui.selected+1)
		m.keepRecoverySelectionVisible()
	case "i":
		if ui.selected < len(ui.records) {
			m.record, _ = newRecord(ui.records[ui.selected], "Exact retained recovery record")
			m.viewport.GotoTop()
		}
	case "enter":
		if ui.selected >= len(ui.records) {
			return nil
		}
		r := ui.records[ui.selected]
		m.recovery = nil
		m.detail = nil
		m.record = nil
		m.resourceID = ""
		m.messageOperation++
		m.poolSequence++
		m.composer = nil
		m.poolUI = nil
		switch r.Kind {
		case "pool":
			var request model.PoolMutation
			json.Unmarshal(r.Request, &request)
			m.poolUI = &poolDashboard{mode: "operation"}
			m.poolPending = &poolOperation{request: request, state: "unknown", error: "Restored exact request. Lookup/retry is explicit; nothing has been sent on startup."}
			m.poolIntent = &r
		case "message":
			var request manager.MessageRequest
			json.Unmarshal(r.Request, &request)
			m.pendingMessage = &request
			m.messageIntent = &r
			m.composer = &messageComposer{request: request, authority: r.Authority, review: true, unknown: true}
		}
		m.notice = "Restored UNKNOWN request for authority " + safe(r.Authority) + "; no lookup or send was performed."
		m.viewport.GotoTop()
	default:
		scrollReview(&m.viewport, key)
	}
	return nil
}
func (m *dashboardModel) recoveryText() string {
	ui := m.recovery
	var out strings.Builder
	out.WriteString("RETAINED OPERATIONS · UNKNOWN ONLY\n\nOpening this list performs no network request. Select an exact record; lookup and same-key retry remain explicit.\nPrivate message bodies and reasons remain in your private ARM state directory.\n\n")
	if ui.err != "" {
		out.WriteString("STORAGE NEEDS ATTENTION\n" + ui.err + "\nNo unresolved file was purged. Fix the path/type/permissions or inspect the named record, then Refresh.\n\n")
	}
	for i, r := range ui.records {
		var q struct{ Reason, Body string }
		json.Unmarshal(r.Request, &q)
		purpose := q.Reason
		if r.Kind == "message" {
			purpose = q.Body
		}
		fmt.Fprintf(&out, "%s%s · %s\n  Key: %s\n  %s\n\n", selectedPrefix(ui.selected, i), human(r.Kind), safe(r.Authority), safe(r.key()), short(safe(purpose), max(10, m.mainWidth()-8)))
	}
	if len(ui.records) == 0 && ui.err == "" {
		out.WriteString("No retained uncertain operations.\n")
	}
	out.WriteString("\n[Enter] Recover selected · [i] Exact record · [r] Refresh · Esc back\nResolved backend outcomes are cleared only after matching lookup/response or definite initial rejection.")
	return out.String()
}
func (m *dashboardModel) recoveryControls() []mouseControl {
	return []mouseControl{{"esc", "Back"}, {"enter", "Recover"}, {"r", "Refresh"}, {"i", "Record"}, {"pgdown", "↓"}}
}

func (m *dashboardModel) recoveryMouseRegions() []mouseRegion {
	if m.recovery == nil || m.record != nil {
		return nil
	}
	width, height := m.viewportGeometry()
	var regions []mouseRegion
	next, row := 0, 0
	for _, line := range strings.Split(m.displayContent(), "\n") {
		plain := ansi.Strip(line)
		if next < len(m.recovery.records) && strings.HasPrefix(plain, "  Key: "+safe(m.recovery.records[next].key())) {
			y := 5 + row - 1 - m.viewport.YOffset()
			end := min(5+height, y+4)
			y = max(5, y)
			if end > y {
				regions = append(regions, mouseRegion{x: 2, y: y, width: width, height: end - y, row: next, recovery: true})
			}
			next++
		}
		row += max(1, (ansi.StringWidth(line)+max(1, width)-1)/max(1, width))
	}
	return regions
}

func (m *dashboardModel) keepRecoverySelectionVisible() {
	m.syncViewport()
	if m.recovery == nil || m.recovery.selected >= len(m.recovery.records) {
		return
	}
	width := max(1, m.viewport.Width())
	row, previous := 0, 0
	key := "  Key: " + safe(m.recovery.records[m.recovery.selected].key())
	for _, line := range strings.Split(m.displayContent(), "\n") {
		if strings.HasPrefix(ansi.Strip(line), key) {
			if previous < m.viewport.YOffset() {
				m.viewport.SetYOffset(previous)
			} else if row+2 >= m.viewport.YOffset()+m.viewport.Height() {
				m.viewport.SetYOffset(max(0, previous-m.viewport.Height()+5))
			}
			return
		}
		previous = row
		row += max(1, (ansi.StringWidth(line)+width-1)/width)
	}
}
