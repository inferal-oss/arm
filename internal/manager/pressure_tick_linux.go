//go:build linux

package manager

import (
	"time"

	"golang.org/x/sys/unix"
)

func pressureNativeTick() (time.Duration, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		return -1, err
	}
	return time.Duration(ts.Sec)*time.Second + time.Duration(ts.Nsec), nil
}
