package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"getarm.org/internal/model"
)

// viewGuardian is the Guardian page. It is listed beside Monitor but keeps
// the last index, so the number keys of the other views do not move.
const viewGuardian = 8

// guardianPoll is how often a visible Guardian page reads the guardian.
const guardianPoll = 2 * time.Second

type guardianReadMsg struct {
	session transport
	status  *model.GuardianStatus
	err     error
}

type guardianUndoMsg struct {
	session transport
	action  model.GuardianAction
	err     error
}

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

// readGuardian reads guardian status when the visible Guardian page is due.
func (m *dashboardModel) readGuardian(force bool) tea.Cmd {
	if m.view != viewGuardian || m.nodeUI != nil || !m.connected || m.guardianLoading {
		return nil
	}
	if !force && m.timeNow.Sub(m.guardianRead) < guardianPoll {
		return nil
	}
	m.guardianLoading = true
	ctx, session := m.ctx, m.session
	return func() tea.Msg {
		var g model.GuardianStatus
		if err := session.Call(ctx, "guardian_status", nil, &g); err != nil {
			return guardianReadMsg{session: session, err: err}
		}
		return guardianReadMsg{session: session, status: &g}
	}
}

func (m *dashboardModel) acceptGuardianRead(msg guardianReadMsg) {
	if msg.session != m.session {
		return
	}
	m.guardianLoading = false
	m.guardianRead = m.timeNow
	m.guardianErr = ""
	if msg.err != nil {
		m.guardianErr = msg.err.Error()
		return
	}
	id, previous := m.selectionID(), m.selected
	m.data.guardian = msg.status
	if m.view == viewGuardian {
		m.restoreSelection(id, previous)
	}
}

func (m *dashboardModel) guardianActions() []model.GuardianAction {
	if m.data.guardian == nil {
		return nil
	}
	return m.data.guardian.Actions
}

// guardianKey handles the Guardian page's own keys. Undo asks once: x
// selects the action, y sends it, and any other key keeps it unchanged.
func (m *dashboardModel) guardianKey(k string, armed *model.GuardianAction) (tea.Cmd, bool) {
	if armed != nil {
		action := *armed
		if k != "y" {
			m.notice = "Guardian action kept unchanged."
			return nil, k == "n" || k == "esc"
		}
		m.notice = "Undoing " + action.Step + " " + safe(action.GroupName) + "…"
		ctx, session := m.ctx, m.session
		return func() tea.Msg {
			var undone model.GuardianAction
			err := session.Call(ctx, "guardian_undo", model.GuardianUndoRequest{Action: action.ID}, &undone)
			return guardianUndoMsg{session: session, action: action, err: err}
		}, true
	}
	switch k {
	case "x":
		actions := m.guardianActions()
		if m.selected >= len(actions) {
			return nil, true
		}
		a := actions[m.selected]
		if !model.GuardianUndoable(a) {
			m.notice = "This action has nothing left to undo."
			return nil, true
		}
		m.guardianConfirm = &a
		m.notice = fmt.Sprintf("Undo %s on %s? The program is then spared for the rest of the incident. [y] undo · any other key keeps it", a.Step, safe(orDash(a.GroupName)))
		return nil, true
	case "r":
		return m.readGuardian(true), true
	}
	return nil, false
}

func (m *dashboardModel) acceptGuardianUndo(msg guardianUndoMsg) tea.Cmd {
	if msg.session != m.session {
		return nil
	}
	if msg.err != nil {
		m.notice = "Undo failed: " + safe(msg.err.Error())
	} else {
		m.notice = "Reverted " + msg.action.Step + " on " + safe(orDash(msg.action.GroupName)) + "."
		// Until the next reading arrives, the action shows nothing to undo.
		if g := m.data.guardian; g != nil {
			now := m.timeNow
			for i := range g.Actions {
				if g.Actions[i].ID == msg.action.ID {
					g.Actions[i].RevertedAt = &now
				}
			}
		}
	}
	return m.readGuardian(true)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func guardianNumber(v *float64, unit string) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprintf("%.1f%s", *v, unit)
}

func guardianBytes(v *uint64) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprintf("%.1f GiB", float64(*v)/(1<<30))
}

func guardianRate(v *float64) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprintf("%.1f MiB/s", *v/(1<<20))
}

func guardianLadderLine(ladder []model.GuardianStepRule) string {
	if len(ladder) == 0 {
		return "no steps"
	}
	parts := make([]string, 0, len(ladder))
	for _, r := range ladder {
		parts = append(parts, fmt.Sprintf("%s %s %s", r.Step, r.Level, r.After.Round(time.Second)))
	}
	return strings.Join(parts, " → ")
}

// guardianText is the Guardian page: a verdict, pressure, the guardian's
// state and ladder, open incidents, the heaviest programs and recent actions.
func (m *dashboardModel) guardianText() string {
	width := m.mainWidth()
	inner := width - 4
	g := m.data.guardian
	if g == nil {
		body := "Waiting for the host guardian…"
		if m.guardianErr != "" {
			body = "Guardian status unavailable: " + safe(m.guardianErr) + "\n\nThis backend may predate the host guardian."
		}
		return window("HOST GUARDIAN", body, width, 0, false, "")
	}
	stale := g.Stale || m.guardianErr != ""
	v := model.GuardianVerdictOf(*g)
	tone, style := "accent", accent.Bold(true)
	switch {
	case v.Level == model.PressureUnknown:
		tone, style = "", muted
	case v.Partial:
		tone, style = "warning", lipgloss.NewStyle().Foreground(colorWarning)
	case v.Level != model.PressureNormal:
		tone, style = pressureTone(v.Level), pressureStyle(v.Level)
	}
	updated := "updated " + g.At.Local().Format("15:04:05")
	if g.At.IsZero() {
		updated = "no reading yet"
	}
	if m.guardianErr != "" {
		updated = "stale · " + short(m.guardianErr, 40)
	}
	lines := []string{align(style.Render(v.Text), muted.Render(updated), inner)}
	if g.Mode == model.GuardianOff {
		lines = append(lines, muted.Render("Guardian off · it observes nothing and acts on nothing."), muted.Render("Enable: arm config set guardian.enabled true, then restart the backend."))
	} else {
		lines = append(lines, fmt.Sprintf("Mode %s · %s host · %d of %d process actions this hour", g.Mode, g.Role, g.Budget.Used, g.Budget.Limit))
		if g.Mode == model.GuardianObserve {
			lines = append(lines, muted.Render("Observe mode records steps as planned and runs none of them."))
		}
		lines = append(lines, muted.Render("Ladder ")+fitBlock(guardianLadderLine(g.Ladder), max(1, inner-7), 1))
		lines = append(lines, muted.Render("Change it: arm guardian ladder edit"))
	}
	if g.Inventory != "" {
		lines = append(lines, muted.Render("Inventory: "+short(g.Inventory, inner-11)))
	}
	if stale {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorWarning).Render("Pressure is stale or unavailable; the guardian holds all actions."))
	}
	sections := []string{window("HOST GUARDIAN", strings.Join(lines, "\n"), width, 0, false, tone)}

	// Pressure comes from the guardian's own reading so the page agrees with
	// its verdict even when the monitor stream is behind.
	cells := []string{}
	for _, r := range []struct {
		name string
		p    model.PressureReading
	}{{"CPU", g.Pressure.CPU}, {"MEMORY", g.Pressure.Memory}, {"I/O", g.Pressure.IO}} {
		level := r.p.Level
		if level == "" || g.Stale {
			level = model.PressureUnknown
		}
		cell := muted.Render(fmt.Sprintf("%-7s", r.name)) + pressureStyle(level).Render(strings.ToUpper(level))
		if r.p.Reason != "" && level != model.PressureUnknown {
			cell += muted.Render(" · " + r.p.Reason)
		}
		cells = append(cells, fitBlock(cell, inner, 1))
	}
	sections = append(sections, window("PRESSURE", strings.Join(cells, "\n"), width, 0, false, pressureTone(g.Pressure.CPU.Level, g.Pressure.Memory.Level, g.Pressure.IO.Level)))

	if len(g.Incidents) > 0 {
		rows := []string{}
		for _, i := range g.Incidents {
			row := fmt.Sprintf("%-7s %s since %s · peak %s · last step %s", i.Resource, pressureStyle(i.Level).Render(strings.ToUpper(i.Level)), i.Since.Local().Format("15:04:05"), i.Peak, orDash(i.Step))
			if i.Recovering != nil {
				row += muted.Render(" · recovering since " + i.Recovering.Local().Format("15:04:05"))
			}
			rows = append(rows, fitBlock(row, inner, 1))
		}
		sections = append(sections, window("INCIDENTS", strings.Join(rows, "\n"), width, 0, false, "warning"))
	}

	name := max(12, min(30, inner-48))
	rows := []string{muted.Render(fmt.Sprintf("%-*s %7s %10s %11s %5s  %s", name, "PROGRAM", "CPU", "MEMORY", "DISK", "PROCS", "NOTES"))}
	for _, p := range g.Groups {
		notes := []string{}
		if p.Protected != "" {
			notes = append(notes, "protected: "+p.Protected)
		}
		if p.Managed {
			notes = append(notes, "ARM-managed")
		}
		if p.Agent != "" {
			notes = append(notes, "agent "+p.Agent)
		}
		notes = append(notes, p.State...)
		rows = append(rows, fitBlock(fmt.Sprintf("%-*s %7s %10s %11s %5d  %s", name, short(p.Name, name), guardianNumber(p.CPUCores, ""), guardianBytes(p.MemoryBytes), guardianRate(p.DiskBytesPerSecond), p.Processes, safe(strings.Join(notes, "; "))), inner, 1))
	}
	if len(g.Groups) == 0 {
		rows = append(rows, muted.Render("No program activity sampled yet."))
	}
	sections = append(sections, window("HEAVIEST PROGRAMS · CPU CORES", strings.Join(rows, "\n"), width, 0, false, ""))

	rows = rows[:0]
	for i, a := range g.Actions {
		outcome := a.Outcome
		switch a.Outcome {
		case model.GuardianFailed:
			outcome = lipgloss.NewStyle().Foreground(colorDanger).Render(outcome)
		case model.GuardianApplied, model.GuardianPartial:
			outcome = accent.Render(outcome)
		default:
			outcome = muted.Render(outcome)
		}
		row := fmt.Sprintf("%s%s %-12s %-9s %s — %s", selectedPrefix(m.selected, i), a.At.Local().Format("15:04:05"), a.Step, outcome, safe(orDash(a.GroupName)), safe(a.Reason))
		if model.GuardianUndoable(a) {
			row += lipgloss.NewStyle().Foreground(colorBlue).Render("  [x] undo")
		}
		rows = append(rows, fitBlock(row, inner, 1))
	}
	if len(g.Actions) == 0 {
		rows = append(rows, muted.Render("No guardian actions recorded."))
	}
	sections = append(sections, window("RECENT ACTIONS · ↑↓ select · Enter inspect", strings.Join(rows, "\n"), width, 0, false, ""))
	return strings.Join(sections, "\n")
}
