package observe

import (
	"context"
	"fmt"
	"time"

	"github.com/inferal-oss/arm/internal/model"
	"golang.org/x/sys/unix"
)

func memorySnapshot(ctx context.Context) model.ResourceMeasurement {
	return memoryAvailabilityValues(MemoryAvailability(ctx))
}
func meminfo(data []byte) model.ResourceMeasurement {
	return memoryAvailabilityValues(parseMemoryAvailability(data, time.Now().UTC()))
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
