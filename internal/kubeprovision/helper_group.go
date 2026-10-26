package kubeprovision

import (
	"syscall"
	"time"
)

// observeKubeGroupAbsence is observation only. After the original leader has
// been reaped, no signal may be sent to its saved numeric group ID. ESRCH is
// the sole positive witness that no process remains in that group at probe
// time; credential helpers that escaped the group are outside this witness.
func observeKubeGroupAbsence(pgid int) bool {
	if pgid <= 0 {
		return false
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if syscall.Kill(-pgid, 0) == syscall.ESRCH {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		time.Sleep(min(20*time.Millisecond, remaining))
	}
}

func signalCurrentKubeGroup(pid int) error {
	if pid <= 0 {
		return syscall.EINVAL
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}
