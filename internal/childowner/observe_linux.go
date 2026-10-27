package childowner

import (
	"fmt"
	"golang.org/x/sys/unix"
	"runtime"
	"syscall"
	"unsafe"
)

// Linux amd64/arm64 siginfo_t child union starts at byte16. Both supported
// targets use the same fixed int32 PID/UID/status layout.
type linuxChildInfo struct {
	Signo, Errno, Code, Pad, PID int32
	UID                          uint32
	Status                       int32
}

func observeTerminal(pid int) (terminal, error) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return terminal{}, fmt.Errorf("unsupported Linux child observation ABI")
	}
	var raw unix.Siginfo
	if err := unix.Waitid(unix.P_PID, pid, &raw, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil); err != nil {
		return terminal{}, err
	}
	info := (*linuxChildInfo)(unsafe.Pointer(&raw))
	if info.PID == 0 {
		return terminal{}, nil
	}
	if info.PID != int32(pid) || info.Signo != int32(syscall.SIGCHLD) || info.Errno != 0 {
		return terminal{}, fmt.Errorf("crossed or malformed Linux child observation")
	}
	switch info.Code {
	case 1:
		return terminal{ready: true, code: int(info.Status) & 255}, nil
	case 2, 3:
		if info.Status > 0 && info.Status < 65 {
			return terminal{ready: true, signal: syscall.Signal(info.Status)}, nil
		}
	}
	return terminal{}, fmt.Errorf("unsupported Linux terminal observation code=%d status=%d", info.Code, info.Status)
}

// Linux retains the existing terminal group KILL; only Darwin needs the
// positive sole-terminal-member witness to avoid zombie-only killpg EPERM.
func terminalOnlyGroup(int) (bool, error) { return false, nil }

const nativeTerminalGroupGrace = 0

const nativePreflightSignals = false
