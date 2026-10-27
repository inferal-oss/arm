package childowner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Controlled operations establish owner-state error cuts, not native cleanup.
func TestUnknownObservationRetiresWithoutSignalsOrWait(t *testing.T) {
	signals, waits := 0, 0
	o := &Owner{pid: 123, observe: func(int) (terminal, error) { return terminal{}, errors.New("unknown child") }, signal: func(int, syscall.Signal) error { signals++; return nil }, wait: func() error { waits++; return nil }}
	result := o.Wait(context.Background())
	if result.Reaped || result.CleanupError == nil || waits != 0 || signals != 0 || o.Active() {
		t.Fatal(result, waits, signals)
	}
	if err := o.Signal(syscall.SIGKILL); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
	if result = o.Wait(context.Background()); !errors.Is(result.CleanupError, ErrRetired) || waits != 0 || signals != 0 {
		t.Fatal(result, waits, signals)
	}
}
func TestRetirementPrecedesSoleWaitAndDeniedSignalStaysFailure(t *testing.T) {
	signals, waits := 0, 0
	var o *Owner
	o = &Owner{pid: 123, observe: func(int) (terminal, error) { return terminal{ready: true, code: 0}, nil }, signal: func(int, syscall.Signal) error { signals++; return syscall.EPERM }, wait: func() error {
		waits++
		if o.Active() {
			t.Error("Wait began while control authority remained live")
		}
		if err := o.Signal(syscall.SIGTERM); !errors.Is(err, ErrRetired) {
			t.Error(err)
		}
		return errors.New("native Wait uncertain")
	}}
	result := o.Wait(context.Background())
	if result.Reaped || result.CleanupError == nil || !errors.Is(result.CleanupError, syscall.EPERM) || waits != 1 || signals != 1 {
		t.Fatal(result, waits, signals)
	}
	if err := o.Signal(syscall.SIGKILL); !errors.Is(err, ErrRetired) || signals != 1 {
		t.Fatal(err, signals)
	}
}

var unknownFixtureMu sync.Mutex
var unknownFixtureCommands []*exec.Cmd

// No requested fixture executes before its original owner is constructed.
func startOwnedFixture(t *testing.T, args ...string) (*exec.Cmd, *Owner) {
	t.Helper()
	if err := Available(); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	gate := []string{"-c", `IFS= read -r gate <&3 || exit 125; [ "$gate" = go ] || exit 125; exec 3<&-; exec "$@"`, "owner-test"}
	cmd := exec.Command("/bin/sh", append(gate, args...)...)
	cmd.ExtraFiles = []*os.File{reader}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	o, err := New(cmd)
	if err != nil {
		writer.Close()
		reader.Close()
		unknownFixtureMu.Lock()
		unknownFixtureCommands = append(unknownFixtureCommands, cmd)
		unknownFixtureMu.Unlock()
		t.Fatalf("original fixture binding unknown; handle retained without further signal/Wait: %v", err)
	}
	t.Cleanup(func() {
		if o.Active() {
			signalErr := o.Signal(syscall.SIGKILL)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result := o.Wait(ctx)
			if signalErr != nil && !errors.Is(signalErr, syscall.ESRCH) {
				t.Errorf("original fixture cleanup signal: %v", signalErr)
			}
			if !result.Reaped || result.CleanupError != nil {
				t.Errorf("original fixture cleanup/join uncertain: %+v", result)
			}
		} else if cmd.ProcessState == nil {
			t.Error("original fixture owner unknown; no post-retirement signal or Wait is authorized")
		}
	})
	if _, err = writer.Write([]byte("go\n")); err != nil {
		writer.Close()
		result := o.Wait(context.Background())
		t.Fatalf("private gate write: %v; original join %+v", err, result)
	}
	return cmd, o
}
func TestNativeOriginalExitAndDescendantTeardown(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "descendant-ran")
	cmd, o := startOwnedFixture(t, "/bin/sh", "-c", `(sleep 1; printf unsafe > "$1") & exit 7`, "sh", marker)
	var err error
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := o.Wait(ctx)
	if !result.Reaped || result.CleanupError != nil || cmd.ProcessState.ExitCode() != 7 {
		t.Fatal(result, cmd.ProcessState)
	}
	if err = o.Signal(syscall.SIGKILL); !errors.Is(err, ErrRetired) {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("original descendant outlived pre-Wait teardown", err)
	}
	t.Logf("original pid %d observed non-reaping, descendants signalled before sole Wait, exit %d", cmd.Process.Pid, cmd.ProcessState.ExitCode())
}
func TestNativeConcurrentCancellationCannotSignalAfterWait(t *testing.T) {
	_, o := startOwnedFixture(t, "/bin/sh", "-c", "exec sleep 30")
	var err error
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Go(func() {
			err := o.Signal(syscall.SIGKILL)
			if err != nil && !errors.Is(err, ErrRetired) && !errors.Is(err, syscall.ESRCH) {
				t.Error(err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := o.Wait(ctx)
	wg.Wait()
	if !result.Reaped || result.CleanupError != nil {
		t.Fatal(result)
	}
	for i := 0; i < 3; i++ {
		if err = o.Signal(syscall.SIGTERM); !errors.Is(err, ErrRetired) {
			t.Fatal(err)
		}
	}
}

func TestRepeatedEINTRCannotBypassCancellationBound(t *testing.T) {
	signals, waits, observations := 0, 0, 0
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := &Owner{pid: 123, killSentAt: time.Now().Add(-3 * time.Second), observe: func(int) (terminal, error) { observations++; return terminal{}, syscall.EINTR }, signal: func(int, syscall.Signal) error { signals++; return nil }, wait: func() error { waits++; return nil }}
	result := o.Wait(ctx)
	if result.Reaped || result.CleanupError == nil || o.Active() || waits != 0 || observations != 1 || signals != 0 {
		t.Fatal(result, waits, observations, signals)
	}
}

func TestNativeDeniedSignalCannotBecomeSuccessfulCleanupAfterReap(t *testing.T) {
	cmd, o := startOwnedFixture(t, "/bin/sh", "-c", "exit 0")
	o.mu.Lock()
	originalSignal := o.signal
	o.signal = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGTERM {
			return syscall.EPERM
		}
		return originalSignal(pid, sig)
	}
	o.mu.Unlock()
	if err := o.Signal(syscall.SIGTERM); !errors.Is(err, syscall.EPERM) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := o.Wait(ctx)
	if !result.Reaped || cmd.ProcessState.ExitCode() != 0 || !errors.Is(result.CleanupError, syscall.EPERM) {
		t.Fatal(result, cmd.ProcessState)
	}
	// The permission failure above is controlled; exit/reap is actual native.
	// A positive later exit must not erase that earlier failed control outcome.
}

func TestNativeSuccessfulKillIsNotRepeatedDuringJoin(t *testing.T) {
	_, o := startOwnedFixture(t, "/bin/sh", "-c", "exec sleep 30")
	calls := 0
	o.mu.Lock()
	original := o.signal
	o.signal = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGKILL {
			calls++
			if calls > 1 {
				return syscall.EPERM
			}
		}
		return original(pid, sig)
	}
	o.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Go(func() {
			if err := o.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, ErrRetired) {
				t.Error(err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := o.Wait(ctx)
	wg.Wait()
	if !result.Reaped || result.CleanupError != nil || calls != 1 {
		t.Fatal(result, calls)
	}
	if err := o.Signal(syscall.SIGKILL); !errors.Is(err, ErrRetired) || calls != 1 {
		t.Fatal(err, calls)
	}
}
func TestDeniedKillDoesNotLatchOrDisappear(t *testing.T) {
	calls := 0
	o := &Owner{pid: 123, observe: func(int) (terminal, error) { return terminal{ready: true}, nil }, signal: func(int, syscall.Signal) error {
		calls++
		if calls == 1 {
			return syscall.EPERM
		}
		return nil
	}, wait: func() error { return errors.New("controlled unknown join") }}
	if err := o.Signal(syscall.SIGKILL); !errors.Is(err, syscall.EPERM) {
		t.Fatal(err)
	}
	if err := o.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	result := o.Wait(context.Background())
	if calls != 2 || !errors.Is(result.CleanupError, syscall.EPERM) || result.Reaped {
		t.Fatal(result, calls)
	}
}
func TestTerminalOnlyWitnessSkipsKillWithoutClearingPriorDenial(t *testing.T) {
	calls, waits := 0, 0
	o := &Owner{pid: 123, observe: func(int) (terminal, error) { return terminal{ready: true}, nil }, terminalOnlyGroup: func(int) (bool, error) { return true, nil }, signal: func(int, syscall.Signal) error { calls++; return syscall.EPERM }, wait: func() error { waits++; return errors.New("controlled unknown join") }}
	if err := o.Signal(syscall.SIGTERM); !errors.Is(err, syscall.EPERM) {
		t.Fatal(err)
	}
	result := o.Wait(context.Background())
	if calls != 1 || waits != 1 || !errors.Is(result.CleanupError, syscall.EPERM) {
		t.Fatal(result, calls, waits)
	}
}
func TestUnknownTerminalGroupWitnessRetainsFailureAndKills(t *testing.T) {
	calls := 0
	o := &Owner{pid: 123, observe: func(int) (terminal, error) { return terminal{ready: true}, nil }, terminalOnlyGroup: func(int) (bool, error) { return true, syscall.EACCES }, signal: func(int, syscall.Signal) error { calls++; return nil }, wait: func() error { return errors.New("controlled unknown join") }}
	result := o.Wait(context.Background())
	if calls != 1 || !errors.Is(result.CleanupError, syscall.EACCES) || result.Reaped {
		t.Fatal(result, calls)
	}
}

func TestTerminalGroupGraceWaitsForExactWitness(t *testing.T) {
	observations, signals, waits := 0, 0, 0
	o := &Owner{pid: 123, terminalGroupGrace: 50 * time.Millisecond, observe: func(int) (terminal, error) { return terminal{ready: true}, nil }, terminalOnlyGroup: func(int) (bool, error) { observations++; return observations == 2, nil }, signal: func(int, syscall.Signal) error { signals++; return syscall.EPERM }, wait: func() error { waits++; return errors.New("controlled unknown join") }}
	result := o.Wait(context.Background())
	if observations != 2 || signals != 0 || waits != 1 || errors.Is(result.CleanupError, syscall.EPERM) {
		t.Fatal(result, observations, signals, waits)
	}
}
func TestTerminalGroupGraceBoundStillIssuesRealKill(t *testing.T) {
	observations, signals, waits := 0, 0, 0
	o := &Owner{pid: 123, terminalGroupGrace: 20 * time.Millisecond, observe: func(int) (terminal, error) { return terminal{ready: true}, nil }, terminalOnlyGroup: func(int) (bool, error) { observations++; return false, nil }, signal: func(int, syscall.Signal) error { signals++; return syscall.EPERM }, wait: func() error { waits++; return errors.New("controlled unknown join") }}
	started := time.Now()
	result := o.Wait(context.Background())
	elapsed := time.Since(started)
	if observations < 2 || signals != 1 || waits != 1 || !errors.Is(result.CleanupError, syscall.EPERM) || elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Fatal(result, observations, signals, waits, elapsed)
	}
}
func TestTerminalGroupGraceErrorStopsPollingAndRemainsSticky(t *testing.T) {
	observations, signals := 0, 0
	o := &Owner{pid: 123, terminalGroupGrace: time.Second, observe: func(int) (terminal, error) { return terminal{ready: true}, nil }, terminalOnlyGroup: func(int) (bool, error) { observations++; return false, syscall.EACCES }, signal: func(int, syscall.Signal) error { signals++; return nil }, wait: func() error { return errors.New("controlled unknown join") }}
	result := o.Wait(context.Background())
	if observations != 1 || signals != 1 || !errors.Is(result.CleanupError, syscall.EACCES) {
		t.Fatal(result, observations, signals)
	}
}
