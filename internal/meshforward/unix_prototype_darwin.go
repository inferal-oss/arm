//go:build arm_forward_review && arm_forward_unix_prototype && darwin

package meshforward

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

func unixPrototypePeerUID(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid uint32
	var inner error
	err = raw.Control(func(fd uintptr) {
		credential, e := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		inner = e
		if e == nil {
			// macOS sys/ucred.h defines XUCRED_VERSION as zero. A
			// different structure version has no trusted UID layout.
			if credential.Version != 0 {
				inner = errors.New("unknown Unix peer credential version")
			} else {
				uid = credential.Uid
			}
		}
	})
	if err != nil {
		return 0, err
	}
	return uid, inner
}
