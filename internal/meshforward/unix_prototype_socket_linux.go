//go:build arm_forward_review && arm_forward_unix_prototype && linux

package meshforward

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Linux creates the descriptor with CLOEXEC in the same syscall. There is no
// socket-to-fcntl fork/exec window and no fallback to an unprotected socket.
func (p *UnixPrototype) openRawSocket() error {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	p.setupRaw = os.NewFile(uintptr(fd), "source-unix-listener")
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		return err
	}
	if flags&unix.FD_CLOEXEC == 0 {
		return errors.New("atomic source Unix socket CLOEXEC absent")
	}
	return nil
}
