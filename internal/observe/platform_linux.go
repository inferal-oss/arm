package observe

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func memorySnapshot(ctx context.Context) model.ResourceMeasurement {
	r := unavailable("host-physical-memory", "proc:meminfo", "memory counters unavailable")
	if err := ctx.Err(); err != nil {
		r.Total.Detail = err.Error()
		return r
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		r.Total.Detail = err.Error()
		return r
	}
	return meminfo(data)
}
func meminfo(data []byte) model.ResourceMeasurement {
	r := unavailable("host-physical-memory", "proc:meminfo", "required meminfo counter unavailable")
	values := map[string]float64{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[2] != "kB" {
			continue
		}
		if f[0] != "MemTotal:" && f[0] != "MemAvailable:" {
			continue
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		bytes, err := blocksBytes(n, 1024)
		if err == nil {
			values[f[0]] = bytes
		}
	}
	total, ok := values["MemTotal:"]
	if !ok {
		return r
	}
	r.Total = known(total, "bytes", "host-physical-memory", "proc:meminfo MemTotal", "reported")
	if available, ok := values["MemAvailable:"]; ok && available <= total {
		r.Available = known(available, "bytes", "host-estimated-memory-available", "proc:meminfo MemAvailable", "estimated")
		r.Available.Detail = "Kernel estimate including reclaimable caches; host scope, not a container or cgroup allowance"
		r.Used = known(total-available, "bytes", "host-total-minus-available", "proc:meminfo MemTotal minus MemAvailable", "estimated")
	}
	return r
}
func filesystemSnapshot(ctx context.Context, path string) model.ResourceMeasurement {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return unavailable(path, "statfs", err.Error())
	}
	size := s.Frsize
	if size <= 0 {
		size = s.Bsize
	}
	if size <= 0 {
		return unavailable(path, "statfs", "invalid filesystem block size")
	}
	r := filesystemValues(path, "statfs", s.Blocks, s.Bfree, s.Bavail, uint64(size))
	name := fmt.Sprintf("linux-magic:0x%x", uint64(s.Type))
	switch s.Type {
	case unix.BTRFS_SUPER_MAGIC:
		name = "btrfs"
	case unix.XFS_SUPER_MAGIC:
		name = "xfs"
	case unix.EXT4_SUPER_MAGIC:
		name = "ext"
	case unix.TMPFS_MAGIC:
		name = "tmpfs"
	case unix.OVERLAYFS_SUPER_MAGIC:
		name = "overlay"
	case 0x2fc12fc1:
		name = "zfs"
	}
	fs := &model.Filesystem{Path: path, Type: name, Volume: fmt.Sprintf("fsid:%x", s.Fsid.Val), IdentityQuality: "mount-session", Shared: name == "btrfs" || name == "xfs" || name == "zfs" || name == "overlay", PoolTotal: unknown("bytes", "filesystem-pool", "statfs", "shared-pool topology has not been discovered"), PoolFree: unknown("bytes", "filesystem-pool", "statfs", "shared-pool topology has not been discovered"), Detail: "Filesystem block reports do not establish exclusive extents or reclaimable bytes; shared extents, snapshots and quotas need filesystem-specific providers"}
	r.Filesystem = fs
	return r
}
