package observe

import (
	"context"
	"math"
	"runtime"
	"time"
	"unsafe"

	"github.com/inferal-oss/arm/internal/model"
	"golang.org/x/sys/unix"
)

func MemoryAvailability(ctx context.Context) model.MemoryAvailability {
	at := time.Now().UTC()
	r := model.MemoryAvailability{At: at, Scope: "host-free-and-file-backed-memory", Method: "Mach HOST_VM_INFO64; sysctl hw.memsize/hw.pagesize", Quality: "unavailable", Detail: "Host memory counters unavailable"}
	if ctx.Err() != nil || initMonitorMach() != nil {
		return r
	}
	total, e1 := unix.SysctlUint64("hw.memsize")
	page, e2 := unix.SysctlUint32("hw.pagesize")
	if e1 != nil || e2 != nil || total == 0 || total > math.MaxInt64 || page == 0 {
		return r
	}
	n := int64(total)
	r.Total = &n
	// Request the public vm_statistics64 revision-1 prefix (152 bytes).
	// Newer revisions append fields; the kernel honors the supplied count.
	// external_page_count counts pageable file-backed pages; unlike inactive
	// pages, it excludes anonymous memory that would require compression/swap.
	// See apple-oss-distributions/xnu osfmk/mach/vm_statistics.h and host.c.
	var stats [38]uint32
	count := uint32(len(stats))
	host := monitorMach.host()
	defer monitorMach.portDeallocate(monitorMach.task(), host)
	if monitorMach.statistics64(host, 4, unsafe.Pointer(&stats[0]), &count) != 0 || count < 38 {
		return r
	}
	free, external := uint64(stats[0]), uint64(stats[34])
	runtime.KeepAlive(&stats)
	return darwinAvailability(total, uint64(page), free, external, at)
}

func darwinAvailability(total, page, free, external uint64, at time.Time) model.MemoryAvailability {
	r := model.MemoryAvailability{At: at, Scope: "host-free-and-file-backed-memory", Method: "Mach HOST_VM_INFO64; sysctl hw.memsize/hw.pagesize", Quality: "unavailable", Detail: "Invalid native memory counters"}
	if total == 0 || total > math.MaxInt64 || page == 0 || free > math.MaxInt64/page || external > math.MaxInt64/page || free > math.MaxUint64-external {
		return r
	}
	pages := free + external
	if pages > math.MaxInt64/page || pages*page > total {
		return r
	}
	n, available := int64(total), int64(pages*page)
	r.Total = &n
	r.Available = &available
	r.Quality = "estimated"
	r.Detail = "Free and pageable file-backed memory, including caches that may require writeback. Anonymous inactive pages, compression and swap are not added. Advisory host estimate, not guaranteed exclusive allocation."
	return r
}
