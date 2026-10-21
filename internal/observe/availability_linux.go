package observe

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func MemoryAvailability(ctx context.Context) model.MemoryAvailability {
	at := time.Now().UTC()
	data, err := readMonitorFile(ctx, "/proc/meminfo")
	if err != nil {
		return model.MemoryAvailability{At: at, Scope: "host", Method: "proc:meminfo MemTotal/MemAvailable", Quality: "unavailable", Detail: err.Error()}
	}
	return parseMemoryAvailability(data, at)
}
func parseMemoryAvailability(data []byte, at time.Time) model.MemoryAvailability {
	r := model.MemoryAvailability{At: at, Scope: "host", Method: "proc:meminfo MemTotal/MemAvailable", Quality: "unavailable", Detail: "Required host memory counters unavailable; cgroup allowance is not inferred"}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || (fields[0] != "MemTotal:" && fields[0] != "MemAvailable:") {
			continue
		}
		if seen[fields[0]] {
			r.Available = nil
			r.Quality = "unavailable"
			r.Detail = "Duplicate memory counter"
			return r
		}
		seen[fields[0]] = true
		if len(fields) != 3 || fields[2] != "kB" {
			r.Available = nil
			r.Detail = "Malformed memory counter or unexpected unit"
			return r
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || n > math.MaxInt64/1024 {
			r.Available = nil
			return r
		}
		value := int64(n * 1024)
		if fields[0] == "MemTotal:" {
			r.Total = &value
		} else {
			r.Available = &value
		}
	}
	if r.Total == nil || *r.Total <= 0 || r.Available == nil || *r.Available > *r.Total {
		r.Available = nil
		return r
	}
	r.Quality = "estimated"
	r.Detail = "Kernel MemAvailable estimate includes reclaimable caches. Host scope; not a container/cgroup quota or an allocation guarantee."
	return r
}
