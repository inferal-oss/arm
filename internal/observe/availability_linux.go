package observe

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/model"
)

func MemoryAvailability(ctx context.Context) model.MemoryAvailability {
	at := time.Now().UTC()
	data, err := readMonitorFile(ctx, "/proc/meminfo")
	if err != nil {
		return model.MemoryAvailability{At: at, Scope: "host", Method: "proc:meminfo MemTotal/MemAvailable", Quality: "unavailable", Detail: err.Error()}
	}
	r := parseMemoryAvailability(data, at)
	if r.Available == nil {
		return r
	}
	// Host MemAvailable can be much larger than the allowance of this process.
	// A missing or hidden ancestor must never become positive admission credit.
	capacity, free, limited, err := linuxMemoryCgroupHeadroom(ctx)
	if err != nil {
		r.Total, r.Available = nil, nil
		r.Scope = "memory-cgroup-unknown"
		r.Method = "proc:meminfo+cgroup:memory.max/current"
		r.Quality = "unavailable"
		r.Detail = "Complete memory cgroup headroom unavailable: " + err.Error()
		return r
	}
	if limited {
		*r.Total = min(*r.Total, capacity)
		*r.Available = min(*r.Available, free)
		r.Scope = "host+complete-memory-cgroup-ancestry"
		r.Method = "proc:meminfo+cgroup:memory.max/current"
		r.Detail = "Minimum host MemAvailable and conservative remaining memory.max across visible ancestors; memory.current includes reclaimable cache. Full promises remain charged."
	}
	return r
}

// This requires the initial cgroup namespace and a mount rooted at the real
// hierarchy root. A v1 memory hierarchy is left unknown until its usage and
// limit accounting have a separately validated admission interpretation.
func linuxMemoryCgroupHeadroom(ctx context.Context) (int64, int64, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	beforeNS, err := linuxCPUQuotaNamespace()
	if err != nil {
		return 0, 0, false, err
	}
	membership, err := readMonitorFile(ctx, "/proc/self/cgroup")
	if err != nil {
		return 0, 0, false, err
	}
	if err := linuxMemoryThreadMembership(ctx, membership); err != nil {
		return 0, 0, false, err
	}
	mountinfo, err := readMonitorFile(ctx, "/proc/self/mountinfo")
	if err != nil {
		return 0, 0, false, err
	}
	capacity, free, limited, err := memoryCgroupV2Headroom(ctx, membership, mountinfo)
	if err != nil {
		return 0, 0, false, err
	}
	afterMembership, err := readMonitorFile(ctx, "/proc/self/cgroup")
	if err != nil {
		return 0, 0, false, err
	}
	afterMountinfo, err := readMonitorFile(ctx, "/proc/self/mountinfo")
	if err != nil {
		return 0, 0, false, err
	}
	afterNS, err := linuxCPUQuotaNamespace()
	if err != nil || beforeNS != afterNS || string(membership) != string(afterMembership) || string(mountinfo) != string(afterMountinfo) {
		return 0, 0, false, fmt.Errorf("memory cgroup scope changed during observation")
	}
	if err := linuxMemoryThreadMembership(ctx, afterMembership); err != nil {
		return 0, 0, false, err
	}
	return capacity, free, limited, nil
}

func linuxMemoryThreadMembership(ctx context.Context, leader []byte) error {
	f, err := os.Open("/proc/self/task")
	if err != nil {
		return err
	}
	entries, readErr := f.ReadDir(4097)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || len(entries) == 0 || len(entries) > 4096 {
		return fmt.Errorf("complete native thread memory scope unavailable")
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		tid, err := strconv.Atoi(entry.Name())
		if err != nil || tid <= 0 {
			return fmt.Errorf("invalid native thread identity")
		}
		threadMembership, err := readMonitorFile(ctx, fmt.Sprintf("/proc/self/task/%d/cgroup", tid))
		if err != nil || !sameCPUThreadMembership(leader, threadMembership) {
			return fmt.Errorf("thread memory cgroup scopes differ or changed")
		}
		if _, err := linuxCPUQuotaNamespaceFile(fmt.Sprintf("/proc/self/task/%d/ns/cgroup", tid)); err != nil {
			return err
		}
	}
	return nil
}

func memoryCgroupV2Headroom(ctx context.Context, membership, mountinfo []byte) (int64, int64, bool, error) {
	members, err := parseCPUCgroupMembership(membership)
	if err != nil {
		return 0, 0, false, err
	}
	if _, v1 := members["memory"]; v1 {
		return 0, 0, false, fmt.Errorf("v1 memory cgroup accounting is unsupported")
	}
	member, ok := members["v2"]
	if !ok {
		return 0, 0, false, fmt.Errorf("memory cgroup membership is unavailable")
	}
	mounts, err := parseCPUCgroupMounts(mountinfo)
	if err != nil {
		return 0, 0, false, err
	}
	var mount *cpuCgroupMount
	for i := range mounts {
		if mounts[i].kind != "cgroup2" {
			continue
		}
		if mount != nil || mounts[i].root != "/" {
			return 0, 0, false, fmt.Errorf("memory cgroup mount is ambiguous or hides ancestors")
		}
		mount = &mounts[i]
	}
	if mount == nil {
		return 0, 0, false, fmt.Errorf("memory cgroup mount is unavailable")
	}
	rel, err := filepath.Rel("/", member)
	if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return 0, 0, false, fmt.Errorf("memory cgroup membership cannot be mapped")
	}
	current := filepath.Join(mount.path, rel)
	var capacity, free int64
	limited := false
	for depth := 0; ; depth++ {
		if err := ctx.Err(); err != nil {
			return 0, 0, false, err
		}
		if depth >= 128 {
			return 0, 0, false, fmt.Errorf("memory cgroup ancestry exceeds observation bound")
		}
		info, err := os.Lstat(current)
		if err != nil {
			return 0, 0, false, err
		}
		if !info.IsDir() {
			return 0, 0, false, fmt.Errorf("memory cgroup ancestor is not a directory")
		}
		limitData, err := readMonitorFile(ctx, filepath.Join(current, "memory.max"))
		if err != nil {
			// The hierarchy root may omit controller files. Missing files in
			// descendants do not establish an unlimited allowance.
			if current != mount.path || !os.IsNotExist(err) {
				return 0, 0, false, err
			}
		} else {
			limit, finite, err := parseMemoryCgroupValue(limitData, true)
			if err != nil {
				return 0, 0, false, err
			}
			if finite {
				usageData, err := readMonitorFile(ctx, filepath.Join(current, "memory.current"))
				if err != nil {
					return 0, 0, false, err
				}
				used, _, err := parseMemoryCgroupValue(usageData, false)
				if err != nil {
					return 0, 0, false, err
				}
				confirmedLimit, err := readMonitorFile(ctx, filepath.Join(current, "memory.max"))
				if err != nil || string(confirmedLimit) != string(limitData) {
					return 0, 0, false, fmt.Errorf("memory cgroup limit changed during observation")
				}
				remaining := max(int64(0), limit-used)
				if !limited || limit < capacity {
					capacity = limit
				}
				if !limited || remaining < free {
					free = remaining
				}
				limited = true
			}
		}
		if current == mount.path {
			break
		}
		parent := filepath.Dir(current)
		if parent == current || !strings.HasPrefix(parent, mount.path) {
			return 0, 0, false, fmt.Errorf("memory cgroup escaped visible hierarchy")
		}
		current = parent
	}
	return capacity, free, limited, nil
}

func parseMemoryCgroupValue(data []byte, allowMax bool) (int64, bool, error) {
	f := strings.Fields(string(data))
	if len(f) != 1 {
		return 0, false, fmt.Errorf("invalid memory cgroup counter")
	}
	if allowMax && f[0] == "max" {
		return 0, false, nil
	}
	n, err := strconv.ParseUint(f[0], 10, 64)
	if err != nil || n > math.MaxInt64 {
		return 0, false, fmt.Errorf("invalid memory cgroup counter")
	}
	return int64(n), true, nil
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
