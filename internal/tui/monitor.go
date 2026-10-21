package tui

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/yrashk/arm/internal/model"
)

func (m *dashboardModel) monitorText() string {
	width := m.mainWidth()
	s := m.page.Latest
	if s == nil {
		return window("HOST MONITOR", muted.Render("Waiting for the first host sample…")+"\n\nCPU, memory, storage and I/O will appear here.\nUnknown measurements stay explicitly unavailable.", width, 8, false, "")
	}
	cpu := m.cpuWindow(s, width)
	if width < 84 {
		return strings.Join([]string{cpu, m.memoryWindow(s, width), m.storageWindow(s, width), m.ioWindow(s.DiskIO, true, width), m.ioWindow(s.Network, false, width)}, "\n")
	}
	left := (width - 1) / 2
	right := width - left - 1
	memoryStorage := lipgloss.JoinHorizontal(lipgloss.Top, m.memoryWindow(s, left), " ", m.storageWindow(s, right))
	diskNetwork := lipgloss.JoinHorizontal(lipgloss.Top, m.ioWindow(s.DiskIO, true, left), " ", m.ioWindow(s.Network, false, right))
	return cpu + "\n" + memoryStorage + "\n" + diskNetwork
}

func (m *dashboardModel) cpuWindow(s *model.MonitorSnapshot, width int) string {
	inner := width - 4
	big := strong.Foreground(colorAccent).Render(measurement(s.CPU))
	lines := []string{align(big+muted.Render(" busy"), muted.Render(fmt.Sprintf("%d logical CPUs", len(s.Cores))), inner)}
	graphWidth := max(5, inner-6)
	columns := max(1, inner/20)
	rows := 2
	if m.height >= 36 {
		rows = 3
	}
	limit := min(len(s.Cores), columns*rows)
	coreRows := (limit + columns - 1) / columns
	cpuHeight := max(8, m.bodyHeight()-18)
	if width < 84 {
		cpuHeight = min(12, max(8, m.bodyHeight()/2))
	}
	graphHeight := max(2, cpuHeight-coreRows-4)
	lines = append(lines, m.historyGraph(graphWidth, graphHeight, 100, func(p model.MonitorPoint) *float64 { return p.CPU }, "accent")...)
	for start := 0; start < limit; start += columns {
		var cells []string
		for i := start; i < min(start+columns, limit); i++ {
			core := s.Cores[i]
			label := fmt.Sprintf("%02d", i+1)
			percent := measurement(core.Busy)
			total := float64(100)
			cell := muted.Render(label) + " " + bar(core.Busy.Value, &total, 7, "") + " " + strong.Render(fmt.Sprintf("%6s", percent))
			cells = append(cells, fitBlock(cell, inner/columns, 1))
		}
		lines = append(lines, strings.Join(cells, ""))
	}
	foot := fmt.Sprintf("%ds history · %s samples", len(m.history)*max(1, int(m.page.Cadence.Seconds())), m.page.Cadence)
	if limit < len(s.Cores) {
		foot += fmt.Sprintf(" · +%d cores in [i] record", len(s.Cores)-limit)
	}
	lines = append(lines, align(muted.Render(foot), muted.Render("GPU ")+measurement(s.GPU), inner))
	return window("CPU", strings.Join(lines, "\n"), width, 0, false, "accent")
}

func (m *dashboardModel) memoryWindow(s *model.MonitorSnapshot, width int) string {
	inner := width - 4
	r := s.Memory
	lines := []string{align(strong.Foreground(colorBlue).Render(measurement(r.Used)), strong.Render(percentage(r.Used.Value, r.Total.Value)), inner), bar(r.Used.Value, r.Total.Value, inner, "blue"), align(muted.Render("Available"), measurement(r.Available), inner), align(muted.Render("Total"), measurement(r.Total), inner), lipgloss.NewStyle().Foreground(colorBlue).Render("History 60s " + historyLine(m.history, max(1, inner-12), m.page.Cadence, totalValue(r), func(p model.MonitorPoint) *float64 { return p.MemoryUsed })), align(muted.Render("Swap used"), measurement(s.Swap.Used)+" / "+measurement(s.Swap.Total), inner)}
	return window("MEMORY", strings.Join(lines, "\n"), width, 8, false, "blue")
}
func (m *dashboardModel) storageWindow(s *model.MonitorSnapshot, width int) string {
	inner := width - 4
	r := s.Filesystem
	label := "STORAGE"
	if s.FilesystemStale {
		label += " · STALE"
	}
	detail := "Filesystem observation"
	if r.Filesystem != nil {
		detail = strings.ToUpper(safe(r.Filesystem.Type))
		if r.Filesystem.Shared {
			detail += " · shared pool"
		}
	}
	lines := []string{align(strong.Render(measurement(r.Available))+muted.Render(" free"), strong.Render(percentage(r.Used.Value, r.Total.Value))+muted.Render(" used"), inner), bar(r.Used.Value, r.Total.Value, inner, ""), align(muted.Render("Used"), measurement(r.Used), inner), align(muted.Render("Total"), measurement(r.Total), inner), accent.Render("History 60s " + historyLine(m.history, max(1, inner-12), m.page.Cadence, totalValue(r), func(p model.MonitorPoint) *float64 { return p.FilesystemAvailable })), muted.Render(detail + " · free space")}
	return window(label, strings.Join(lines, "\n"), width, 8, false, "")
}
func totalValue(r model.ResourceMeasurement) float64 {
	if r.Total.Value == nil {
		return 0
	}
	return *r.Total.Value
}

func selectedIO(items []model.MonitorIO, id string) (model.MonitorIO, int, bool) {
	if len(items) == 0 {
		return model.MonitorIO{}, 0, false
	}
	for index, item := range items {
		if item.ID == id {
			return item, index, true
		}
	}
	return items[0], 0, true
}
func (m *dashboardModel) ioWindow(items []model.MonitorIO, disk bool, width int) string {
	inner := width - 4
	title := "NETWORK"
	id := m.selectedNetwork
	key := "n"
	tone := "purple"
	if disk {
		title = "DISK I/O"
		id = m.selectedDisk
		key = "d"
		tone = "blue"
	}
	item, index, found := selectedIO(items, id)
	if !found {
		return window(title, muted.Render("unavailable")+"\n\nNo counters reported for this host/sample.\n\n\n[i] complete sample · [?] measurement help", width, 8, false, tone)
	}
	name := short(item.Name, max(1, inner-14))
	readLabel, writeLabel := "↓ Receive", "↑ Send"
	if disk {
		readLabel, writeLabel = "↓ Read", "↑ Write"
	}
	lines := []string{align(strong.Render(name), muted.Render(fmt.Sprintf("%d/%d [%s] next", index+1, len(items), key)), inner), align(muted.Render(readLabel), strong.Render(measurement(item.Read)), inner), align(muted.Render(writeLabel), strong.Render(measurement(item.Write)), inner)}
	ceiling := float64(0)
	get := func(read bool) func(model.MonitorPoint) *float64 {
		return func(p model.MonitorPoint) *float64 {
			values := p.Network
			if disk {
				values = p.DiskIO
			}
			for _, v := range values {
				if v.ID == item.ID {
					if read {
						return v.Read
					}
					return v.Write
				}
			}
			return nil
		}
	}
	for _, p := range m.history {
		for _, read := range []bool{true, false} {
			if value := get(read)(p); value != nil {
				ceiling = max(ceiling, *value)
			}
		}
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(colorBlue).Render("↓ "+historyLine(m.history, max(1, inner-2), m.page.Cadence, ceiling, get(true))), lipgloss.NewStyle().Foreground(colorPurple).Render("↑ "+historyLine(m.history, max(1, inner-2), m.page.Cadence, ceiling, get(false))), muted.Render("Per-device scope · [?] help"))
	return window(title, strings.Join(lines, "\n"), width, 8, false, tone)
}

func (m *dashboardModel) historyGraph(width, height int, ceiling float64, value func(model.MonitorPoint) *float64, tone string) []string {
	points := m.history
	if len(points) > 60 {
		points = points[len(points)-60:]
	}
	rows := make([][]rune, height)
	for row := range rows {
		rows[row] = []rune(strings.Repeat(" ", width))
	}
	blocks := []rune(" ▁▂▃▄▅▆▇█")
	for column := 0; column < width; column++ {
		slot := int(math.Round(float64(column) * 59 / float64(max(1, width-1))))
		index := slot - (60 - len(points))
		if index < 0 || index >= len(points) {
			continue
		}
		point := points[index]
		n := value(point)
		gap := index > 0 && point.At.Sub(points[index-1].At) > m.page.Cadence*3/2
		if n == nil || ceiling <= 0 || math.IsNaN(*n) || math.IsInf(*n, 0) || gap {
			rows[height-1][column] = '·'
			continue
		}
		level := int(math.Round(max(0, min(1, *n/ceiling)) * float64(height*8)))
		for row := height - 1; row >= 0; row-- {
			fill := max(0, min(8, level-(height-1-row)*8))
			if fill > 0 {
				rows[row][column] = blocks[fill]
			}
		}
	}
	out := make([]string, height)
	for row, content := range rows {
		label := ""
		if row == 0 {
			label = "100%"
		}
		if row == height-1 {
			label = "  0%"
		} else if height > 4 && row == height/2 {
			label = " 50%"
		}
		out[row] = accent.Render(string(content)) + " " + muted.Render(label)
	}
	return out
}

func nextIO(items []model.MonitorIO, current string) string {
	_, index, found := selectedIO(items, current)
	if !found {
		return ""
	}
	return items[(index+1)%len(items)].ID
}
