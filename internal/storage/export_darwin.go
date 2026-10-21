package storage

import (
	"fmt"
	"math"

	"golang.org/x/sys/unix"
)

func availableExportBytes(fd int) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Fstatfs(fd, &st); err != nil {
		return 0, err
	}
	if st.Bsize <= 0 || st.Bavail > math.MaxInt64/uint64(st.Bsize) {
		return 0, fmt.Errorf("invalid filesystem availability counters")
	}
	return int64(st.Bavail * uint64(st.Bsize)), nil
}
