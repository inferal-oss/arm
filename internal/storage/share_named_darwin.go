package storage

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func readOnlyNamedAt(fd int, name string) (readOnlyNamedObservation, error) {
	var s unix.Stat_t
	if err := unix.Fstatat(fd, name, &s, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return readOnlyNamedObservation{}, err
	}
	return readOnlyDarwinNamedObservation(s)
}

func readOnlyDarwinNamedObservation(s unix.Stat_t) (readOnlyNamedObservation, error) {
	if s.Btim.Sec == 0 && s.Btim.Nsec == 0 {
		return readOnlyNamedObservation{}, fmt.Errorf("creation-time identity unavailable for read-only resolution")
	}
	id := Identity(&s)
	id.BornSeconds = s.Btim.Sec
	id.BornNanoseconds = s.Btim.Nsec
	id.BirthKnown = true
	return readOnlyNamedObservation{Identity: id, Mode: uint32(s.Mode)}, nil
}
