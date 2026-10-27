package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Match semantic row prefixes in the actual rendered content, including wrapped
// rows and modal geometry. No untrusted text creates an executable control.
func (m *dashboardModel) meshMouseRegions() []mouseRegion {
	ui := m.meshUI
	if ui == nil || m.view != 6 || m.nodeUI != nil || m.record != nil || ui.loading || ui.busy || ui.prompt != nil {
		return nil
	}
	if ui.form == nil && ui.mode != "nodes" && ui.mode != "operations" {
		return nil
	}
	width, height := m.viewportGeometry()
	x, y := 2, 5
	if m.modal() {
		w, h := m.modalGeometry()
		x = max(0, (m.mainWidth()-w)/2) + 2
		y = 4 + max(0, (m.bodyHeight()-h)/2) + 1
	}
	var regions []mouseRegion
	row, index := 0, 0
	for _, line := range strings.Split(m.displayContent(), "\n") {
		plain := ansi.Strip(line)
		if ui.form == nil && ui.mode == "nodes" && index < m.meshItemCount() && (strings.HasPrefix(plain, "╔═ ") || strings.HasPrefix(plain, "╭─ ")) {
			start := y + row - m.viewport.YOffset()
			end := min(y+height, start+6)
			start = max(y, start)
			if end > start {
				regions = append(regions, mouseRegion{x: x, y: start, width: width, height: end - start, row: index, mesh: true})
			}
			index++
		}
		if len(plain) >= 2 && (strings.HasPrefix(plain, "> ") || strings.HasPrefix(plain, "  ")) && index < m.meshItemCount() {
			label := ""
			if ui.form != nil {
				label = ui.form.fields[index].label
			} else if ui.mode == "nodes" {
				label = nodeDisplayName(ui.registry.Nodes[index])
			} else {
				label = ui.operations[index].Kind + " · " + meshPhaseLabel(ui.operations[index])
			}
			if strings.HasPrefix(plain[2:], safe(label)) {
				start := y + row - m.viewport.YOffset()
				end := min(y+height, start+1)
				start = max(y, start)
				if end > start {
					regions = append(regions, mouseRegion{x: x, y: start, width: width, height: end - start, row: index, mesh: true})
				}
				index++
			}
		}
		row += max(1, (ansi.StringWidth(line)+max(1, width)-1)/max(1, width))
	}
	return regions
}

func (m *dashboardModel) acceptMeshMouse(region mouseRegion) tea.Cmd {
	ui := m.meshUI
	if ui == nil || ui.loading || ui.busy || ui.prompt != nil || region.row < 0 || region.row >= m.meshItemCount() {
		return nil
	}
	selected := ui.selected
	if ui.form != nil {
		selected = ui.form.selected
		ui.form.selected = region.row
	} else {
		ui.selected = region.row
	}
	if selected == region.row {
		return m.meshKey("enter")
	}
	return nil
}
