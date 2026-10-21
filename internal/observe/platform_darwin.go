package observe

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func memorySnapshot(ctx context.Context) model.ResourceMeasurement {
	r := unavailable("host-physical-memory", "sysctl/vm_stat", "memory counters unavailable")
	if total, err := unix.SysctlUint64("hw.memsize"); err == nil {
		r.Total = known(float64(total), "bytes", "host-physical-memory", "sysctl:hw.memsize", "reported")
	} else {
		r.Total.Detail = err.Error()
	}
	data, err := command(ctx, "/usr/bin/vm_stat")
	if err != nil {
		r.Available.Detail = err.Error()
		r.Used.Detail = err.Error()
		return r
	}
	free, err := vmFree(data)
	if err != nil {
		r.Available.Detail = err.Error()
		return r
	}
	// Deliberately do not add inactive, purgeable or speculative lists: these
	// counters can overlap. This is a conservative free-pages measure, not
	// macOS's memory pressure or the maximum allocation that could succeed.
	r.Available = known(free, "bytes", "host-free-pages", "vm_stat:Pages free", "reported")
	r.Available.Detail = "Free pages only; excludes potential cache reclamation, compression and swap; not a memory-pressure estimate"
	if r.Total.Value != nil && free <= *r.Total.Value {
		r.Used = known(*r.Total.Value-free, "bytes", "host-nonfree-physical-memory", "hw.memsize minus vm_stat free pages", "estimated")
	}
	return r
}
func vmFree(data []byte) (float64, error) {
	pageSize := uint64(0)
	pages := uint64(0)
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		if _, tail, ok := strings.Cut(line, "page size of "); ok {
			fields := strings.Fields(tail)
			if len(fields) > 0 {
				pageSize, _ = strconv.ParseUint(fields[0], 10, 64)
			}
		}
		if _, tail, ok := strings.Cut(line, "Pages free:"); ok {
			n, err := strconv.ParseUint(strings.Trim(strings.TrimSpace(tail), "."), 10, 64)
			if err != nil {
				return 0, err
			}
			pages = n
			found = true
		}
	}
	if !found || pageSize == 0 {
		return 0, fmt.Errorf("vm_stat free-page counters unavailable")
	}
	return blocksBytes(pages, pageSize)
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
