// Package childowner retains control of one original started child until its
// single terminal Wait. Nothing here reconstructs authority from a stored PID.
package childowner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var ErrRetired = errors.New("original child control authority is retired")

type terminal struct {
	ready  bool
	code   int
	signal syscall.Signal
}

type Result struct {
	WaitError    error
	CleanupError error
	Reaped       bool
}

// Owner is process-local, never serialized or reconstructed after recovery.
// Its mutex serializes signals and the irreversible retirement before Wait.
type Owner struct {
	mu                  sync.Mutex
	cmd                 *exec.Cmd
	pid                 int
	retired             bool
	signalsRetired      bool
	terminalGroupProven bool
	terminalGroupExit   terminal
	preflightSignals    bool
	waiting             bool
	failure             error
	killSentAt          time.Time
	killSucceeded       bool
	terminalOnlyGroup   func(int) (bool, error)
	terminalGroupGrace  time.Duration
	observe             func(int) (terminal, error)
	signal              func(int, syscall.Signal) error
	wait                func() error
}

// Available checks the native observer before any child is started. The
// current process cannot be its own child, so ECHILD is the expected result.
func Available() error {
	_, err := observeTerminal(os.Getpid())
	if errors.Is(err, syscall.ECHILD) {
		return nil
	}
	return fmt.Errorf("native original-child observer unavailable: %v", err)
}

func New(cmd *exec.Cmd) (*Owner, error) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 0 || cmd.ProcessState != nil {
		return nil, fmt.Errorf("original started unwaited command required")
	}
	group, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return nil, fmt.Errorf("original child group unavailable: %w", err)
	}
	if group != cmd.Process.Pid {
		return nil, fmt.Errorf("original child must lead its own process group")
	}
	// Refuse unavailable native observation while the private workload gate
	// remains closed; no command may launch before its owner can observe exit.
	if _, err := observeTerminal(cmd.Process.Pid); err != nil {
		return nil, fmt.Errorf("original non-reaping observation unavailable before gate: %w", err)
	}
	return &Owner{cmd: cmd, pid: cmd.Process.Pid, observe: observeTerminal, terminalOnlyGroup: terminalOnlyGroup, terminalGroupGrace: nativeTerminalGroupGrace, preflightSignals: nativePreflightSignals, signal: func(pid int, sig syscall.Signal) error { return syscall.Kill(-pid, sig) }, wait: cmd.Wait}, nil
}

func (o *Owner) Signal(sig syscall.Signal) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.retired || o.signalsRetired {
		return ErrRetired
	}
	if o.preflightSignals {
		ended, err := o.observe(o.pid)
		if err != nil {
			o.failure = errors.Join(o.failure, fmt.Errorf("original child signal preflight is unknown: %w", err))
			o.signalsRetired = true
			return o.failure
		}
		if ended.ready {
			sole, err := o.proveTerminalOnlyGroupLocked()
			if err != nil {
				o.failure = errors.Join(o.failure, fmt.Errorf("original terminal group signal preflight is unknown: %w", err))
				o.signalsRetired = true
				return o.failure
			}
			if sole {
				o.terminalGroupProven = true
				o.terminalGroupExit = ended
				o.signalsRetired = true
				return ErrRetired
			}
		}
	}
	return o.signalLocked(sig)
}
func (o *Owner) signalLocked(sig syscall.Signal) error {
	// An actual successful group KILL is irreversible. Repeating it can yield
	// EPERM for Darwin's remaining zombie-only group, without adding cleanup.
	if sig == syscall.SIGKILL && o.killSucceeded {
		return nil
	}
	if sig == syscall.SIGKILL && o.killSentAt.IsZero() {
		o.killSentAt = time.Now()
	}
	err := o.signal(o.pid, sig)
	if sig == syscall.SIGKILL && err == nil {
		o.killSucceeded = true
	}
	// ESRCH is an observation only, never a positive exit or a join witness.
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		o.failure = errors.Join(o.failure, fmt.Errorf("original group signal %d denied or unknown: %w", sig, err))
	}
	return err
}
func (o *Owner) Active() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return !o.retired && !o.signalsRetired
}

// Wait observes the leader without reaping it. A terminal unreaped leader is
// still the original PID anchor while descendants receive the final KILL.
// Retirement precedes the sole native Wait; later callers can never signal a
// numeric PID that the OS may have reused. Signal/observation/join failures
// remain failures even when a later read appears healthy.
func (o *Owner) Wait(ctx context.Context) Result {
	o.mu.Lock()
	if o.waiting || o.retired {
		o.mu.Unlock()
		return Result{CleanupError: ErrRetired}
	}
	o.waiting = true
	o.mu.Unlock()
	for {
		o.mu.Lock()
		var ended terminal
		var err error
		if o.terminalGroupProven {
			// This unreaped original terminal status was captured together with
			// the positive no-live-group witness before signal retirement.
			ended = o.terminalGroupExit
		} else {
			ended, err = o.observe(o.pid)
		}
		if errors.Is(err, syscall.EINTR) {
			err = nil
			ended = terminal{}
		}
		if err != nil {
			o.failure = errors.Join(o.failure, fmt.Errorf("original child observation is unknown: %w", err))
			o.retired = true
			result := Result{CleanupError: o.failure}
			o.mu.Unlock()
			return result
		}
		if !ended.ready {
			if o.signalsRetired {
				o.retired = true
				result := Result{CleanupError: o.failure}
				o.mu.Unlock()
				return result
			}
			if ctx.Err() != nil && o.killSentAt.IsZero() && !o.signalsRetired {
				_ = o.signalLocked(syscall.SIGKILL)
			}
			if !o.killSentAt.IsZero() && time.Since(o.killSentAt) >= 2*time.Second {
				o.failure = errors.Join(o.failure, fmt.Errorf("original child did not reach terminal observation after bounded cancellation"))
				o.retired = true
				result := Result{CleanupError: o.failure}
				o.mu.Unlock()
				return result
			}
			o.mu.Unlock()
			time.Sleep(20 * time.Millisecond)
			continue
		}
		terminalOnly := o.terminalGroupProven
		if !terminalOnly && !o.killSucceeded && !o.signalsRetired && o.terminalOnlyGroup != nil {
			var groupErr error
			terminalOnly, groupErr = o.proveTerminalOnlyGroupLocked()
			if groupErr != nil {
				o.failure = errors.Join(o.failure, fmt.Errorf("original terminal group membership is unknown: %w", groupErr))
				terminalOnly = false
			}
		}
		if !terminalOnly && !o.signalsRetired {
			_ = o.signalLocked(syscall.SIGKILL)
		}
		o.retired = true
		cleanupErr := o.failure
		o.mu.Unlock()
		waitErr := o.wait()
		var exit *exec.ExitError
		known := waitErr == nil || errors.As(waitErr, &exit)
		if !known || o.cmd.ProcessState == nil {
			return Result{WaitError: waitErr, CleanupError: errors.Join(cleanupErr, fmt.Errorf("sole original Wait did not establish exit: %w", waitErr))}
		}
		status, ok := o.cmd.ProcessState.Sys().(syscall.WaitStatus)
		matches := ok && ((ended.signal == 0 && status.Exited() && status.ExitStatus() == ended.code) || (ended.signal != 0 && status.Signaled() && status.Signal() == ended.signal))
		if !matches {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("sole original Wait disagrees with non-reaping terminal observation"))
		}
		return Result{WaitError: waitErr, CleanupError: cleanupErr, Reaped: matches}
	}
}

// Caller retains the original terminal leader unreaped under Owner.mu. A
// captured positive singleton is an irreversible no-live-group witness; false
// remains bounded grace followed by actual control, never EPERM reinterpretation.
func (o *Owner) proveTerminalOnlyGroupLocked() (bool, error) {
	if o.terminalOnlyGroup == nil {
		return false, nil
	}
	deadline := time.Now().Add(o.terminalGroupGrace)
	for {
		sole, err := o.terminalOnlyGroup(o.pid)
		if err != nil || sole || !time.Now().Before(deadline) {
			return sole, err
		}
		time.Sleep(min(10*time.Millisecond, time.Until(deadline)))
	}
}
