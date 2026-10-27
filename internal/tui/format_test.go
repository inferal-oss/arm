package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func TestTerminalDataCannotInjectControlSequencesOrHideOwnership(t *testing.T) {
	text := safe("agent\x1b]52;c;clipboard\a\n\u202euser")
	if strings.ContainsAny(text, "\x1b\a\n\u202e") || !strings.Contains(text, `\u001b`) || !strings.Contains(text, `\n`) {
		t.Fatal("terminal data was interpreted as control sequences", text)
	}
	if got := owner("501", "alice"); got != "alice (UID 501)" {
		t.Fatal(got)
	}
	if got := owner("501", ""); got != "UID 501" {
		t.Fatal("historical owner was guessed", got)
	}
}

func TestExactCountAndUnknownMeasurementsRemainDistinct(t *testing.T) {
	if got := exact(json.Number("9223372036854775807")); got != "9223372036854775807" {
		t.Fatal("pool count rounded", got)
	}
	if got := quantity(json.Number("9223372036854775807"), "tokens"); got != "9223372036854775807 tokens" {
		t.Fatal(got)
	}
	if got := measurement(model.Measurement{}); got != "unavailable" {
		t.Fatal("missing observation became zero", got)
	}
	if got := byteCount("1073741824"); got != "1 GiB" {
		t.Fatal(got)
	}
}

func TestHistoryGraphBreaksUnknownAndMissingIntervals(t *testing.T) {
	n := float64(100)
	at := time.Now()
	points := []model.MonitorPoint{{At: at, CPU: &n}, {At: at.Add(time.Second), CPU: nil}, {At: at.Add(5 * time.Second), CPU: &n}, {At: at.Add(6 * time.Second), CPU: &n}}
	got := historyLine(points, 10, time.Second, 100, func(p model.MonitorPoint) *float64 { return p.CPU })
	if got != "█··█" {
		t.Fatal("gapped history looked continuous", got)
	}
}
