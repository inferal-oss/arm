package observe

import (
	"context"
	"fmt"
	"strings"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func memorySnapshot(ctx context.Context) model.ResourceMeasurement {
	return memoryAvailabilityValues(MemoryAvailability(ctx))
}

func filesystemSnapshot(ctx context.Context, path string) model.ResourceMeasurement {
	var s unix.Statfs_t
	if err := unix.Statfs(path, &s); err != nil {
		return unavailable(path, "statfs", err.Error())
	}
	r := filesystemValues(path, "statfs", s.Blocks, s.Bfree, s.Bavail, uint64(s.Bsize))
	fs := &model.Filesystem{Path: path, Type: unix.ByteSliceToString(s.Fstypename[:]), Volume: fmt.Sprintf("fsid:%x", s.Fsid.Val), Mount: unix.ByteSliceToString(s.Mntonname[:]), IdentityQuality: "mount-session", PoolTotal: unknown("bytes", "filesystem-pool", "statfs", "pool relationships unavailable"), PoolFree: unknown("bytes", "filesystem-pool", "statfs", "pool relationships unavailable")}
	r.Filesystem = fs
	if fs.Type != "apfs" {
		fs.Detail = "Filesystem block reports do not establish exclusive file ownership or reclaimable bytes"
		return r
	}
	fs.Shared = true
	fs.Detail = "APFS volumes share container capacity; file blocks may be shared by clones and snapshots; never sum volume totals as independent capacity"
	// statfs's mounted device handles arbitrary paths and firmlinks; diskutil
	// info accepts a device/mount, not an arbitrary subdirectory.
	device := unix.ByteSliceToString(s.Mntfromname[:])
	data, err := command(ctx, "/usr/sbin/diskutil", "info", "-plist", device)
	if err != nil {
		fs.Detail += "; " + err.Error()
		return r
	}
	info, err := plist(data)
	if err != nil {
		fs.Detail += "; invalid volume metadata"
		return r
	}
	if id, _ := info["VolumeUUID"].(string); id != "" {
		fs.Volume = "apfs-volume:" + strings.ToLower(id)
	}
	ref, _ := info["APFSContainerReference"].(string)
	if ref == "" {
		fs.Detail += "; container identity unavailable"
		return r
	}
	data, err = command(ctx, "/usr/sbin/diskutil", "apfs", "list", "-plist", ref)
	if err != nil {
		fs.Detail += "; " + err.Error()
		return r
	}
	c, err := apfsPool(data, ref)
	if err != nil {
		fs.Detail += "; " + err.Error()
		return r
	}
	fs.Pool = "apfs-container:" + strings.ToLower(c["APFSContainerUUID"].(string))
	fs.IdentityQuality = "persistent-uuid"
	if !strings.HasPrefix(fs.Volume, "apfs-volume:") {
		fs.IdentityQuality = "persistent-pool-uuid"
	}
	total, okTotal := c["CapacityCeiling"].(uint64)
	free, okFree := c["CapacityFree"].(uint64)
	if okTotal && okFree && free <= total {
		fs.PoolTotal = known(float64(total), "bytes", fs.Pool, "diskutil:APFS CapacityCeiling", "reported")
		fs.PoolFree = known(float64(free), "bytes", fs.Pool, "diskutil:APFS CapacityFree", "reported")
		fs.PoolFree.Detail = "Container free blocks; excludes conditional purgeable space; volume quotas and reserves may further restrict allocation"
	}
	return r
}
