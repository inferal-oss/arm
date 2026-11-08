package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"getarm.org/internal/model"
)

func pressureStyle(level string) lipgloss.Style {
	switch level {
	case model.PressureNormal:
		return accent
	case model.PressureElevated:
		return lipgloss.NewStyle().Foreground(colorWarning)
	case model.PressureHigh:
		return lipgloss.NewStyle().Bold(true).Foreground(colorWarning)
	case model.PressureCritical:
		return lipgloss.NewStyle().Bold(true).Foreground(colorDanger)
	}
	return muted
}

func pressureTone(levels ...string) string {
	worst := -1
	for _, l := range levels {
		if r, ok := model.PressureRank(l); ok && r > worst {
			worst = r
		}
	}
	switch {
	case worst >= 3:
		return "danger"
	case worst >= 1:
		return "warning"
	}
	return "accent"
}

// pressureWindow shows CPU, memory and I/O pressure levels with their
// strongest evidence, and the host guardian's latest state.
func (m *dashboardModel) pressureWindow(s *model.MonitorSnapshot, width int) string {
	inner := width - 4
	stale := m.monitorStale()
	cells := []string{}
	levels := []string{}
	for _, r := range []struct {
		name string
		p    model.PressureReading
	}{{"CPU", s.Pressure.CPU}, {"MEMORY", s.Pressure.Memory}, {"I/O", s.Pressure.IO}} {
		level := r.p.Level
		if level == "" || stale {
			level = model.PressureUnknown
		}
		levels = append(levels, level)
		cell := muted.Render(r.name+" ") + pressureStyle(level).Render(strings.ToUpper(level))
		if r.p.Reason != "" && level != model.PressureUnknown && level != model.PressureNormal {
			cell += muted.Render(" · " + r.p.Reason)
		}
		cells = append(cells, fitBlock(cell, inner/3, 1))
	}
	lines := []string{strings.Join(cells, "")}
	g := m.data.guardian
	switch {
	case g == nil:
	case g.Mode == model.GuardianOff:
		lines = append(lines, muted.Render("Host guardian off · enable: arm config set guardian.enabled true"))
	default:
		line := fmt.Sprintf("Guardian %s · %s · %d/%d actions this hour", g.Mode, g.Role, g.Budget.Used, g.Budget.Limit)
		if len(g.Incidents) > 0 {
			i := g.Incidents[0]
			line += fmt.Sprintf(" · %s incident since %s", i.Resource, i.Since.Local().Format("15:04:05"))
		}
		if len(g.Actions) > 0 {
			a := g.Actions[0]
			line += fmt.Sprintf(" · last: %s %s (%s)", a.Step, a.GroupName, a.Outcome)
		}
		lines = append(lines, fitBlock(line, inner, 1))
	}
	return window("PRESSURE", strings.Join(lines, "\n"), width, 0, false, pressureTone(levels...))
}
