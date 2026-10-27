package manager

import (
	"context"
	"errors"
	"getarm.org/internal/model"
	"io"
	"os"
	"reflect"
	"syscall"
	"time"

	"getarm.org/internal/childowner"
)

func (j *job) signalGroup(sig syscall.Signal) error {
	if j.childOwner != nil {
		return j.childOwner.Signal(sig)
	}
	// Cargo's separate helper watcher already joins before its original Wait.
	// Serialize generic End/SSH entry points with that same irreversible cut.
	j.controlMu.Lock()
	defer j.controlMu.Unlock()
	if j.controlRetired || j.cmd == nil || j.cmd.Process == nil {
		return childowner.ErrRetired
	}
	err := syscall.Kill(-j.cmd.Process.Pid, sig)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		j.controlError = errors.Join(j.controlError, err)
	}
	return err
}
func (j *job) retireControls() { j.controlMu.Lock(); j.controlRetired = true; j.controlMu.Unlock() }
func (j *job) controlActive() bool {
	if j.childOwner != nil {
		return j.childOwner.Active()
	}
	j.controlMu.Lock()
	defer j.controlMu.Unlock()
	return !j.controlRetired
}

// requestOriginalJobCancellation requires Manager.mu and the caller's exact
// current job binding. End and degraded clock reconciliation share one volatile
// request: repeating either operation cannot register another escalation worker.
// This requests cleanup; it does not settle a lease or replace the sole Wait.
func (m *Manager) requestOriginalJobCancellation(id string, j *job) error {
	if j == nil || m.jobs[id] != j {
		return model.Err("needs_attention", "Original job registration differs; obligations remain charged")
	}
	if j.originalCancellationRequested {
		return j.originalCancellationError
	}
	j.originalCancellationRequested = true
	// Signal the original group before closing SSH IO: EOF/SIGPIPE must not
	// win the original parent's signal race, even when persistence failed.
	j.originalCancellationError = j.signalGroup(syscall.SIGTERM)
	if j.sshRuntime != nil {
		j.sshRuntime.cancel()
	}
	m.wg.Go(func() {
		select {
		case <-j.done:
			return
		case <-time.After(2 * time.Second):
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.jobs[id] == j {
			_ = j.signalGroup(syscall.SIGKILL)
		}
	})
	return j.originalCancellationError
}

// checkpointExpiryJobBinding never reconstructs signal authority from a row or
// a fresh PID observation. The original process-local owner must still control
// its unreaped child, with all captured lease and SSH transport bindings intact.
// Caller holds Manager.mu; failures confer no cancellation authority.
func (m *Manager) checkpointExpiryJobBinding(id string, l model.Lease, j *job) error {
	if j == nil || m.jobs[id] != j || j.cmd == nil || j.cmd.Process == nil || j.childOwner == nil || j.done == nil ||
		id == "" || l.ID != id || l.Host != m.host || l.Owner != m.owner || l.Host != j.leaseHost || l.Owner != j.leaseOwner ||
		!reflect.DeepEqual(l.Request, j.leaseRequest) || j.identity == nil || l.Process == nil ||
		l.PID <= 0 || l.PID != j.cmd.Process.Pid || j.identity.PID != l.PID || j.identity.PGID != l.PID ||
		j.identity.UID != l.Owner || j.identity.Start == "" || j.identity.Namespace == "" ||
		!reflect.DeepEqual(l.Process, j.identity) || !j.childOwner.Active() {
		return model.Err("needs_attention", "Original expired job binding is unavailable; obligations remain charged")
	}
	if r := j.sshRuntime; r != nil {
		h := r.leaseHold
		if h == nil || m.sshTransports[id] != h || h.manager != m || h.runtime != r || h.job != j || r.cancel == nil ||
			!h.nativeStarted || h.nativeReaped || h.staged || h.err != nil ||
			h.original.ID != id || h.original.Host != l.Host || h.original.Owner != l.Owner ||
			!reflect.DeepEqual(h.original.Request, l.Request) || h.expectedPID != l.PID ||
			!reflect.DeepEqual(h.expectedProcess, l.Process) {
			return model.Err("needs_attention", "Original expired SSH transport binding differs; obligations remain charged")
		}
	} else if m.sshTransports[id] != nil {
		return model.Err("needs_attention", "Original expired job SSH runtime is unavailable; obligations remain charged")
	}
	return nil
}

// joinUnstarted closes the private gate before the sole original waiter. A
// failed cleanup never erases the captured process or releases its promises.
// Caller holds Manager.mu; no requested executable received its gate.
func (m *Manager) joinUnstarted(l model.Lease, j *job, reader, writer *os.File, files io.Closer) (model.Lease, bool, error) {
	_ = reader.Close()
	_ = writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := j.childOwner.Wait(ctx)
	closeErr := files.Close()
	if j.sshRuntime == nil {
		close(j.done)
	} else {
		close(j.nativeDone)
		h := j.sshRuntime.leaseHold
		h.nativeReaped = result.Reaped
		h.nativeSafe = result.Reaped && result.CleanupError == nil && closeErr == nil
	}
	if !result.Reaped || result.CleanupError != nil || closeErr != nil {
		l.PID = j.cmd.Process.Pid
		l.Process = j.identity
		m.jobs[l.ID] = j
		l.State = "needs_attention"
		l.Ended = nil
		l.Revision++
		l.Failure = "Original gated child cleanup is uncertain: " + errors.Join(result.CleanupError, result.WaitError, closeErr).Error()
		if j.sshRuntime != nil {
			current, err := m.stageSSHResult(l)
			return current, false, err
		}
		return l, false, m.put(l, "lease.supervision_unknown")
	}
	if j.sshRuntime == nil {
		delete(m.jobs, l.ID)
	} else {
		m.jobs[l.ID] = j
	}
	l.PID = 0
	l.Process = nil
	return l, true, nil
}

func (j *job) controlFailure() error {
	j.controlMu.Lock()
	defer j.controlMu.Unlock()
	return j.controlError
}
