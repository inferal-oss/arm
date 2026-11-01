package agentstatus

import "golang.org/x/sys/unix"

// Set close-on-exec atomically; Dup followed by CloseOnExec leaks a window to
// concurrent subprocess creation.
func duplicatePrivateFD(fd int) (int, error) {
	return unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
}
