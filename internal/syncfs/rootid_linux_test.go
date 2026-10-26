//go:build linux

package syncfs

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxRootIDRefusesMissingStatxBirthtime(t *testing.T) {
	st := unix.Stat_t{Ino: 42}
	x := unix.Statx_t{Mask: unix.STATX_INO | unix.STATX_TYPE, Ino: 42, Mode: unix.S_IFDIR}
	if _, e := linuxRootEvidence(x, st); !errors.Is(e, ErrNativeIdentityUnavailable) {
		t.Fatalf("missing STATX_BTIME mask accepted: %v", e)
	}
	x.Mask |= unix.STATX_BTIME
	x.Btime = unix.StatxTimestamp{Sec: 1000, Nsec: 123}
	got, e := linuxRootEvidence(x, st)
	if e != nil || got.BirthSec != 1000 || got.BirthNsec != 123 || got.MountID != nil {
		t.Fatalf("supported birthtime evidence rejected: %+v %v", got, e)
	}
}
