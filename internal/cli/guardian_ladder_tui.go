package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/guardian"
	"getarm.org/internal/model"
	"github.com/charmbracelet/colorprofile"
)

type ladderRow struct {
	step  string
	on    bool
	level string
	after time.Duration
}

// ladderEditor edits every step in its required order, so any selection is
// a valid ladder. Following the role default writes no ladder at all.
type ladderEditor struct {
	report  guardianLadderReport
	rows    []ladderRow
	cursor  int
	custom  bool // false follows the role default
	dirty   bool
	editing bool
	input   textinput.Model
	notice  string
	discard bool // a second quit discards unsaved changes
	save    bool
	width   int
}

func newLadderEditor(r guardianLadderReport) *ladderEditor {
	in := textinput.New()
	in.Prompt = "delay: "
	in.CharLimit = 12
	m := &ladderEditor{report: r, custom: r.Source == "config", input: in, width: 80}
	m.load(r.Ladder)
	return m
}

func (m *ladderEditor) load(ladder []model.GuardianStepRule) {
	m.rows = m.rows[:0]
	for _, s := range guardian.Steps() {
		rule, on := ladderDefault(m.report.Role, s), false
		if i := ladderIndex(ladder, s); i >= 0 {
			rule, on = ladder[i], true
		}
		m.rows = append(m.rows, ladderRow{step: s, on: on, level: rule.Level, after: rule.After})
	}
}

func (m *ladderEditor) ladder() []model.GuardianStepRule {
	out := []model.GuardianStepRule{}
	for _, r := range m.rows {
		if r.on {
			out = append(out, model.GuardianStepRule{Step: r.step, Level: r.level, After: r.after})
		}
	}
	return out
}

func (m *ladderEditor) changed() { m.custom, m.dirty, m.discard, m.notice = true, true, false, "" }

func (m *ladderEditor) Init() tea.Cmd { return nil }

func (m *ladderEditor) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.save = false
			return m, tea.Quit
		}
		if m.editing {
			return m.updateDelay(msg)
		}
		row := &m.rows[m.cursor]
		switch msg.String() {
		case "up", "k":
			m.cursor = (m.cursor + len(m.rows) - 1) % len(m.rows)
		case "down", "j":
			m.cursor = (m.cursor + 1) % len(m.rows)
		case "space", "x":
			row.on = !row.on
			m.changed()
		case "left", "h", "right", "l":
			step := 1
			if k := msg.String(); k == "left" || k == "h" {
				step = -1
			}
			i := slices.Index(ladderLevels, row.level) + step
			row.level = ladderLevels[min(max(i, 0), len(ladderLevels)-1)]
			m.changed()
		case "+", "=", "-", "_", "]", "[":
			delta := map[string]time.Duration{"+": 5 * time.Second, "=": 5 * time.Second, "-": -5 * time.Second, "_": -5 * time.Second, "]": time.Minute, "[": -time.Minute}[msg.String()]
			row.after = min(max(row.after+delta, 0), ladderMaxDelay)
			m.changed()
		case "enter", "e":
			m.editing = true
			m.input.SetValue(daemonconfig.DurationText(row.after))
			m.input.CursorEnd()
			return m, m.input.Focus()
		case "m", "u":
			role := model.GuardianManned
			if msg.String() == "u" {
				role = model.GuardianUnmanned
			}
			m.load(guardian.DefaultLadder(role))
			m.changed()
			m.notice = "Loaded the " + role + " preset."
		case "d":
			m.load(guardian.DefaultLadder(m.report.Role))
			m.custom, m.dirty, m.discard = false, true, false
			m.notice = "Following the " + m.report.Role + " default; saving removes the custom ladder."
		case "s", "ctrl+s":
			if m.custom && len(m.ladder()) == 0 {
				m.notice = "Enable at least one step, or set guardian.mode observe to record without acting."
				return m, nil
			}
			m.save = true
			return m, tea.Quit
		case "q", "esc":
			if m.dirty && !m.discard {
				m.discard = true
				m.notice = "Unsaved changes. Press q again to discard, or s to save."
				return m, nil
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *ladderEditor) updateDelay(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editing = false
		m.input.Blur()
		return m, nil
	case "enter":
		d, err := parseLadderDelay(m.input.Value())
		if err != nil {
			m.notice = err.Error()
			return m, nil
		}
		m.rows[m.cursor].after = d
		m.editing = false
		m.input.Blur()
		m.changed()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *ladderEditor) View() tea.View {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	selected := lipgloss.NewStyle().Reverse(true)
	dim := lipgloss.NewStyle().Faint(true)
	var b strings.Builder
	fmt.Fprintln(&b, title.Render("ARM · host guardian ladder"))
	source := "custom ladder"
	if !m.custom {
		source = "default for " + m.report.Role + " hosts"
	}
	if m.dirty {
		source += " · unsaved"
	}
	fmt.Fprintf(&b, "Role %s · mode %s · %s\n\n", m.report.Role, m.report.Mode, source)
	fmt.Fprintf(&b, "      %-13s %-9s %7s  %s\n", "STEP", "LEVEL", "AFTER", "EFFECT")
	for i, r := range m.rows {
		box := "[ ]"
		if r.on {
			box = "[x]"
		}
		line := fmt.Sprintf("  %s %-13s %-9s %7s  %s", box, r.step, r.level, daemonconfig.DurationText(r.after), ladderEffects[r.step])
		if w := max(20, m.width-2); len([]rune(line)) > w {
			line = string([]rune(line)[:w-1]) + "…"
		}
		switch {
		case i == m.cursor:
			line = selected.Render(line)
		case !r.on:
			line = dim.Render(line)
		}
		fmt.Fprintln(&b, line)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, dim.Render("Each step runs once its pressure level has lasted its delay, in this order."))
	if m.editing {
		fmt.Fprintln(&b, m.input.View()+dim.Render("  enter applies · esc cancels"))
	}
	for _, w := range ladderWarnings(m.report.Mode, m.report.Role, m.report.Enabled, m.ladder()) {
		fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(w))
	}
	if m.notice != "" {
		fmt.Fprintln(&b, m.notice)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, dim.Render("↑↓ select · space toggle · ←→ level · +/- 5s · [/] 1m · enter type delay"))
	fmt.Fprintln(&b, dim.Render("m/u manned/unmanned preset · d role default · s save · q quit"))
	return tea.NewView(lipgloss.NewStyle().Padding(1, 1).Render(b.String()))
}

func (a *app) guardianLadderEdit(rep guardianLadderReport, dryRun bool, expect string) (int, error) {
	if a.machine() || !a.inputTTY || !a.outputTTY {
		return 2, fmt.Errorf("guardian ladder edit needs a terminal; use guardian ladder set, remove, preset or reset")
	}
	m := newLadderEditor(rep)
	opts := []tea.ProgramOption{tea.WithContext(a.ctx), tea.WithInput(a.in), tea.WithOutput(a.out), tea.WithoutSignalHandler()}
	if !a.colors {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}
	if _, err := tea.NewProgram(m, opts...).Run(); err != nil {
		return 1, err
	}
	if !m.save || !m.dirty {
		fmt.Fprintln(a.out, "No changes saved.")
		return 0, nil
	}
	code, err := a.saveGuardianLadder(rep, m.ladder(), !m.custom, dryRun, expect)
	if err != nil {
		// Keep the work visible so it can be reapplied.
		fmt.Fprintln(a.out, "Not saved. The intended ladder was:")
		if !m.custom {
			fmt.Fprintln(a.out, "  the "+rep.Role+" default (arm guardian ladder reset)")
		}
		for _, r := range m.ladder() {
			if m.custom {
				fmt.Fprintf(a.out, "  %-13s %-9s %s\n", r.Step, r.Level, daemonconfig.DurationText(r.After))
			}
		}
	}
	return code, err
}
