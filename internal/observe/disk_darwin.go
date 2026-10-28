package observe

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

func NativeDiskAvailability(ctx context.Context, path string) model.DiskAvailability {
	at := time.Now().UTC()
	r := model.DiskAvailability{At: at, Scope: "state-filesystem", Path: path, Method: "statfs:f_bavail", Quality: "unavailable", Detail: "Filesystem availability is unavailable"}
	if ctx.Err() != nil {
		return r
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return r
	}
	var st unix.Statfs_t
	if err = unix.Statfs(absolute, &st); err != nil {
		r.Detail = err.Error()
		return r
	}
	r = diskCounters(absolute, fmt.Sprintf("fsid:%x", st.Fsid.Val), unix.ByteSliceToString(st.Fstypename[:]), st.Blocks, st.Bfree, st.Bavail, uint64(st.Bsize), at)
	return r
}

func DiskAvailability(ctx context.Context, path string) model.DiskAvailability {
	r := NativeDiskAvailability(ctx, path)
	if r.Filesystem != "apfs" || r.Available == nil {
		return r
	}
	r.Shared = true
	r.Detail += " APFS shared-container identity is not established by statfs alone."
	optional, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	var st unix.Statfs_t
	if unix.Statfs(r.Path, &st) != nil {
		return r
	}
	device := unix.ByteSliceToString(st.Mntfromname[:])
	data, err := command(optional, "/usr/sbin/diskutil", "info", "-plist", device)
	if err != nil {
		return r
	}
	info, err := plist(data)
	if err != nil {
		return r
	}
	ref, _ := info["APFSContainerReference"].(string)
	if ref == "" {
		return r
	}
	data, err = command(optional, "/usr/sbin/diskutil", "apfs", "list", "-plist", ref)
	if err != nil {
		return r
	}
	pool, err := apfsPool(data, ref)
	if err != nil {
		return r
	}
	total, tok := pool["CapacityCeiling"].(uint64)
	free, fok := pool["CapacityFree"].(uint64)
	if tok && fok {
		r = boundDiskPool(r, "apfs-container:"+strings.ToLower(pool["APFSContainerUUID"].(string)), total, free)
	}
	return r
}
