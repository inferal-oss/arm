//go:build linux

package manager

import (
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
		credential, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		inner = e
		if e == nil {
			uid = credential.Uid
		}
	})
	if err != nil {
		return 0, err
	}
	return uid, inner
}
