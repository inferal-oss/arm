package observe

import (
	"golang.org/x/sys/unix"
	"os"
)

func benchmarkCacheMode(f *os.File) (string, error) {
	_, err := unix.FcntlInt(f.Fd(), unix.F_NOCACHE, 1)
	return "F_NOCACHE; buffered write+fsync; QD1", err
}
func benchmarkDropCache(_ *os.File, _ int64) error { return nil }
