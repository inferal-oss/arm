package storage

import (
	"fmt"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func readOnlyOpenedObservation(fd int, expected model.FileIdentity) (ReadOnlyObservation, error) {
	var s unix.Stat_t
	if err := unix.Fstat(fd, &s); err != nil {
		return ReadOnlyObservation{}, err
	}
	// Preserve the original opened-identity refusal before querying its mount.
	// The full stat fields are retained for this same observation, not reread.
	if s.Btim.Sec == 0 && s.Btim.Nsec == 0 {
		return ReadOnlyObservation{}, fmt.Errorf("native file birth identity unavailable")
	}
	id := Identity(&s)
	id.BornSeconds = s.Btim.Sec
	id.BornNanoseconds = s.Btim.Nsec
	id.BirthKnown = true
	if id != expected {
		return ReadOnlyObservation{}, fmt.Errorf("artifact changed while opening")
	}
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return ReadOnlyObservation{}, err
	}
	return readOnlyDarwinObservation(s, fs)
}

func readOnlyDarwinObservation(s unix.Stat_t, fs unix.Statfs_t) (ReadOnlyObservation, error) {
	if s.Btim.Sec == 0 && s.Btim.Nsec == 0 {
		return ReadOnlyObservation{}, fmt.Errorf("native file birth identity unavailable")
	}
	return ReadOnlyObservation{
		Identity: model.FileIdentity{Device: uint64(s.Dev), Inode: s.Ino, Owner: s.Uid, BornSeconds: s.Btim.Sec, BornNanoseconds: s.Btim.Nsec, BirthKnown: true},
		Mount:    fmt.Sprintf("%x", fs.Fsid.Val), Size: s.Size, Mode: uint32(s.Mode), Links: uint64(s.Nlink),
		ModifiedSeconds: s.Mtim.Sec, ModifiedNanos: s.Mtim.Nsec, ChangedSeconds: s.Ctim.Sec, ChangedNanos: s.Ctim.Nsec,
	}, nil
}
