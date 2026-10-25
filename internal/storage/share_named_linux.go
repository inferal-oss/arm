package storage

import (
	"fmt"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

// Device fields have no separate statx mask bit. Every other consumed field
// must be reported explicitly; unused size/time/mount fields are not requested.
const readOnlyNamedMask = uint32(unix.STATX_TYPE | unix.STATX_MODE | unix.STATX_UID | unix.STATX_INO | unix.STATX_BTIME)

func readOnlyNamedAt(fd int, name string) (readOnlyNamedObservation, error) {
	var s unix.Statx_t
	if err := unix.Statx(fd, name, unix.AT_SYMLINK_NOFOLLOW, int(readOnlyNamedMask), &s); err != nil {
		return readOnlyNamedObservation{}, err
	}
	return readOnlyStatxNamedObservation(s)
}

func readOnlyStatxNamedObservation(s unix.Statx_t) (readOnlyNamedObservation, error) {
	if s.Mask&readOnlyNamedMask != readOnlyNamedMask {
		return readOnlyNamedObservation{}, fmt.Errorf("native type/mode/owner/inode/birth identity unavailable for read-only resolution")
	}
	return readOnlyNamedObservation{
		Identity: model.FileIdentity{Device: unix.Mkdev(s.Dev_major, s.Dev_minor), Inode: s.Ino, Owner: s.Uid, BornSeconds: s.Btime.Sec, BornNanoseconds: int64(s.Btime.Nsec), BirthKnown: true},
		Mode:     uint32(s.Mode),
	}, nil
}
