package observe

import (
	"golang.org/x/sys/unix"
	"os"
)

func benchmarkCacheMode(_ *os.File) (string, error) {
	return "POSIX_FADV_DONTNEED (advisory); buffered write+fsync; QD1", nil
}
func benchmarkDropCache(f *os.File, size int64) error {
	return unix.Fadvise(int(f.Fd()), 0, size, unix.FADV_DONTNEED)
}
