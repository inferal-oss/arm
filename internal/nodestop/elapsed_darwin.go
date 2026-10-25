//go:build darwin

package nodestop

import "golang.org/x/sys/unix"

// Apple documents CLOCK_MONOTONIC_RAW as the nanosecond counterpart of
// mach_continuous_time, including time spent asleep.
func nativeReviewTick() (int64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &ts); err != nil {
		return 0, err
	}
	return clockTick(ts.Sec, ts.Nsec)
}
