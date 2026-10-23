//go:build darwin

package providerexec

import (
	"golang.org/x/sys/unix"
	"os"
)

func stamp(f *os.File) (fileStamp, error) {
	var s unix.Stat_t
	err := unix.Fstat(int(f.Fd()), &s)
	return fileStamp{uint64(s.Dev), s.Ino, uint32(s.Mode), s.Uid, uint64(s.Nlink), s.Size, s.Mtim.Sec, s.Mtim.Nsec, s.Ctim.Sec, s.Ctim.Nsec}, err
}
