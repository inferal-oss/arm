package storage

import (
	"testing"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func TestReadOnlyNamedDarwinRequiresBirthAndPreservesFields(t *testing.T) {
	s := unix.Stat_t{Mode: unix.S_IFDIR | 0751, Ino: 29, Uid: 701, Dev: 13}
	if got, err := readOnlyDarwinNamedObservation(s); err == nil || got != (readOnlyNamedObservation{}) {
		t.Fatal("missing birth disclosed a named observation", got, err)
	}
	s.Btim.Sec, s.Btim.Nsec = 41, 43
	want := readOnlyNamedObservation{Identity: model.FileIdentity{Device: 13, Inode: 29, Owner: 701, BornSeconds: 41, BornNanoseconds: 43, BirthKnown: true}, Mode: unix.S_IFDIR | 0751}
	got, err := readOnlyDarwinNamedObservation(s)
	if err != nil || got != want {
		t.Fatal("coherent named fields differ", got, want, err)
	}
	// A nonzero nanosecond alone is still a reported native birth identity.
	s.Btim.Sec = 0
	got, err = readOnlyDarwinNamedObservation(s)
	if err != nil || !got.Identity.BirthKnown || got.Identity.BornSeconds != 0 || got.Identity.BornNanoseconds != 43 {
		t.Fatal("subsecond birth lost", got, err)
	}
}
