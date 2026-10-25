package storage

import (
	"fmt"
	"strconv"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

const readOnlyObservationMask = uint32(unix.STATX_BASIC_STATS | unix.STATX_BTIME | unix.STATX_MNT_ID)

func readOnlyOpenedObservation(fd int, expected model.FileIdentity) (ReadOnlyObservation, error) {
	var s unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, int(readOnlyObservationMask), &s); err != nil {
		return ReadOnlyObservation{}, err
	}
	observation, err := readOnlyStatxObservation(s)
	if err == nil && observation.Identity != expected {
		return ReadOnlyObservation{}, fmt.Errorf("artifact changed while opening")
	}
	return observation, err
}

func readOnlyStatxObservation(s unix.Statx_t) (ReadOnlyObservation, error) {
	if s.Mask&readOnlyObservationMask != readOnlyObservationMask {
		return ReadOnlyObservation{}, fmt.Errorf("native file birth/mount/version identity unavailable")
	}
	return ReadOnlyObservation{
		Identity: model.FileIdentity{Device: unix.Mkdev(s.Dev_major, s.Dev_minor), Inode: s.Ino, Owner: s.Uid, BornSeconds: s.Btime.Sec, BornNanoseconds: int64(s.Btime.Nsec), BirthKnown: true},
		Mount:    strconv.FormatUint(s.Mnt_id, 10), Size: int64(s.Size), Mode: uint32(s.Mode), Links: uint64(s.Nlink),
		ModifiedSeconds: s.Mtime.Sec, ModifiedNanos: int64(s.Mtime.Nsec), ChangedSeconds: s.Ctime.Sec, ChangedNanos: int64(s.Ctime.Nsec),
	}, nil
}
