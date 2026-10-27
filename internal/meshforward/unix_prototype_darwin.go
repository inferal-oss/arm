//go:build arm_forward_review && arm_forward_unix_prototype && darwin

package meshforward

import (
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
			uid = credential.Uid
		}
	})
	if err != nil {
		return 0, err
	}
	return uid, inner
}
