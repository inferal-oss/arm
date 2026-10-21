// Package observe supplies read-only measurements. It never establishes
// deletion authority and never treats a file's block count as exclusive space.
package observe

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func known(value float64, unit, scope, method, quality string) model.Measurement {
	return model.Measurement{Value: &value, Unit: unit, Scope: scope, Method: method, Quality: quality, At: time.Now().UTC(), Complete: true}
}
func unknown(unit, scope, method, detail string) model.Measurement {
	return model.Measurement{Unit: unit, Scope: scope, Method: method, Quality: "unavailable", At: time.Now().UTC(), Detail: detail}
}
func unavailable(scope, method, detail string) model.ResourceMeasurement {
	u := unknown("bytes", scope, method, detail)
	return model.ResourceMeasurement{Total: u, Available: u, Used: u}
}

func memoryAvailabilityValues(reading model.MemoryAvailability) model.ResourceMeasurement {
	r := unavailable(reading.Scope, reading.Method, reading.Detail)
	if reading.Total != nil {
		r.Total = known(float64(*reading.Total), "bytes", "host-physical-memory", reading.Method, "reported")
		r.Total.At = reading.At
	}
	if reading.Available == nil || reading.Total == nil {
		return r
	}
	r.Available = known(float64(*reading.Available), "bytes", reading.Scope, reading.Method, reading.Quality)
	r.Available.Detail = reading.Detail
	r.Available.At = reading.At
	r.Used = known(float64(*reading.Total-*reading.Available), "bytes", "host-total-minus-estimated-available", reading.Method, "estimated")
	r.Used.Detail = "Total minus estimated available memory, including reclaimable file caches in availability; not application-exclusive usage"
	r.Used.At = reading.At
	return r
}

// Snapshot measures the filesystem containing path, not all filesystems and not
// a lease's consumption. Platform commands are bounded by the caller's deadline.
func Snapshot(ctx context.Context, path string) map[string]model.ResourceMeasurement {
	cpu := model.ResourceMeasurement{
		Total:     known(float64(runtime.NumCPU()), "logical-core-equivalents", "process-visible-cpus", "runtime.NumCPU", "reported"),
		Used:      unknown("logical-core-equivalents", "host", "none", "CPU utilization sampling is not implemented"),
		Available: unknown("logical-core-equivalents", "host", "none", "idle CPU is not a performance guarantee"),
	}
	var memory, disk model.ResourceMeasurement
	var wg sync.WaitGroup
	wg.Go(func() { memory = memorySnapshot(ctx) })
	wg.Go(func() { disk = Filesystem(ctx, path) })
	wg.Wait()
	return map[string]model.ResourceMeasurement{"cpu": cpu, "memory": memory, "disk": disk}
}

func Filesystem(ctx context.Context, path string) model.ResourceMeasurement {
	abs, err := filepath.Abs(path)
	if err != nil {
		return unavailable(path, "statfs", err.Error())
	}
	return filesystemSnapshot(ctx, abs)
}

func blocksBytes(blocks, size uint64) (float64, error) {
	if size == 0 || blocks > math.MaxInt64/size {
		return 0, fmt.Errorf("invalid or overflowing filesystem block counters")
	}
	return float64(blocks * size), nil
}

func filesystemValues(path, method string, blocks, free, available, size uint64) model.ResourceMeasurement {
	if free > blocks || available > free {
		return unavailable(path, method, "inconsistent filesystem block counters")
	}
	total, err := blocksBytes(blocks, size)
	if err != nil {
		return unavailable(path, method, err.Error())
	}
	avail, _ := blocksBytes(available, size)
	used, _ := blocksBytes(blocks-free, size)
	return model.ResourceMeasurement{
		Total:     known(total, "bytes", "filesystem-containing:"+path, method, "reported"),
		Available: known(avail, "bytes", "nonprivileged-filesystem-available:"+path, method, "reported"),
		Used:      known(used, "bytes", "filesystem-blocks-in-use:"+path, method, "reported"),
	}
}
