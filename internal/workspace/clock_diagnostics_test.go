package workspace

import (
	"errors"
	"github.com/inferal-oss/arm/internal/model"
	"strings"
	"testing"
	"time"
)

func TestClockRefusalRetainsFirstObservedCause(t *testing.T) {
	for _, tc := range []struct {
		name          string
		wall, elapsed time.Duration
		reason        string
	}{
		{"forward", 10 * time.Second, 0, "wall and elapsed time differ by 10s"},
		{"suspend-like", 70 * time.Second, time.Second, "wall and elapsed time differ by 1m9s"},
		{"elapsed-regression", 0, -time.Second, "elapsed clock moved backwards"},
		{"recorded-floor", -time.Second, 0, "wall time is before the recorded workspace floor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			clock := &fakeClock{wall: start}
			store := &Store{clock: clock, wallStart: start}
			if err := store.checkClock(start); err != nil {
				t.Fatal(err)
			}
			clock.advance(tc.wall, tc.elapsed)
			observed := clock.Wall().Format(time.RFC3339Nano)
			first := store.checkClock(start)
			if code(first) != "workspace_clock_unavailable" {
				t.Fatal(first)
			}
			message := first.Error()
			if !strings.Contains(message, tc.reason) || !strings.Contains(message, observed) {
				t.Fatalf("missing first observed cause/time: %v", first)
			}
			if len(message) > 512 {
				t.Fatalf("unbounded clock diagnostic: %d bytes", len(message))
			}
			// Mutating a returned error cannot alter the retained first observation.
			var typed *model.Error
			if !errors.As(first, &typed) {
				t.Fatal("untyped clock refusal")
			}
			typed.Message = "caller-owned mutation"
			clock.advance(time.Hour, time.Hour)
			again := store.checkClock(time.Time{})
			if code(again) != "workspace_clock_unavailable" || again.Error() != message {
				t.Fatalf("first evidence lost or authority recovered: %v", again)
			}
		})
	}
}

func TestClockRefusalDiagnosticPreservesStrictSkewBoundary(t *testing.T) {
	for _, direction := range []time.Duration{-1, 1} {
		start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		clock := &fakeClock{wall: start}
		store := &Store{clock: clock, wallStart: start}
		clock.advance(direction*MaxClockSkew, 0)
		if err := store.checkClock(time.Time{}); err != nil {
			t.Fatalf("exact allowed skew refused: %v", err)
		}
		clock.advance(direction*time.Nanosecond, 0)
		if err := store.checkClock(time.Time{}); code(err) != "workspace_clock_unavailable" {
			t.Fatalf("excess skew admitted: %v", err)
		}
	}
}
