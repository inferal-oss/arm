//go:build arm_forward_review && arm_forward_unix_prototype && darwin

package meshforward

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Darwin lacks SOCK_CLOEXEC here. Match Go's fork/exec coordination from
// socket creation through verified FD_CLOEXEC (or original close on failure).
func (p *UnixPrototype) openRawSocket() error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	p.setupRaw = os.NewFile(uintptr(fd), "source-unix-listener")
	_, err = unix.FcntlInt(uintptr(fd), unix.F_SETFD, unix.FD_CLOEXEC)
	if err == nil {
		var flags int
		flags, err = unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err == nil && flags&unix.FD_CLOEXEC == 0 {
			err = errors.New("protected source Unix socket CLOEXEC absent")
		}
	}
	if err != nil {
		closeErr := p.setupRaw.Close() // still under ForkLock
		p.setupRawClosed = true
		return errors.Join(err, closeErr)
	}
	return nil
}
