package sharefs

import (
	"fmt"
	"strconv"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func version(fd int) (Version, error) {
	var s unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_BASIC_STATS|unix.STATX_BTIME|unix.STATX_MNT_ID, &s); err != nil {
		return Version{}, err
	}
	needed := uint32(unix.STATX_BASIC_STATS | unix.STATX_BTIME | unix.STATX_MNT_ID)
	if s.Mask&needed != needed {
		return Version{}, fmt.Errorf("native file birth/mount/version identity unavailable")
	}
	return Version{Identity: model.FileIdentity{Device: unix.Mkdev(s.Dev_major, s.Dev_minor), Inode: s.Ino, Owner: s.Uid, BornSeconds: s.Btime.Sec, BornNanoseconds: int64(s.Btime.Nsec), BirthKnown: true}, Mount: strconv.FormatUint(s.Mnt_id, 10), Size: int64(s.Size), Mode: uint32(s.Mode), Links: uint64(s.Nlink), ModifiedSeconds: s.Mtime.Sec, ModifiedNanos: int64(s.Mtime.Nsec), ChangedSeconds: s.Ctime.Sec, ChangedNanos: int64(s.Ctime.Nsec)}, nil
}

func nativeMountIdentity(fd int) (string, error) {
	var s unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &s); err != nil {
		return "", err
	}
	if s.Mask&unix.STATX_MNT_ID == 0 {
		return "", fmt.Errorf("native mount identity unavailable")
	}
	return strconv.FormatUint(s.Mnt_id, 10), nil
}
