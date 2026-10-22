package sharefs

import (
	"fmt"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func version(fd int) (Version, error) {
	var s unix.Stat_t
	if err := unix.Fstat(fd, &s); err != nil {
		return Version{}, err
	}
	mount, err := nativeMountIdentity(fd)
	if err != nil {
		return Version{}, err
	}
	if s.Btim.Sec == 0 && s.Btim.Nsec == 0 {
		return Version{}, fmt.Errorf("native file birth identity unavailable")
	}
	return Version{Identity: model.FileIdentity{Device: uint64(s.Dev), Inode: s.Ino, Owner: s.Uid, BornSeconds: s.Btim.Sec, BornNanoseconds: s.Btim.Nsec, BirthKnown: true}, Mount: mount, Size: s.Size, Mode: uint32(s.Mode), Links: uint64(s.Nlink), ModifiedSeconds: s.Mtim.Sec, ModifiedNanos: s.Mtim.Nsec, ChangedSeconds: s.Ctim.Sec, ChangedNanos: s.Ctim.Nsec}, nil
}

func nativeMountIdentity(fd int) (string, error) {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", fs.Fsid.Val), nil
}
