package observe

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func DiskAvailability(ctx context.Context, path string) model.DiskAvailability {
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
	size := st.Frsize
	if size <= 0 {
		size = st.Bsize
	}
	if size <= 0 {
		return r
	}
	name := fmt.Sprintf("linux-magic:0x%x", uint64(st.Type))
	switch st.Type {
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
	r = diskCounters(absolute, fmt.Sprintf("fsid:%x", st.Fsid.Val), name, st.Blocks, st.Bfree, st.Bavail, uint64(size), at)
	r.Shared = name == "btrfs" || name == "xfs" || name == "zfs" || name == "overlay"
	if r.Shared {
		r.Detail += " Shared backing topology is unknown; mount reports must not be summed as exclusive capacity."
	}
	return r
}
