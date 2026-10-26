//go:build linux

package syncfs

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func renameNoReplace(fromFD int, from string, toFD int, to string) error {
	return unix.Renameat2(fromFD, from, toFD, to, unix.RENAME_NOREPLACE)
}
func renameExchange(fromFD int, from string, toFD int, to string) error {
	return unix.Renameat2(fromFD, from, toFD, to, unix.RENAME_EXCHANGE)
}
func statMtime(s unix.Stat_t) (int64, int64) { return s.Mtim.Sec, s.Mtim.Nsec }
func statCtime(s unix.Stat_t) (int64, int64) { return s.Ctim.Sec, s.Ctim.Nsec }
func checkCasefoldDir(fd int) error {
	flags, e := unix.IoctlGetInt(fd, unix.FS_IOC_GETFLAGS)
	if e == nil && flags&0x40000000 != 0 {
		return fmt.Errorf("casefold-enabled Linux directory requires native name adapter")
	}
	return nil
}
