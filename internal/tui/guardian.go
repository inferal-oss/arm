package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
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

// guardianLadderLines shows the ladder as steps in order, each with its
// minimum level and delay, wrapped to width.
func guardianLadderLines(ladder []model.GuardianStepRule, width int) []string {
	if len(ladder) == 0 {
		return []string{muted.Render("no steps")}
	}
	arrow := muted.Render(" › ")
	lines, line := []string{}, ""
	for i, r := range ladder {
		step := strong.Render(r.Step) + " " + pressureStyle(r.Level).Render("≥"+r.Level) + muted.Render(" "+r.After.Round(time.Second).String())
		if i < len(ladder)-1 {
			step += arrow
		}
		if line != "" && ansi.StringWidth(line)+ansi.StringWidth(step) > width {
			lines, line = append(lines, line), ""
		}
		line += step
	}
	return append(lines, line)
}

func guardianTone(v model.GuardianVerdict) string {
	switch {
	case v.Level == model.PressureUnknown:
		return ""
	case v.Partial:
		return "warning"
	case v.Level != model.PressureNormal:
		return pressureTone(v.Level)
	}
	return "accent"
}

// guardianBadge names the verdict in a reversed label, and returns the rest
// of its sentence.
func guardianBadge(v model.GuardianVerdict) (string, string) {
	label, style := "ALL CLEAR", accent
	switch {
	case v.Level == model.PressureUnknown:
		label, style = "UNKNOWN", muted
	case v.Partial:
		label, style = "PARTLY UNKNOWN", lipgloss.NewStyle().Foreground(colorWarning)
	case v.Level != model.PressureNormal:
		label, style = "UNDER PRESSURE", pressureStyle(v.Level)
	}
	text := v.Text
	if _, rest, ok := strings.Cut(text, ": "); ok {
		text = strings.ToUpper(rest[:1]) + rest[1:]
	}
	return style.Bold(true).Reverse(true).Render(" " + label + " "), text
}

var guardianModeHint = map[string]string{
	model.GuardianObserve: "records steps, runs none",
	model.GuardianAdvise:  "notifies and asks only",
	model.GuardianAct:     "runs every step",
}

// guardianSummary is the status window: the verdict, the guardian's mode,
// role and hourly budget, pressure per resource, and the ladder.
func (m *dashboardModel) guardianSummary(g *model.GuardianStatus, inner int) string {
	badge, text := guardianBadge(model.GuardianVerdictOf(*g))
	updated := "updated " + g.At.Local().Format("15:04:05")
	if g.At.IsZero() {
		updated = "no reading yet"
	}
	warn := lipgloss.NewStyle().Foreground(colorWarning)
	if m.guardianErr != "" {
		updated = warn.Render("stale · " + short(m.guardianErr, 40))
	} else {
		updated = muted.Render(updated)
	}
	lines := []string{align(badge+"  "+strong.Render(text), updated, inner), ""}

	// The guardian's facts sit beside pressure per resource; narrow
	// windows stack them.
	cell := func(label, value string, width int) string {
		return fitBlock(muted.Render(fmt.Sprintf("%-8s", label))+value, width, 1)
	}
	mode := strong.Render(g.Mode)
	if hint := guardianModeHint[g.Mode]; hint != "" {
		mode += muted.Render(" · " + hint)
	}
	role := strong.Render(g.Role) + muted.Render(map[string]string{model.GuardianManned: " · operator present", model.GuardianUnmanned: " · unattended"}[g.Role])
	used, limit := float64(g.Budget.Used), float64(g.Budget.Limit)
	budget := bar(&used, &limit, 8, "") + fmt.Sprintf(" %d/%d", g.Budget.Used, g.Budget.Limit) + muted.Render(" this hour")
	facts := [][2]string{{"MODE", mode}, {"HOST", role}, {"ACTIONS", budget}}
	pressure := [][2]string{}
	for _, r := range []struct {
		name string
		p    model.PressureReading
	}{{"CPU", g.Pressure.CPU}, {"MEMORY", g.Pressure.Memory}, {"I/O", g.Pressure.IO}} {
		level := r.p.Level
		if level == "" || g.Stale {
			level = model.PressureUnknown
		}
		value := pressureStyle(level).Render(strings.ToUpper(level))
		if r.p.Reason != "" && level != model.PressureUnknown {
			value += muted.Render(" · " + r.p.Reason)
		}
		pressure = append(pressure, [2]string{r.name, value})
	}
	if inner < 72 {
		for _, c := range append(facts, pressure...) {
			lines = append(lines, cell(c[0], c[1], inner))
		}
	} else {
		left := inner / 2
		for i := range facts {
			lines = append(lines, cell(facts[i][0], facts[i][1], left-2)+"  "+cell(pressure[i][0], pressure[i][1], inner-left))
		}
	}
	lines = append(lines, "")

	if g.Mode == model.GuardianOff {
		lines = append(lines, muted.Render("The guardian observes nothing and acts on nothing."), "Enable it: "+accent.Render("arm config set guardian.enabled true")+muted.Render(", then restart the backend."))
	} else {
		ladder := guardianLadderLines(g.Ladder, max(1, inner-8))
		for i, l := range ladder {
			label := "        "
			if i == 0 {
				label = muted.Render("LADDER  ")
			}
			lines = append(lines, label+l)
		}
		lines = append(lines, muted.Render("        change it with ")+accent.Render("arm guardian ladder edit"))
	}
	if g.Inventory != "" {
		lines = append(lines, warn.Render(short("Inventory: "+g.Inventory, inner)))
	}
	if g.Stale || m.guardianErr != "" {
		lines = append(lines, warn.Render("Pressure is stale or unavailable; the guardian holds all actions."))
	}
	return strings.Join(lines, "\n")
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
	sections := []string{window("HOST GUARDIAN", m.guardianSummary(g, inner), width, 0, false, guardianTone(model.GuardianVerdictOf(*g)))}
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
