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

func TestNativeDarwinNaturalExitBeforeFirstTermRetiresSignalsAndJoins(t *testing.T) {
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
	calls := 0
	o.mu.Lock()
	o.signal = func(int, syscall.Signal) error { calls++; return syscall.EPERM }
	o.mu.Unlock()
	if err := o.Signal(syscall.SIGTERM); !errors.Is(err, ErrRetired) || o.Active() {
		t.Fatal(err, o.Active())
	}
	if err := o.Signal(syscall.SIGKILL); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
	result := o.Wait(ctx)
	if !result.Reaped || result.CleanupError != nil || cmd.ProcessState.ExitCode() != 0 || calls != 0 {
		t.Fatal(result, cmd.ProcessState, calls)
	}
}

// Reproduces the real Darwin race: the preflight sees a live leader, the
// leader then exits and the kernel's zombie-only killpg returns EPERM. The
// owner must prove the terminal singleton instead of recording a denial.
func TestNativeDarwinLeaderExitBetweenPreflightAndSignalIsProven(t *testing.T) {
	cmd, o := startOwnedFixture(t, "/bin/sh", "-c", "exit 0")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var kernelErr error
	calls := 0
	o.mu.Lock()
	o.signal = func(pid int, sig syscall.Signal) error {
		calls++
		for {
			ended, err := observeTerminal(pid)
			if err != nil {
				return err
			}
			if ended.ready {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
		kernelErr = syscall.Kill(-pid, sig)
		return kernelErr
	}
	observe := o.observe
	o.observe = func(pid int) (terminal, error) {
		if calls == 0 {
			return terminal{}, nil // The preflight observed the leader before exit.
		}
		return observe(pid)
	}
	o.mu.Unlock()
	if err := o.Signal(syscall.SIGKILL); !errors.Is(err, ErrRetired) || o.Active() {
		t.Fatal(err, kernelErr, o.Active())
	}
	if !errors.Is(kernelErr, syscall.EPERM) {
		t.Fatal("kernel did not report zombie-only group EPERM", kernelErr)
	}
	result := o.Wait(ctx)
	if !result.Reaped || result.CleanupError != nil || cmd.ProcessState.ExitCode() != 0 || calls != 1 {
		t.Fatal(result, cmd.ProcessState, calls)
	}
}

// A live leader whose signal is genuinely denied keeps the failure even on
// Darwin's post-denial witness path, and later cleanup cannot erase it.
func TestNativeDarwinDeniedSignalToLiveLeaderStaysFailure(t *testing.T) {
	cmd, o := startOwnedFixture(t, "/bin/sh", "-c", "exec sleep 30")
	o.mu.Lock()
	original := o.signal
	o.signal = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGTERM {
			return syscall.EPERM
		}
		return original(pid, sig)
	}
	o.mu.Unlock()
	if err := o.Signal(syscall.SIGTERM); !errors.Is(err, syscall.EPERM) || !o.Active() {
		t.Fatal(err, o.Active())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := o.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	result := o.Wait(ctx)
	if !result.Reaped || !errors.Is(result.CleanupError, syscall.EPERM) || cmd.ProcessState.ExitCode() != -1 {
		t.Fatal(result, cmd.ProcessState)
	}
}
