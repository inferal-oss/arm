package tui

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/model"
)

func TestLeaseCheckinHumanDurationLabels(t *testing.T) {
	cases := []struct {
		ns    int64
		label string
	}{
		{0, "0s"}, {1, "1ns"}, {999, "999ns"}, {1000, "1µs"}, {1234, "1.234µs"}, {999999, "999.999µs"}, {1000000, "1ms"}, {1234567, "1.235ms"}, {999999999, "1s"}, {1000000000, "1s"}, {1234567890, "1.235s"}, {59999999999, "1m"}, {60000000000, "1m"}, {600000000000, "10m"}, {120000000000, "2m"}, {1799133950000, "29m59s"}, {1799600000000, "30m"}, {3661900000000, "1h1m2s"}, {604800000000000, "7d"}, {math.MaxInt64, "106751d23h47m17s"}, {-1, "Invalid duration"}, {math.MinInt64, "Invalid duration"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			if got := reviewDuration(time.Duration(tc.ns)); got != tc.label {
				t.Fatalf("duration label %d: got %q want %q", tc.ns, got, tc.label)
			}
		})
	}
}
func TestLeaseDurationParserAllowsZeroComponentsAndRetainsTotalGuards(t *testing.T) {
	for _, tc := range []struct {
		text string
		want time.Duration
	}{{"10m0s", 10 * time.Minute}, {"0h2m0s", 2 * time.Minute}, {"7d0h0m0s", 7 * 24 * time.Hour}, {"0m0.001s", time.Millisecond}, {"0.1m0s", 6 * time.Second}, {"0s1m0s", time.Minute}} {
		got, err := parseTTL(tc.text)
		if err != nil || got != tc.want {
			t.Fatal("valid positive duration with zero component refused", tc.text, got, err)
		}
	}
	for _, text := range []string{"0s", "0h0m0s", "-0h1m", "+0h1m", "7d0.000000001s", "999999999999999999h0s", "1m00.0000000001s", "10m0s garbage", "0.0000000001s"} {
		if _, err := parseTTL(text); err == nil {
			t.Fatal("invalid/zero/overflow duration accepted", text)
		}
	}
}
func TestLeaseCheckinDurationRendererKeepsExactDraft(t *testing.T) {
	m, _ := checkinFixture(t)
	openTestCheckin(t, m)
	ui := m.checkins
	ui.policy.Enabled = true
	ui.policy.Interval = 10 * time.Minute
	ui.policy.ContentionInterval = 2 * time.Minute
	ui.policy.ReplyTimeout = 10 * time.Minute
	now := time.Now()
	ui.pending.Items = []model.LeaseReview{{State: "pending", Agent: "sample", Created: now, Deadline: now.Add(time.Minute), Context: []model.LeaseReviewContext{{Lease: ui.lease.ID, Purpose: "Sample remaining time", Remaining: 1799133950000}}}}
	text := ansi.Strip(m.checkinText())
	if !strings.Contains(text, "every 10m · reply window 10m · 2m while work waits") || !strings.Contains(text, "Captured remaining: 29m59s") {
		t.Fatal("human labels missing", text)
	}
	ui.policy.Interval = 15123456789
	ui.policy.ContentionInterval = 0
	ui.policy.ReplyTimeout = 5000000123
	press(m, "e")
	press(m, "y")
	if !ui.confirm || ui.request == nil || ui.request.Interval != 15123456789 || ui.request.ReplyTimeout != 5000000123 {
		t.Fatal("display rounding changed exact draft", ui.note)
	}
	text = ansi.Strip(m.checkinText())
	if !strings.Contains(text, "every 15.123s") || !strings.Contains(text, "Exact draft durations: regular 15.123456789s") {
		t.Fatal("review hides exact chosen precision", text)
	}
}
