package childowner

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
)

func TestDarwinTerminalOnlyPIDListRequiresExactSoleAnchor(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ids       [2]int32
		size      int32
		accepted  bool
		malformed bool
	}{
		{"exact", [2]int32{123, 0}, 4, true, false},
		{"other-member", [2]int32{123, 124}, 8, false, false},
		{"full-duplicate", [2]int32{123, 123}, 8, false, false},
		{"wrong-anchor", [2]int32{124, 0}, 4, false, false},
		{"empty", [2]int32{}, 0, false, true},
		{"denied", [2]int32{}, -1, false, true},
		{"truncated-size", [2]int32{123}, 3, false, true},
		{"oversized", [2]int32{123}, 12, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := terminalOnlyPIDList(123, tc.ids, tc.size)
			if got != tc.accepted || (err != nil) != tc.malformed {
				t.Fatal(got, err)
			}
		})
	}
}
func TestNativeDarwinTerminalOnlyGroupAvoidsZombieSignal(t *testing.T) {
	cmd, o := startOwnedFixture(t, "/bin/sh", "-c", "exit 0")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		ended, err := observeTerminal(cmd.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if ended.ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	sole, err := terminalOnlyGroup(cmd.Process.Pid)
	if err != nil || !sole {
		t.Fatal("original terminal anchor was not sole native group member", sole, err)
	}
	calls := 0
	o.mu.Lock()
	o.signal = func(int, syscall.Signal) error { calls++; return syscall.EPERM }
	o.mu.Unlock()
	result := o.Wait(ctx)
	if !result.Reaped || result.CleanupError != nil || cmd.ProcessState.ExitCode() != 0 || calls != 0 {
		t.Fatal(result, cmd.ProcessState, calls)
	}
	if err := o.Signal(syscall.SIGKILL); !errors.Is(err, ErrRetired) || calls != 0 {
		t.Fatal(err, calls)
	}
}
