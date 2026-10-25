package storage

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestObservedReadOnlyDarwinRequiresNativeBirthIdentity(t *testing.T) {
	s := unix.Stat_t{Mode: unix.S_IFDIR | 0700, Ino: 19, Uid: 501, Nlink: 2, Size: 4096, Dev: 7}
	fs := unix.Statfs_t{}
	if got, err := readOnlyDarwinObservation(s, fs); err == nil || got != (ReadOnlyObservation{}) {
		t.Fatal("missing native birth accepted", got, err)
	}
	s.Btim.Sec = 10
	s.Btim.Nsec = 11
	s.Mtim.Sec = 12
	s.Mtim.Nsec = 13
	s.Ctim.Sec = 14
	s.Ctim.Nsec = 15
	got, err := readOnlyDarwinObservation(s, fs)
	if err != nil || got.Identity.Inode != 19 || !got.Identity.BirthKnown || got.Identity.BornSeconds != 10 || got.Identity.BornNanoseconds != 11 || got.ModifiedSeconds != 12 || got.ModifiedNanos != 13 || got.ChangedSeconds != 14 || got.ChangedNanos != 15 {
		t.Fatal("complete native observation differs", got, err)
	}
}
