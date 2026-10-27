package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/model"
)

// Lip Gloss builds the perceptual palette once. Bubble Tea v2 downsamples these
// RGB styles to the detected terminal profile (including NO_COLOR); text,
// borders, selection markers and numeric values never depend on the gradient.
var monitorPalettes = map[string][]color.Color{
	"accent": lipgloss.Blend1D(32, lipgloss.Color("#399a8d"), lipgloss.Color("#a1f0d8")),
	"blue":   lipgloss.Blend1D(32, lipgloss.Color("#5b87b8"), lipgloss.Color("#b3dcff")),
	"purple": lipgloss.Blend1D(32, lipgloss.Color("#8877c4"), lipgloss.Color("#d9c1ff")),
}

func toneColor(tone string, fraction float64) color.Color {
	palette := monitorPalettes[tone]
	if palette == nil {
		palette = monitorPalettes["accent"]
	}
	return palette[int(max(0, min(1, fraction))*float64(len(palette)-1))]
}
func meterFill(filled, width int, tone string) string {
	var out strings.Builder
	for i := 0; i < filled; i++ {
		out.WriteString(lipgloss.NewStyle().Foreground(toneColor(tone, float64(i)/float64(max(1, width-1)))).Render("━"))
	}
	return out.String()
}
func brandMark() string {
	return strong.Foreground(toneColor("accent", .85)).Render(" arm ")
}
func (m *dashboardModel) navigationLabel(index int) string {
	if m.width >= 100 {
		return fmt.Sprintf("%d %s", index+1, viewNames[index])
	}
	names := []string{"Mon", "Res", "Lse", "Msg", "Act", "Host", "Mesh"}
	if m.width < 55 {
		return fmt.Sprintf("%d%s", index+1, names[index])
	}
	return fmt.Sprintf("%d %s", index+1, names[index])
}
func leaseInk(l model.Lease) color.Color {
	switch l.State {
	case "queued", "provisioning", "ending", "reclaiming", "needs_attention":
		return colorWarning
	case "active":
		return colorAccent
	case "terminal":
		switch l.Outcome {
		case "failed", "launch_failed", "admission_failed":
			return colorDanger
		case "expired", "revoked", "rejected", "interrupted":
			return colorWarning
		case "completed":
			return colorAccent
		default:
			return colorText
		}
	default:
		return colorWarning
	}
}

// Keep scroll position in the frame, outside the scrolled content and trusted
// button regions. Line counts use Bubbles' visual wrapping, not byte lengths.
func scrollFrame(frame string, view viewport.Model, width int) string {
	total := view.TotalLineCount()
	if total <= view.Height() {
		return frame
	}
	start := view.YOffset() + 1
	end := min(total, view.YOffset()+view.Height())
	arrows := "↓"
	if !view.AtTop() {
		arrows = "↑↓"
	}
	if view.AtBottom() {
		arrows = "↑"
	}
	label := fmt.Sprintf(" %d–%d / %d %s ", start, end, total, arrows)
	size := ansi.StringWidth(label)
	if size > width-4 {
		return frame
	}
	lines := strings.Split(frame, "\n")
	last := len(lines) - 1
	left := width - size - 2
	lines[last] = ansi.Cut(lines[last], 0, left) + muted.Background(colorPanel).Render(label) + ansi.Cut(lines[last], left+size, width)
	return strings.Join(lines, "\n")
}
