//go:build darwin

package syncfs

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinRootIDRefusesMissingBirthtime(t *testing.T) {
	if _, e := readNativeRootEvidence(0, unix.Stat_t{Gen: 12}); !errors.Is(e, ErrNativeIdentityUnavailable) {
		t.Fatalf("missing birthtime accepted: %v", e)
	}
	zeroGen, e := readNativeRootEvidence(0, unix.Stat_t{Btim: unix.Timespec{Sec: 1000}})
	if e != nil || zeroGen.Generation == nil || *zeroGen.Generation != 0 {
		t.Fatalf("reported zero generation was not represented: %+v %v", zeroGen, e)
	}
	st := unix.Stat_t{Btim: unix.Timespec{Sec: 1000, Nsec: 123}, Gen: 12}
	got, e := readNativeRootEvidence(0, st)
	if e != nil || got.BirthSec != 1000 || got.BirthNsec != 123 || got.Generation == nil || *got.Generation != 12 {
		t.Fatalf("native creation evidence lost: %+v %v", got, e)
	}
}
