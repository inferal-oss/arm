package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"getarm.org/internal/model"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// guardianWatch refreshes guardian status until interrupted: a live view in
// a terminal, otherwise one JSON object per line. A failed read is reported
// and retried at the next interval rather than ending the watch.
func (a *app) guardianWatch(interval time.Duration) (int, error) {
	if a.machine() || !a.inputTTY {
		if !a.machine() {
			return 2, fmt.Errorf("the live view needs terminal input to quit; use --json for JSON lines")
		}
		enc := json.NewEncoder(a.out)
		for {
			var s model.GuardianStatus
			if err := a.call("guardian_status", nil, &s); err != nil {
				if a.ctx.Err() != nil {
					return 0, nil
				}
				if err := enc.Encode(map[string]string{"at": time.Now().UTC().Format(time.RFC3339Nano), "error": err.Error()}); err != nil {
					return 1, err
				}
			} else if err := enc.Encode(s); err != nil {
				return 1, err
			}
			select {
			case <-a.ctx.Done():
				return 0, nil
			case <-time.After(interval):
			}
		}
	}
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	owned := *a
	owned.ctx = ctx
	// Backend warnings would garble the full-screen view.
	owned.err = io.Discard
	m := &guardianWatchModel{a: &owned, interval: interval, width: 100, height: 40}
	opts := []tea.ProgramOption{tea.WithContext(a.ctx), tea.WithInput(a.in), tea.WithOutput(a.out), tea.WithoutSignalHandler()}
	if !a.colors {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}
	if _, err := tea.NewProgram(m, opts...).Run(); err != nil && a.ctx.Err() == nil {
		return 1, err
	}
	return 0, nil
}

type guardianReadMsg struct {
	status model.GuardianStatus
	err    error
	at     time.Time
}
type guardianTickMsg struct{}

type guardianWatchModel struct {
	a             *app
	interval      time.Duration
	read          *guardianReadMsg
	width, height int
}

func (m *guardianWatchModel) fetch() tea.Msg {
	var s model.GuardianStatus
	err := m.a.call("guardian_status", nil, &s)
	return guardianReadMsg{status: s, err: err, at: time.Now()}
}

func (m *guardianWatchModel) Init() tea.Cmd { return m.fetch }

func (m *guardianWatchModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		}
	case guardianReadMsg:
		m.read = &msg
		return m, tea.Tick(m.interval, func(time.Time) tea.Msg { return guardianTickMsg{} })
	case guardianTickMsg:
		return m, m.fetch
	}
	return m, nil
}

func (m *guardianWatchModel) View() tea.View {
	var body string
	switch {
	case m.read == nil:
		body = "Reading guardian status…\n"
	case m.read.err != nil:
		body = m.a.paint("1;31", "Guardian status unavailable: "+m.read.err.Error()) + "\nRetrying every " + m.interval.String() + ".\n"
	default:
		body = m.a.guardianText(m.read.status)
	}
	footer := "Refreshing every " + m.interval.String()
	if m.read != nil {
		footer += " · updated " + m.read.at.Format(time.TimeOnly)
	}
	footer += " · q quits"
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	// Keep the footer visible: clip the body to the terminal.
	if limit := max(3, m.height-3); len(lines) > limit {
		lines = append(lines[:limit-1], "…")
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, max(20, m.width-2), "…")
	}
	return tea.NewView(lipgloss.NewStyle().Padding(0, 1).Render(strings.Join(lines, "\n") + "\n\n" + lipgloss.NewStyle().Faint(true).Render(footer)))
}
