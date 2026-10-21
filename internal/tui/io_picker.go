package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/model"
)

type ioPicker struct {
	disk         bool
	selectedID   string
	returnOffset int
}
type ioOption struct {
	id, name         string
	missing, checked bool
}

func (m *dashboardModel) ioItems(disk bool) []model.MonitorIO {
	if m.page.Latest == nil {
		return nil
	}
	if disk {
		return m.page.Latest.DiskIO
	}
	return m.page.Latest.Network
}
func (m *dashboardModel) ioSelection(disk bool) *ioSelection {
	if disk {
		return &m.diskSelection
	}
	return &m.networkSelection
}
func (m *dashboardModel) ioOptions() []ioOption {
	if m.ioPicker == nil {
		return nil
	}
	selection := m.ioSelection(m.ioPicker.disk)
	var options []ioOption
	known := map[string]bool{}
	for _, item := range m.ioItems(m.ioPicker.disk) {
		if known[item.ID] {
			continue
		}
		known[item.ID] = true
		name := item.Name
		if name == "" {
			name = item.ID
		}
		options = append(options, ioOption{id: item.ID, name: name, checked: selection.All || selection.IDs[item.ID]})
	}
	var missing []string
	for id, selected := range selection.IDs {
		if !selection.All && selected && !known[id] {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	for _, id := range missing {
		options = append(options, ioOption{id: id, name: id, missing: true, checked: true})
	}
	return options
}
func (m *dashboardModel) pickerIndex() int {
	for i, option := range m.ioOptions() {
		if option.id == m.ioPicker.selectedID {
			return i
		}
	}
	return 0
}
func (m *dashboardModel) openIOPicker(disk bool) tea.Cmd {
	m.ioPicker = &ioPicker{disk: disk, returnOffset: m.viewport.YOffset()}
	if options := m.ioOptions(); len(options) > 0 {
		m.ioPicker.selectedID = options[0].id
	}
	m.viewport.GotoTop()
	return nil
}
func (m *dashboardModel) pickerKey(key string) tea.Cmd {
	options := m.ioOptions()
	index := m.pickerIndex()
	selection := m.ioSelection(m.ioPicker.disk)
	switch key {
	case "esc":
		offset := m.ioPicker.returnOffset
		m.ioPicker = nil
		m.syncViewport()
		m.viewport.SetYOffset(offset)
	case "a":
		*selection = ioSelection{All: true}
	case "n":
		*selection = ioSelection{IDs: map[string]bool{}}
	case "enter", " ", "space":
		if len(options) > 0 {
			selection.toggle(options[index].id, m.ioItems(m.ioPicker.disk))
		}
	case "up", "k":
		if len(options) > 0 {
			index = max(0, index-1)
			m.ioPicker.selectedID = options[index].id
			m.keepPickerVisible(index)
		}
	case "down", "j":
		if len(options) > 0 {
			index = min(len(options)-1, index+1)
			m.ioPicker.selectedID = options[index].id
			m.keepPickerVisible(index)
		}
	case "?":
		m.ioPicker = nil
		m.record, _ = newRecord(map[string]any{"Reported sums": "Charts sum the selected reported device or interface rates. Layered disks and virtual network interfaces can overlap, so these sums are not exclusive physical throughput.", "Unknown and reset": "A missing selected member, unavailable counter, counter reset or time gap makes that direction unavailable or breaks its history. Known idle traffic is zero; unknown traffic is never filled with zero.", "All": "Includes newly reported devices/interfaces. Historical points with different membership are gaps, not throughput changes.", "Custom selection": "Keeps exactly your chosen IDs during this dashboard session. New devices are not added. Missing selected IDs remain selected and make the sum unavailable until they return or are deselected.", "No selection": "Shows no total, rather than a misleading idle zero. No backend resource, lease or host setting is changed."}, "I/O chart help")
		m.viewport.GotoTop()
	default:
		scrollReview(&m.viewport, key)
	}
	return nil
}
func (m *dashboardModel) keepPickerVisible(index int) {
	m.syncViewport()
	row := 3 + 2*index
	if row < m.viewport.YOffset() {
		m.viewport.SetYOffset(row)
	} else if row+1 >= m.viewport.YOffset()+m.viewport.Height() {
		m.viewport.SetYOffset(max(0, row-m.viewport.Height()+2))
	}
}
func (m *dashboardModel) pickerText() string {
	disk := m.ioPicker.disk
	selection := m.ioSelection(disk)
	noun := "interfaces"
	if disk {
		noun = "devices"
	}
	options := m.ioOptions()
	width := max(1, min(m.mainWidth(), 88)-4)
	mode := "Custom selection"
	if selection.All {
		mode = "All reported " + noun
	} else if len(selection.selectedIDs(m.ioItems(disk))) == 0 {
		mode = "Nothing selected"
	}
	lines := []string{strong.Render(short("Choose "+noun+" to include.", width)), "Mode: " + mode, ""}
	selectedIndex := m.pickerIndex()
	for i, option := range options {
		mark := " "
		if option.checked {
			mark = "x"
		}
		label := safe(option.name)
		if option.missing {
			label += " · not currently reported"
		}
		row := selectedPrefix(selectedIndex, i) + "[" + mark + "] " + short(label, max(1, width-6))
		style := panelStyle
		if i == selectedIndex {
			style = style.Background(lipgloss.Color("#203630")).Bold(true)
		}
		lines = append(lines, style.Render(fitBlock(row, width, 1)), muted.Render("      "+short(safe(option.id), max(1, width-6))))
	}
	if len(options) == 0 {
		lines = append(lines, muted.Render("No device/interface counters currently reported."))
	}
	lines = append(lines, "", muted.Render("Enter/Space toggles · a All · n None · Esc Done"), muted.Render("Changes affect this chart only. [?] Measurement help."))
	return strings.Join(lines, "\n")
}
func (m *dashboardModel) pickerMouseRegions() []mouseRegion {
	if m.ioPicker == nil {
		return nil
	}
	width, height := m.modalGeometry()
	x := max(0, (m.mainWidth()-width)/2) + 2
	yOrigin := 4 + max(0, (m.bodyHeight()-height)/2) + 1
	var regions []mouseRegion
	for i := range m.ioOptions() {
		y := yOrigin + 3 + 2*i - m.viewport.YOffset()
		bottom := min(yOrigin+m.viewport.Height(), y+2)
		y = max(yOrigin, y)
		if bottom > y {
			regions = append(regions, mouseRegion{x: x, y: y, width: max(1, width-4), height: bottom - y, row: i, picker: true})
		}
	}
	return regions
}
func (m *dashboardModel) monitorSelectorRegions() []mouseRegion {
	if !m.monitorLayout() {
		return nil
	}
	var regions []mouseRegion
	for row, line := range strings.Split(m.monitorText(), "\n") {
		plain := ansi.Strip(line)
		for _, key := range []string{"d", "n"} {
			label := fmt.Sprintf("[%s] Select", key)
			index := strings.Index(plain, label)
			if index < 0 {
				continue
			}
			y := 4 + row - m.viewport.YOffset()
			if y >= 4 && y < 4+m.bodyHeight() {
				regions = append(regions, mouseRegion{x: ansi.StringWidth(plain[:index]), y: y, width: len(label), height: 1, key: key, row: -1})
			}
		}
	}
	return regions
}
