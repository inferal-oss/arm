//go:build darwin

package storage

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func sourceMountFD(fd int) (string, error) {
	var s unix.Statfs_t
	if err := unix.Fstatfs(fd, &s); err != nil {
		return "", err
	}
	return sourceDarwinMountIdentity(s)
}
func sourceDarwinMountIdentity(s unix.Statfs_t) (string, error) {
	name := unix.ByteSliceToString(s.Mntonname[:])
	if name == "" {
		return "", fmt.Errorf("source mountpoint identity unavailable")
	}
	return fmt.Sprintf("darwin-mount:%v:%s", s.Fsid, name), nil
}
