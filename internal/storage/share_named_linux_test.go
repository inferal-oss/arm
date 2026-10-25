package storage

import (
	"fmt"
	"testing"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func TestReadOnlyNamedStatxRequiresEveryConsumedField(t *testing.T) {
	s := unix.Statx_t{Mask: readOnlyNamedMask, Mode: unix.S_IFDIR | 0751, Ino: 29, Uid: 701, Dev_major: 3, Dev_minor: 17}
	s.Btime.Sec, s.Btime.Nsec = 41, 43
	want := readOnlyNamedObservation{Identity: model.FileIdentity{Device: unix.Mkdev(3, 17), Inode: 29, Owner: 701, BornSeconds: 41, BornNanoseconds: 43, BirthKnown: true}, Mode: unix.S_IFDIR | 0751}
	if got, err := readOnlyStatxNamedObservation(s); err != nil || got != want {
		t.Fatal("complete native fields differ", got, want, err)
	}
	for _, field := range []struct {
		name string
		mask uint32
	}{{"type", unix.STATX_TYPE}, {"mode", unix.STATX_MODE}, {"owner", unix.STATX_UID}, {"inode", unix.STATX_INO}, {"birth", unix.STATX_BTIME}} {
		t.Run(field.name, func(t *testing.T) {
			bad := s
			bad.Mask &^= field.mask
			if got, err := readOnlyStatxNamedObservation(bad); err == nil || got != (readOnlyNamedObservation{}) {
				t.Fatal("missing consumed field accepted", fmt.Sprintf("%#x", field.mask), got, err)
			}
		})
	}
	// Optional statx fields do not supply missing authority or needlessly gate
	// this identity-only observation. The opened-FD full version stays separate.
	s.Mask |= unix.STATX_SIZE | unix.STATX_MTIME | unix.STATX_CTIME | unix.STATX_MNT_ID
	if got, err := readOnlyStatxNamedObservation(s); err != nil || got != want {
		t.Fatal("additional native fields changed named identity", got, err)
	}
}
