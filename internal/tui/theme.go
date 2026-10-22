package tui

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Match the web console's dark surfaces and teal accent. Shape, labels and
// keyboard markers carry focus/status independently of terminal color support.
var (
	colorBackground = lipgloss.Color("#101519")
	colorPanel      = lipgloss.Color("#151c21")
	colorLine       = lipgloss.Color("#3b4c58")
	colorText       = lipgloss.Color("#e1e9ed")
	colorAccent     = lipgloss.Color("#74d9c8")
	colorBlue       = lipgloss.Color("#86b7db")
	colorPurple     = lipgloss.Color("#b7acf1")
	colorWarning    = lipgloss.Color("#efc47c")
	colorDanger     = lipgloss.Color("#f79797")
	canvasStyle     = lipgloss.NewStyle().Foreground(colorText).Background(colorBackground)
	panelStyle      = lipgloss.NewStyle().Foreground(colorText).Background(colorPanel)
)

func align(left, right string, width int) string {
	right = ansi.Truncate(right, max(0, width), "…")
	space := max(1, width-ansi.StringWidth(right)-ansi.StringWidth(left))
	if ansi.StringWidth(left)+ansi.StringWidth(right)+1 > width {
		left = ansi.Truncate(left, max(0, width-ansi.StringWidth(right)-1), "…")
	}
	return left + strings.Repeat(" ", space) + right
}

// window delegates sizing, padding, backgrounds and borders to Lip Gloss. Its
// top border gets a bounded title, as in btop's named monitor windows.
func window(title, body string, width, height int, focused bool, tone string) string {
	width = max(6, width)
	inner := max(1, width-4)
	if height <= 0 {
		height = len(strings.Split(body, "\n")) + 2
	}
	height = max(3, height)
	border := lipgloss.RoundedBorder()
	ink := colorLine
	if focused {
		border = lipgloss.DoubleBorder()
		ink = colorAccent
	}
	switch tone {
	case "accent":
		ink = colorAccent
	case "warning":
		ink = colorWarning
	case "danger":
		ink = colorDanger
	case "blue":
		ink = colorBlue
	case "purple":
		ink = colorPurple
	}
	frame := panelStyle.Border(border).BorderForeground(ink).Padding(0, 1).Width(width).Height(height).Render(fitBlock(body, inner, height-2))
	lines := strings.Split(frame, "\n")
	label := ansi.Truncate(safe(title), max(0, width-6), "…")
	lineStyle := lipgloss.NewStyle().Foreground(ink).Background(colorPanel)
	titleInk := colorText
	if focused || tone != "" {
		titleInk = ink
	}
	lines[0] = lineStyle.Render(border.TopLeft+border.Top+" ") + strong.Foreground(titleInk).Background(colorPanel).Render(label) + lineStyle.Render(" "+strings.Repeat(border.Top, max(0, width-ansi.StringWidth(label)-5))+border.TopRight)
	return strings.Join(lines, "\n")
}

func keycap(key, label, tone string) string {
	style := lipgloss.NewStyle().Bold(true).Padding(0, 1).Foreground(colorAccent).Background(lipgloss.Color("#203630"))
	if tone == "danger" {
		style = style.Foreground(colorDanger).Background(lipgloss.Color("#3a282b"))
	}
	if tone == "quiet" {
		style = style.Foreground(colorText).Background(lipgloss.Color("#29343d"))
	}
	return style.Render("[" + key + "] " + label)
}

func bar(value, total *float64, width int, tone string) string {
	width = max(1, width)
	if value == nil || total == nil || *total <= 0 || math.IsNaN(*value) || math.IsNaN(*total) || math.IsInf(*value, 0) || math.IsInf(*total, 0) {
		return muted.Render(strings.Repeat("·", width))
	}
	filled := int(math.Round(max(0, min(1, *value / *total)) * float64(width)))
	return meterFill(filled, width, tone) + muted.Foreground(colorLine).Render(strings.Repeat("─", width-filled))
}

func percentage(value, total *float64) string {
	if value == nil || total == nil || *total <= 0 || math.IsNaN(*value) || math.IsInf(*value, 0) || math.IsNaN(*total) || math.IsInf(*total, 0) {
		return "unavailable"
	}
	return fmt.Sprintf("%.1f%%", max(0, *value / *total * 100))
}

// Keep structured detail/record labels legible in every view, including forms
// and reviews; terminal color stripping still leaves all names and markers.
func fieldLabels(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.Contains(line, "\x1b[") {
			continue
		}
		if split := strings.Index(line, ": "); split >= 0 && split < 45 {
			lines[i] = strong.Foreground(colorBlue).Render(line[:split+1]) + line[split+1:]
		}
	}
	return strings.Join(lines, "\n")
}
