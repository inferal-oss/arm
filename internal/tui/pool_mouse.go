package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func (m *dashboardModel) poolModal() bool {
	return m.poolUI != nil && m.detail == nil && m.record == nil && (m.poolUI.review != nil || m.poolUI.form != nil || m.poolUI.mode == "actions")
}
func (m *dashboardModel) poolWindowTitle() (string, string) {
	ui := m.poolUI
	if ui.review != nil {
		return "REVIEW POOL · " + strings.ToUpper(ui.review.request.Operation), "warning"
	}
	if ui.form != nil {
		return "POOL · CURRENT SNAPSHOT", ""
	}
	return "CUSTOM POOLS · " + strings.ToUpper(ui.mode), ""
}
func (m *dashboardModel) poolMouseFrame() string {
	if m.poolUI == nil {
		return ""
	}
	return fmt.Sprintf("/%p/%d/%d/%p", m.poolUI, m.poolUI.generation, m.poolSequence, m.poolPending)
}
func (m *dashboardModel) poolScrollFrame() string {
	if m.poolUI == nil {
		return ""
	}
	return fmt.Sprintf("/%p/%s/%d/%p/%p", m.poolUI, m.poolUI.mode, m.poolUI.generation, m.poolUI.form, m.poolUI.review)
}
func (m *dashboardModel) poolMouseRegions() []mouseRegion {
	ui := m.poolUI
	if ui == nil || ui.review != nil || ui.form != nil || ui.loading || m.detail != nil || m.record != nil {
		return nil
	}
	starts := map[int]int{}
	switch ui.mode {
	case "list":
		for i := range ui.views {
			starts[4+2*i] = i
		}
	case "detail", "consumers":
		if ui.current != nil {
			if ui.mode == "consumers" {
				starts = m.poolSubtreeRender().rows
			} else {
				starts = m.poolDetailRender().rows
			}
		}
	case "actions":
		for i := range m.poolActionOptions() {
			starts[2+i] = i
		}
	case "history":
		for i := range ui.history.Entries {
			starts[4+6*i] = i
		}
	default:
		return nil
	}
	width, height := m.viewportGeometry()
	originX, originY := 2, 5
	if m.modal() {
		dialogWidth, dialogHeight := m.modalGeometry()
		originX = max(0, (m.mainWidth()-dialogWidth)/2) + 2
		originY = 4 + max(0, (m.bodyHeight()-dialogHeight)/2) + 1
	}
	bottom := originY + height
	var regions []mouseRegion
	row := 0
	for source, line := range strings.Split(m.displayContent(), "\n") {
		if index, ok := starts[source]; ok {
			y := originY + row - m.viewport.YOffset()
			h := 1
			if ui.mode == "list" {
				h = 2
			}
			end := min(bottom, y+h)
			y = max(originY, y)
			if end > y {
				regions = append(regions, mouseRegion{x: originX, y: y, width: max(1, width), height: end - y, row: index, pool: true})
			}
		}
		row += max(1, (ansi.StringWidth(line)+max(1, width)-1)/max(1, width))
	}
	return regions
}
func (m *dashboardModel) acceptPoolMouse(region mouseRegion) tea.Cmd {
	ui := m.poolUI
	if ui == nil || ui.loading || ui.review != nil || ui.form != nil || region.row < 0 {
		return nil
	}
	switch ui.mode {
	case "list":
		if region.row >= len(ui.views) {
			return nil
		}
		if ui.selected == region.row {
			return m.readPools("detail", ui.views[region.row].Pool.ID, 0, nil)
		}
		ui.selected = region.row
	case "detail", "consumers":
		if ui.current != nil {
			users := append(append([]model.ResourceUse{}, ui.current.Users...), ui.current.Queued...)
			if ui.mode == "consumers" {
				users = nil
				for _, entry := range ui.consumers.Entries {
					users = append(users, entry.Use)
				}
			}
			if region.row < len(users) {
				ui.selected = region.row
				return m.loadDetail(users[region.row].Lease)
			}
		}
	case "actions":
		options := m.poolActionOptions()
		if region.row < len(options) {
			ui.selected = region.row
			return m.beginPoolForm(options[region.row])
		}
	case "history":
		if region.row < len(ui.history.Entries) {
			ui.selected = region.row
			m.record, _ = newRecord(ui.history.Entries[region.row], "Pool journal entry")
			m.viewport.GotoTop()
		}
	}
	return nil
}
