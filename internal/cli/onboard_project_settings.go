package cli

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"getarm.org/internal/rollout"
	"github.com/charmbracelet/x/ansi"
)

var pickerScopes = []onboardChoice{
	{"Discovered Git checkouts", "Cover the Git checkouts represented in this selection.", "git"},
	{"All registered worktrees", "Cover this repository's existing and future registered Git checkouts.", rollout.WorktreesKind},
	{"Exact directories", "Cover the selected directories and their subdirectories.", "directory"},
}

var pickerPolicies = []onboardChoice{
	{"New sessions only", "Sessions started after this policy takes effect can opt in. Older sessions need an invitation.", "new-sessions"},
	{"Existing and future sessions", "Eligible sessions in scope can opt in by acknowledging ARM.", "all"},
	{"Observe only", "Sessions can report their presence. Joining requires an explicit invitation.", "observe"},
	{"Disabled", "Save the project scope with discovery and adoption disabled.", "disabled"},
}

func pickerChoice(choices []onboardChoice, value string) onboardChoice {
	for _, choice := range choices {
		if choice.value == value {
			return choice
		}
	}
	return onboardChoice{title: value}
}

func pickerScopeLabel(kind string) string {
	return pickerChoice(pickerScopes, kind).title
}

func (m *projectOnboardModel) settingsChoices() (choices []onboardChoice, current, heading string) {
	setting := m.settings[m.cursor]
	if m.stage == pickerPolicy {
		return pickerPolicies, setting.Mode, "Who can join?"
	}
	choices = pickerScopes
	if m.group(setting.Group).Kind == "directory" {
		choices = pickerScopes[2:]
	}
	return choices, setting.Kind, "Which checkouts should ARM cover?"
}

func (m *projectOnboardModel) openSettingsChoice(stage projectPickerStage) {
	m.stage = stage
	choices, current, _ := m.settingsChoices()
	m.choice = max(0, slices.IndexFunc(choices, func(choice onboardChoice) bool { return choice.value == current }))
}

func (m *projectOnboardModel) updateSettingsChoice(key string) {
	choices, current, _ := m.settingsChoices()
	switch key {
	case "up", "k":
		m.choice = max(0, m.choice-1)
	case "down", "j":
		m.choice = min(len(choices)-1, m.choice+1)
	case "esc", "q":
		m.stage = pickerSettings
	case "enter":
		if value := choices[m.choice].value; value != current {
			setting := &m.settings[m.cursor]
			if m.stage == pickerPolicy {
				setting.Mode = value
			} else {
				setting.Kind = value
			}
			m.plan = rollout.OnboardBatchPlan{}
			m.generation++
			m.notice = ""
		}
		m.stage = pickerSettings
	}
}

func (m *projectOnboardModel) viewSettings(b *strings.Builder, title lipgloss.Style) {
	setting := m.settings[m.cursor]
	group := m.group(setting.Group)
	width := max(20, m.width-4)
	line := func(text string) { fmt.Fprintln(b, ansi.Truncate(text, width, "…")) }
	fmt.Fprintf(b, "Configure project · %d of %d\n\n", m.cursor+1, len(m.settings))
	line(title.Render(terminalSafe(group.Name)))
	line("  Project ID    " + terminalSafe(setting.Project))
	line("  Coverage      " + pickerScopeLabel(setting.Kind))
	line("  Eligibility   " + pickerChoice(pickerPolicies, setting.Mode).title)
	path, label := group.Directory, "  Checkout      "
	if setting.Kind == "directory" {
		label = "  Directory     "
	}
	if setting.Kind == rollout.WorktreesKind {
		for _, member := range group.Memberships {
			if member.Checkout != nil {
				path, label = member.Checkout.Common.Path, "  Repository    "
				break
			}
		}
	}
	path = terminalSafe(path)
	if available := width - len(label); ansi.StringWidth(path) > available {
		path = ansi.TruncateLeft(path, ansi.StringWidth(path)-available+1, "…")
	}
	line(label + path)
	fmt.Fprintln(b)
	if len(m.settings) > 1 {
		line("↑/↓ switch project")
	}
	line("i  ID   s  Coverage   p  Eligibility")
	line("Enter  Review policy   Esc  Back")
}

func (m *projectOnboardModel) viewSettingsChoice(b *strings.Builder, title, selected lipgloss.Style) {
	choices, current, heading := m.settingsChoices()
	width := max(20, m.width-4)
	fmt.Fprintln(b, title.Render(heading))
	group := m.group(m.settings[m.cursor].Group)
	fmt.Fprintln(b, ansi.Truncate(terminalSafe(group.Name), width, "…"))
	fmt.Fprintln(b)
	for i, choice := range choices {
		cursor, mark := " ", "○"
		if i == m.choice {
			cursor = "›"
		}
		if choice.value == current {
			mark = "●"
		}
		line := ansi.Truncate(cursor+" "+mark+" "+choice.title, width, "…")
		if i == m.choice {
			line = selected.Render(line)
		}
		fmt.Fprintln(b, line)
	}
	fmt.Fprintln(b, "\n"+ansi.Wrap(choices[m.choice].detail, width, ""))
	fmt.Fprintln(b, "\n↑/↓ Move   Enter  Choose   Esc  Back")
}
