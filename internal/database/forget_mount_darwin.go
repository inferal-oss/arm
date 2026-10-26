//go:build darwin

package database

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func forgetMountIdentity(f *os.File) (string, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil {
		return "", fmt.Errorf("forget requires descriptor mount identity: %w", err)
	}
	name := unix.ByteSliceToString(st.Mntonname[:])
	if name == "" {
		return "", fmt.Errorf("forget mount point identity unavailable")
	}
	return fmt.Sprintf("darwin-mount:%v:%s", st.Fsid, name), nil
}
