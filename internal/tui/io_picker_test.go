package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/model"
)

func TestIOChecklistMouseKeyboardAllNoneAndNoBackendMutations(t *testing.T) {
	m := visualDashboard(t)
	transport := m.session.(*testTransport)
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "d" && r.y < m.height-3 })
	if m.ioPicker == nil || !m.ioPicker.disk {
		t.Fatal("chart selector not clickable")
	}
	clickRegion(t, m, func(r mouseRegion) bool { return r.picker && r.row == 1 })
	if m.diskSelection.All || len(m.diskSelection.IDs) != 1 || !m.diskSelection.IDs["disk-0"] || !m.networkSelection.All {
		t.Fatal("independent custom checkbox failed", m.diskSelection)
	}
	press(m, " ")
	if len(m.diskSelection.IDs) != 2 || m.diskSelection.All {
		t.Fatal("space did not toggle selected checkbox")
	}
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "a" })
	if !m.diskSelection.All {
		t.Fatal("All mouse button failed")
	}
	clickRegion(t, m, func(r mouseRegion) bool { return r.key == "n" })
	if m.diskSelection.All || len(m.diskSelection.IDs) != 0 {
		t.Fatal("None mouse button failed")
	}
	press(m, "esc")
	m.syncViewport()
	text := ansi.Strip(m.monitorText())
	if !strings.Contains(text, "Nothing selected") || !strings.Contains(text, "unavailable") {
		t.Fatal("none fabricated idle sum", text)
	}
	press(m, "n")
	if m.ioPicker == nil || m.ioPicker.disk {
		t.Fatal("network picker missing")
	}
	press(m, "n")
	press(m, "a")
	press(m, "esc")
	if !m.networkSelection.All || m.diskSelection.All {
		t.Fatal("network controls altered disk")
	}
	if len(transport.operations) != 0 {
		t.Fatal("view selection sent backend requests", transport.operations)
	}
}
func TestIOPickerKeepsMissingSelectionAndRejectsStaleCheckbox(t *testing.T) {
	m := visualDashboard(t)
	m.diskSelection = ioSelection{IDs: map[string]bool{"disk-0": true, "missing": true}}
	m.openIOPicker(true)
	m.syncViewport()
	options := m.ioOptions()
	if len(options) != 3 || !options[2].missing || !options[2].checked {
		t.Fatal("missing selected ID vanished", options)
	}
	if !strings.Contains(ansi.Strip(m.pickerText()), "not currently reported") || m.diskSelection.latest(m.ioItems(true), true) != nil {
		t.Fatal("missing scope was treated complete")
	}
	old := m.View().OnMouse
	var row mouseRegion
	for _, r := range m.mouseRegions() {
		if r.picker && r.row == 0 {
			row = r
		}
	}
	delayed := old(tea.MouseClickMsg{X: row.x, Y: row.y, Button: tea.MouseLeft})
	if delayed == nil {
		t.Fatal("checkbox region missing")
	}
	m.page.Latest.DiskIO = append(m.page.Latest.DiskIO, model.MonitorIO{ID: "new", Name: "New device"})
	m.Update(delayed())
	if !m.diskSelection.IDs["disk-0"] || m.diskSelection.IDs["new"] {
		t.Fatal("stale checkbox retargeted/new ID selected")
	}
	press(m, "?")
	if m.ioPicker != nil || m.record == nil {
		t.Fatal("measurement help not accessible")
	}
	if !strings.Contains(m.record.text(100), "Reported sums") {
		t.Fatal("scope help missing")
	}
}
func TestIOChecklistResponsiveScrollingAndEmptyInventory(t *testing.T) {
	for _, size := range [][2]int{{160, 46}, {110, 32}, {70, 24}, {45, 14}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := visualDashboard(t)
			m.width, m.height = size[0], size[1]
			for i := 0; i < 20; i++ {
				m.page.Latest.Network = append(m.page.Latest.Network, model.MonitorIO{ID: fmt.Sprint("extra-", i), Name: fmt.Sprint("Interface ", i)})
			}
			m.openIOPicker(false)
			m.syncViewport()
			for i := 0; i < 20; i++ {
				m.key(mouseKey("down"))
			}
			m.syncViewport()
			if m.viewport.YOffset() == 0 {
				t.Fatal("keyboard focus did not scroll popup")
			}
			selected := m.ioPicker.selectedID
			m.key(mouseKey("space"))
			if m.networkSelection.All || m.networkSelection.IDs[selected] {
				t.Fatal("keyboard toggled wrong option", selected)
			}
			screen := ansi.Strip(m.View().Content)
			lines := strings.Split(screen, "\n")
			if len(lines) != m.height {
				t.Fatal("popup escaped viewport height")
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > m.width {
					t.Fatal("popup escaped viewport width")
				}
			}
			for _, region := range m.pickerMouseRegions() {
				if region.x < 0 || region.y < 4 || region.x+region.width > m.mainWidth() || region.y+region.height > 4+m.bodyHeight() {
					t.Fatal("clipped picker hit bounds", region)
				}
			}
			bar, controls := m.controlBar()
			doneVisible := false
			for _, control := range controls {
				if control.key == "esc" {
					doneVisible = control.x+control.width <= m.width
				}
			}
			if !doneVisible || !strings.Contains(ansi.Strip(bar), "[esc] Done") {
				t.Fatal("compact mouse dismissal clipped", bar)
			}
			clickRegion(t, m, func(r mouseRegion) bool { return r.key == "esc" && r.y == m.height-3 })
			if m.ioPicker != nil {
				t.Fatal("mouse Done did not close popup")
			}

		})
	}
	m := visualDashboard(t)
	m.page.Latest = nil
	m.openIOPicker(true)
	m.syncViewport()
	if !strings.Contains(ansi.Strip(m.pickerText()), "No device/interface counters") {
		t.Fatal("empty inventory state missing")
	}
	press(m, "n")
	press(m, " ")
	press(m, "a")
	if !m.diskSelection.All {
		t.Fatal("empty All failed")
	}
}

func TestKnownIdleIOHistoryHasBaselineNotUnknownGap(t *testing.T) {
	m := visualDashboard(t)
	zero := 0.0
	items := []model.MonitorIO{rateItem("idle", &zero, &zero)}
	m.history = []model.MonitorPoint{{DiskIO: []model.MonitorHistoryIO{{ID: "idle", Read: &zero, Write: &zero}}}}
	m.diskSelection = ioSelection{All: true}
	if screen := ansi.Strip(m.ioWindow(items, true, 60)); !strings.Contains(screen, "↓ ▁") || strings.Contains(screen, "unavailable") {
		t.Fatal("known idle was rendered as unknown", screen)
	}
}

func TestClosingIOPickerRestoresMonitorScrollContext(t *testing.T) {
	m := visualDashboard(t)
	m.width = 70
	m.height = 24
	m.syncViewport()
	m.viewport.SetYOffset(8)
	before := m.viewport.YOffset()
	if before == 0 {
		t.Fatal("fixture not scrolled")
	}
	m.openIOPicker(true)
	m.syncViewport()
	press(m, "n")
	press(m, "esc")
	m.syncViewport()
	if m.viewport.YOffset() != before {
		t.Fatal("closing picker lost chart context", before, m.viewport.YOffset())
	}
}
