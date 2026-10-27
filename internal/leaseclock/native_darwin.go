package leaseclock

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// CLOCK_MONOTONIC_RAW is mach_continuous_time in nanoseconds on macOS.
// kern.bootsessionuuid is stable under calendar corrections (unlike boottime).
func Read() (Sample, error) {
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return Sample{}, err
	}
	boot = strings.ToLower(boot)
	id, err := uuid.Parse(boot)
	if err != nil || id.String() != boot {
		return Sample{}, fmt.Errorf("invalid boot session UUID")
	}
	var ts unix.Timespec
	if err = unix.ClockGettime(unix.CLOCK_MONOTONIC_RAW, &ts); err != nil {
		return Sample{}, err
	}
	n, err := tick(ts.Sec, ts.Nsec)
	return Sample{Boot: "darwin:" + boot, Tick: n, Wall: time.Now().UTC()}, err
}
