package observe

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func collectCPUAvailability(ctx context.Context) (cpuAvailabilityRaw, error) {
	r := cpuAvailabilityRaw{scope: "host-active-logical-cpus", method: "Mach:host_processor_info+sysctl:hw.activecpu", detail: "Recent idle time across active logical CPUs; includes all workloads and is not a performance guarantee."}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	before, err := unix.SysctlUint32("hw.activecpu")
	if err != nil {
		return r, err
	}
	r.rows, err = darwinCPU(cpuObservationMaxCPUs)
	if err != nil {
		return r, err
	}
	after, err := unix.SysctlUint32("hw.activecpu")
	if err != nil {
		return r, err
	}
	if before == 0 || before != after || int(after) != len(r.rows) {
		return r, fmt.Errorf("active CPU inventory changed or cannot be mapped to native counters")
	}
	if err := validateCPURows(r.rows); err != nil {
		return r, err
	}
	r.at, r.capacity = time.Now(), float64(after)
	r.signature = fmt.Sprintf("mach-active:%d", after)
	return r, ctx.Err()
}
