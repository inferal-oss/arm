package usage

import (
	"fmt"
	"math"

	"getarm.org/internal/model"
)

// Sample is one process observation for host-wide inventory. It carries no
// control authority; callers must revalidate Identity before any action.
type Sample struct {
	Identity   model.ProcessIdentity
	PPID       int
	Name       string
	Stopped    bool // stopped by a signal or tracer
	CPUSeconds *float64
	Resident   *uint64
	Footprint  *uint64 // Darwin physical footprint; nil elsewhere
	DiskRead   *uint64 // cumulative storage bytes, when the platform permits
	DiskWrite  *uint64
}

// Inspect observes any visible process. Counters the caller may not read,
// such as another user's usage, are nil rather than zero.
func Inspect(pid int) (Sample, error) {
	if pid <= 0 || int64(pid) > math.MaxInt32 {
		return Sample{}, fmt.Errorf("PID must be a positive signed 32-bit value")
	}
	return inspect(pid)
}

// ListPIDs enumerates visible processes, bounded by limit.
func ListPIDs(limit int) ([]int, error) { return listPIDs(limit) }
