package storage

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestObservedReadOnlyRequiresEveryNativeStatxMask(t *testing.T) {
	s := unix.Statx_t{Mask: readOnlyObservationMask, Mode: unix.S_IFDIR | 0700, Ino: 19, Uid: 501, Nlink: 2, Size: 4096, Mnt_id: 7, Dev_major: 1, Dev_minor: 2}
	s.Btime.Sec = 10
	s.Btime.Nsec = 11
	s.Mtime.Sec = 12
	s.Mtime.Nsec = 13
	s.Ctime.Sec = 14
	s.Ctime.Nsec = 15
	if got, err := readOnlyStatxObservation(s); err != nil || got.Identity.Inode != 19 || got.Mount != "7" || got.ModifiedSeconds != 12 || got.ModifiedNanos != 13 || got.ChangedSeconds != 14 || got.ChangedNanos != 15 {
		t.Fatal("complete native mask refused", got, err)
	}
	for bit := uint32(1); bit <= readOnlyObservationMask; bit <<= 1 {
		if readOnlyObservationMask&bit == 0 {
			continue
		}
		bad := s
		bad.Mask &^= bit
		if got, err := readOnlyStatxObservation(bad); err == nil || got != (ReadOnlyObservation{}) {
			t.Fatalf("missing native mask%#x accepted: %+v %v", bit, got, err)
		}
	}
}
