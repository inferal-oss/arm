package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/model"
)

const runDisplayLimit = 256 * 1024

type runsDashboard struct {
	records                           []model.Run
	selected                          *model.Run
	data                              []byte
	offset                            int64
	following, done, displayCut, busy bool
	generation                        uint64
	err                               string
	cancel                            context.CancelFunc
}
type runsReadMsg struct {
	ui         *runsDashboard
	session    transport
	generation uint64
	records    []model.Run
	page       *model.RunLog
	err        error
}

func (m *dashboardModel) runsVisible() bool {
	return m.view == 7 && m.nodeUI == nil && m.record == nil && !m.feedFocus && m.inputMode == ""
}
func (m *dashboardModel) retireRuns() {
	if m.runsUI == nil {
		return
	}
	ui := m.runsUI
	ui.generation++
	if ui.cancel != nil {
		ui.cancel()
		ui.cancel = nil
	}
	ui.busy = false
}
func (m *dashboardModel) retireRunsIfHidden() {
	if !m.runsVisible() || !m.connected {
		m.retireRuns()
	}
}
func (m *dashboardModel) readRuns(manual bool) tea.Cmd {
	if !m.runsVisible() || !m.connected || m.session == nil {
		return nil
	}
	if m.runsUI == nil {
		m.runsUI = &runsDashboard{}
	}
	ui := m.runsUI
	if ui.busy {
		return nil
	}
	ui.busy = true
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	ui.cancel = cancel
	session, generation := m.session, ui.generation
	id, offset := "", ui.offset
	if ui.selected != nil && (manual || ui.following && !ui.done) {
		id = ui.selected.ID
	}
	return func() tea.Msg {
		defer cancel()
		msg := runsReadMsg{ui: ui, session: session, generation: generation}
		msg.err = session.Call(ctx, "runs", nil, &msg.records)
		if msg.err == nil && id != "" {
			var page model.RunLog
			msg.err = session.Call(ctx, "run_output", map[string]any{"id": id, "offset": offset}, &page)
			msg.page = &page
		}
		return msg
	}
}
func (m *dashboardModel) acceptRunsRead(msg runsReadMsg) tea.Cmd {
	ui := m.runsUI
	if ui == nil || ui != msg.ui || ui.generation != msg.generation || msg.session != m.session {
		return nil
	}
	ui.busy = false
	ui.cancel = nil
	if !m.runsVisible() || !m.connected {
		return nil
	}
	if msg.err != nil {
		ui.err = msg.err.Error()
		return nil
	}
	id, previous := m.selectionID(), m.selected
	ui.records = msg.records
	ui.err = ""
	m.restoreSelection(id, previous)
	if page := msg.page; page != nil && ui.selected != nil {
		if page.Run.ID != ui.selected.ID || page.Offset != ui.offset || page.NextOffset != ui.offset+int64(len(page.Data)) {
			ui.err = "Run output identity or byte cursor changed"
			return nil
		}
		ui.data = append(ui.data, page.Data...)
		ui.offset = page.NextOffset
		run := page.Run
		ui.selected = &run
		ui.done = page.Done && page.NextOffset >= page.Run.LogBytes
		if len(ui.data) > runDisplayLimit {
			ui.data = ui.data[len(ui.data)-runDisplayLimit:]
			for len(ui.data) > 0 && !utf8.RuneStart(ui.data[0]) {
				ui.data = ui.data[1:]
			}
			ui.displayCut = true
		}
		if ui.following {
			m.syncViewport()
			m.viewport.GotoBottom()
		}
		// Drain a saved log one bounded page at a time, without waiting a full tick.
		if ui.following && len(page.Data) > 0 && !ui.done {
			return m.readRuns(false)
		}
	}
	return nil
}
func (m *dashboardModel) filteredRuns() []model.Run {
	if m.runsUI == nil {
		return nil
	}
	var runs []model.Run
	for _, run := range m.runsUI.records {
		if strings.Contains(strings.ToLower(run.ID+" "+run.Name+" "+run.Agent+" "+run.Reason+" "+run.State+" "+strings.Join(run.Command, " ")), strings.ToLower(m.filter)) {
			runs = append(runs, run)
		}
	}
	return runs
}
func (m *dashboardModel) openRun() tea.Cmd {
	records := m.filteredRuns()
	if m.selected >= len(records) {
		return nil
	}
	m.retireRuns()
	ui := m.runsUI
	run := records[m.selected]
	ui.selected = &run
	ui.following = true
	ui.offset = 0
	ui.data = nil
	ui.done = false
	ui.displayCut = false
	m.viewport.GotoTop()
	return m.readRuns(false)
}
func safeRunOutput(data []byte, done bool) string {
	if !done && len(data) > 0 {
		start := len(data) - 1
		for start > 0 && !utf8.RuneStart(data[start]) && len(data)-start < utf8.UTFMax {
			start--
		}
		if !utf8.FullRune(data[start:]) {
			data = data[:start]
		}
	}
	lines := strings.Split(strings.ToValidUTF8(string(data), "�"), "\n")
	for i, line := range lines {
		// Bubbles soft wrapping becomes expensive for an unbounded single line.
		// Wrap presentation only; byte cursors and the durable log remain exact.
		line = safe(strings.ReplaceAll(line, "\t", "    "))
		var wrapped strings.Builder
		width := 0
		for _, r := range line {
			if width == 512 {
				wrapped.WriteByte('\n')
				width = 0
			}
			wrapped.WriteRune(r)
			width++
		}
		lines[i] = wrapped.String()
	}
	return strings.Join(lines, "\n")
}
func (m *dashboardModel) runsText() string {
	ui := m.runsUI
	if ui == nil {
		return "Reading runs…"
	}
	var out strings.Builder
	if !m.connected {
		out.WriteString("DISCONNECTED · cached run observations are stale\n\n")
	}
	if ui.err != "" {
		fmt.Fprintf(&out, "Runs unavailable: %s\n\n", safe(ui.err))
	}
	if r := ui.selected; r != nil {
		fmt.Fprintf(&out, "%s\nRun ID: %s\nCommand: %s\nAgent: %s\nReason: %s\nHost: %s\nDirectory: %s\nState: %s · %s\nCreated: %s\nStarted: %s · Ended: %s\n", safe(r.Name), safe(r.ID), safe(strings.Join(r.Command, " ")), safe(r.Agent), safe(r.Reason), safe(r.Host), safe(r.CWD), safe(r.State), safe(r.Outcome), r.Created.Local().Format(time.RFC3339), date(r.Started), date(r.Ended))
		if r.LeaseID != "" {
			fmt.Fprintf(&out, "Lease: %s\n", safe(r.LeaseID))
		} else {
			out.WriteString("Observation only · no resource lease\n")
		}
		if r.ExitCode != nil {
			fmt.Fprintf(&out, "Exit code: %d\n", *r.ExitCode)
		}
		if !r.LastReported.IsZero() {
			fmt.Fprintf(&out, "Last report: %s\n", r.LastReported.Local().Format(time.RFC3339))
		}
		if r.Outcome == "reporter_lost" {
			out.WriteString("Reporter lost; process completion is unknown.\n")
		}
		mode := "Paused"
		if ui.following {
			mode = "Following"
		}
		fmt.Fprintf(&out, "\n%s · through byte %d", mode, ui.offset)
		if ui.done {
			out.WriteString(" · end of saved log")
		}
		out.WriteString("\n[f] Follow/pause · [Home] Read from start · [n] Next page · [l] Lease · [Esc] Runs\n")
		if ui.displayCut {
			out.WriteString("Display holds latest 256 KiB; Home reads earlier output.\n")
		}
		if r.Truncated {
			out.WriteString("Saved log reached its retention limit.\n")
		}
		out.WriteString("Long lines wrap in this view.\n\n")
		out.WriteString(safeRunOutput(ui.data, ui.done))
		if len(ui.data) == 0 {
			out.WriteString("(No output yet)")
		}
		return out.String()
	}
	out.WriteString("Current and past runs · Enter follows output\n\n")
	for i, r := range m.filteredRuns() {
		fmt.Fprintf(&out, "%s%s · %s\n  %s\n  %s · %s\n\n", selectedPrefix(m.selected, i), short(r.Name, max(12, m.mainWidth()-30)), safe(r.State), short(strings.Join(r.Command, " "), max(1, m.mainWidth()-8)), short(r.Agent, 30), short(r.Reason, max(1, m.mainWidth()-42)))
	}
	if len(m.filteredRuns()) == 0 {
		out.WriteString("No matching runs.")
	}
	return out.String()
}
func (m *dashboardModel) runKey(k string) tea.Cmd {
	ui := m.runsUI
	if ui == nil || ui.selected == nil {
		return nil
	}
	switch k {
	case "esc":
		m.retireRuns()
		ui.selected = nil
		m.viewport.GotoTop()
		return m.readRuns(false)
	case "f", "end":
		m.retireRuns()
		ui.following = !ui.following
		if k == "end" {
			ui.following = true
		}
		if ui.following {
			m.syncViewport()
			m.viewport.GotoBottom()
			return m.readRuns(false)
		}
	case "home":
		m.retireRuns()
		ui.offset = 0
		ui.data = nil
		ui.done = false
		ui.displayCut = false
		ui.following = false
		m.viewport.GotoTop()
		return m.readRuns(true)
	case "n":
		if !ui.following && !ui.done {
			return m.readRuns(true)
		}
	case "r":
		return m.readRuns(!ui.following)
	case "l":
		if ui.selected.LeaseID != "" {
			id := ui.selected.LeaseID
			m.retireRuns()
			ui.selected = nil
			return m.loadDetail(id)
		}
	case "i":
		m.record, _ = newRecord(*ui.selected, "Run")
		m.viewport.GotoTop()
	default:
		if scrollReview(&m.viewport, k) {
			ui.following = false
		}
	}
	return nil
}
