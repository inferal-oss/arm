package observe

import (
	"math"
	"strings"
	"time"

	"getarm.org/internal/model"
)

func diskCounters(path, volume, filesystem string, blocks, free, available, size uint64, at time.Time) model.DiskAvailability {
	r := model.DiskAvailability{At: at, Path: path, Scope: "state-filesystem:" + volume, Volume: volume, Filesystem: filesystem, Method: "statfs:f_bavail", Quality: "unavailable", Detail: "Invalid or overflowing filesystem counters"}
	if size == 0 || free > blocks || available > free || blocks > math.MaxInt64/size {
		return r
	}
	total, avail := int64(blocks*size), int64(available*size)
	if total <= 0 {
		return r
	}
	r.Total = &total
	r.Available = &avail
	r.Quality = "reported"
	r.Detail = "Space reported available to this user on ARM's state filesystem. Quotas, snapshots and shared extents may impose further limits; no exclusive or purgeable bytes are inferred."
	return r
}
func boundDiskPool(r model.DiskAvailability, pool string, total, free uint64) model.DiskAvailability {
	if pool == "" || free > total || total > math.MaxInt64 {
		return r
	}
	r.Pool = pool
	r.Scope = "state-filesystem-in-shared-pool:" + pool
	r.Shared = true
	if r.Available != nil {
		v := min(*r.Available, int64(free))
		r.Available = &v
	}
	r.Detail = strings.ReplaceAll(r.Detail, " APFS shared-container identity is not established by statfs alone.", "")
	r.Method += "; diskutil:APFS CapacityFree"
	r.Detail += " APFS container free blocks also bound this reading; volumes sharing the container are not independent capacity."
	return r
}
