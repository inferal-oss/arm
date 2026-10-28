package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func activeRunRecord(run model.Run) bool {
	switch run.State {
	case "active", "queued", "provisioning", "ending", "reclaiming", "needs_attention":
		return true
	}
	return false
}
func currentRunRecord(run model.Run) bool {
	return activeRunRecord(run) || run.Outcome == "reporter_lost" || run.State == "interrupted" && run.ExitCode == nil
}
func runStatusLabel(run model.Run) string {
	switch run.State {
	case "active":
		return "Running"
	case "queued":
		return "Queued"
	case "provisioning":
		return "Starting"
	case "ending":
		return "Stopping"
	case "reclaiming":
		return "Cleaning up"
	case "needs_attention":
		return "Needs attention"
	}
	if run.Outcome == "log_failed" {
		return "Log failed"
	}
	if run.Outcome == "reporter_lost" {
		return "Reporter lost"
	}
	if run.State == "interrupted" {
		return "Interrupted"
	}
	switch run.Outcome {
	case "expired":
		return "Expired"
	case "cancelled":
		return "Cancelled"
	case "revoked", "released":
		return "Stopped"
	case "failed", "launch_failed", "admission_failed":
		return "Failed"
	}
	if run.ExitCode != nil && *run.ExitCode != 0 {
		return "Failed"
	}
	if run.State == "terminal" {
		if run.Outcome == "completed" || run.ExitCode != nil && *run.ExitCode == 0 && run.Outcome == "" {
			return "Completed"
		}
		return "Finished"
	}
	return "Unknown"
}
func runListTime(run model.Run, now time.Time) string {
	at := run.Created
	if run.Started != nil {
		at = *run.Started
	}
	if !activeRunRecord(run) {
		if run.Ended != nil {
			at = *run.Ended
		} else if !run.LastReported.IsZero() {
			at = run.LastReported
		}
	}
	if at.IsZero() {
		return "—"
	}
	age := max(time.Duration(0), now.Sub(at))
	seconds := int(age.Seconds())
	label := fmt.Sprintf("%ds", seconds)
	if seconds >= 3600 {
		label = fmt.Sprintf("%dh", seconds/3600)
	} else if seconds >= 60 {
		label = fmt.Sprintf("%dm", seconds/60)
	}
	if activeRunRecord(run) {
		return label + " elapsed"
	}
	if seconds < 10 {
		return "just now"
	}
	if age >= 24*time.Hour {
		return at.Local().Format("Jan 2 15:04")
	}
	return label + " ago"
}
func runBase(value string) string {
	parts := strings.Split(strings.ReplaceAll(value, `\`, "/"), "/")
	if len(parts) > 0 && parts[len(parts)-1] != "" {
		return parts[len(parts)-1]
	}
	return value
}
func runListCommand(run model.Run) string {
	var parts []string
	for i, arg := range run.Command {
		if i == 0 {
			arg = runBase(arg)
		} else if len(arg) > 52 && strings.ContainsAny(arg, `/\`) {
			arg = "…/" + runBase(arg)
		}
		parts = append(parts, strings.Join(strings.Fields(arg), " "))
	}
	if len(parts) == 0 {
		return "Command unavailable"
	}
	return strings.Join(parts, " ")
}
func runListPurpose(run model.Run) string {
	if strings.TrimSpace(run.Reason) != "" {
		return strings.Join(strings.Fields(run.Reason), " ")
	}
	return runListCommand(run)
}
func runStart(run model.Run) time.Time {
	if run.Started != nil {
		return *run.Started
	}
	return run.Created
}
func orderRunRecords(runs []model.Run) {
	sort.SliceStable(runs, func(i, j int) bool {
		a, b := runs[i], runs[j]
		if activeRunRecord(a) != activeRunRecord(b) {
			return activeRunRecord(a)
		}
		if currentRunRecord(a) != currentRunRecord(b) {
			return currentRunRecord(a)
		}
		if !runStart(a).Equal(runStart(b)) {
			return runStart(a).After(runStart(b))
		}
		return a.ID < b.ID
	})
}
func (m *dashboardModel) runListRender() textRows {
	width := max(1, m.mainWidth()-4)
	rows := map[int]int{}
	var lines []string
	if !m.connected {
		lines = append(lines, short("DISCONNECTED · cached runs are stale", width), "")
	}
	if m.runsUI != nil && m.runsUI.err != "" {
		lines = append(lines, short("Runs unavailable: "+m.runsUI.err, width), "")
	}
	records := m.filteredRuns()
	lines = append(lines, short(fmt.Sprintf("%d runs · Enter opens output", len(records)), width), "")
	active := 0
	for _, run := range records {
		if currentRunRecord(run) {
			active++
		}
	}
	group := ""
	for i, run := range records {
		current := "HISTORY"
		count := len(records) - active
		if currentRunRecord(run) {
			current = "CURRENT"
			count = active
		}
		if current != group {
			if group != "" {
				lines = append(lines, "")
			}
			lines = append(lines, muted.Render(short(fmt.Sprintf("%s · %d", current, count), width)))
			group = current
		}
		rows[len(lines)] = i
		state := runStatusLabel(run)
		ink := muted
		if activeRunRecord(run) {
			ink = accent
		}
		if state == "Stopping" || state == "Cleaning up" || state == "Needs attention" || state == "Reporter lost" || state == "Log failed" || state == "Unknown" || state == "Interrupted" {
			ink = strong.Foreground(colorWarning)
		}
		if state == "Failed" {
			ink = strong.Foreground(colorDanger)
		}
		purpose := short(runListPurpose(run), max(1, width-ansi.StringWidth(state)-4))
		lines = append(lines, selectedPrefix(m.selected, i)+align(purpose, ink.Render(state), max(1, width-2)))
		when := runListTime(run, m.timeNow)
		secondaryWidth := min(36, max(14, width/2))
		agent := run.Agent
		if agent == "" {
			agent = "Unattributed"
		}
		secondary := short(agent, max(1, secondaryWidth-ansi.StringWidth(when)-3)) + " · " + when
		lines = append(lines, "  "+align(short(runListCommand(run), max(1, width-ansi.StringWidth(secondary)-4)), muted.Render(secondary), max(1, width-2)))
	}
	if len(records) == 0 {
		lines = append(lines, "No matching runs.")
	}
	return textRows{text: strings.Join(lines, "\n"), rows: rows}
}
func (m *dashboardModel) runListSelectionRow() int {
	for row, index := range m.runListRender().rows {
		if index == m.selected {
			return row
		}
	}
	return 0
}
