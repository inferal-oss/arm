//go:build darwin

package manager

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

func forwardUnixPeerUID(c *net.UnixConn) (uint32, error) {
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
