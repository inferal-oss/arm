//go:build linux

package nodestop

import "golang.org/x/sys/unix"

// Linux CLOCK_BOOTTIME includes suspended time and cannot be set as wall time.
func nativeReviewTick() (int64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		return 0, err
	}
	return clockTick(ts.Sec, ts.Nsec)
}
