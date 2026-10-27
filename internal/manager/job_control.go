package manager

import (
	"context"
	"errors"
	"github.com/inferal-oss/arm/internal/model"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/inferal-oss/arm/internal/childowner"
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
