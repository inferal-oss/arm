//go:build linux

package syncfs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func readNativeRootEvidence(fd int, st unix.Stat_t) (rootCreationEvidence, error) {
	var x unix.Statx_t
	mask := unix.STATX_BASIC_STATS | unix.STATX_BTIME | unix.STATX_MNT_ID
	if e := unix.Statx(fd, "", unix.AT_EMPTY_PATH, mask, &x); e != nil {
		return rootCreationEvidence{}, fmt.Errorf("%w: statx: %v", ErrNativeIdentityUnavailable, e)
	}
	return linuxRootEvidence(x, st)
}

func linuxRootEvidence(x unix.Statx_t, st unix.Stat_t) (rootCreationEvidence, error) {
	if x.Mask&(unix.STATX_BTIME|unix.STATX_INO|unix.STATX_TYPE) != unix.STATX_BTIME|unix.STATX_INO|unix.STATX_TYPE ||
		x.Btime.Sec <= 0 || x.Btime.Nsec >= 1_000_000_000 ||
		x.Ino != st.Ino || x.Mode&unix.S_IFMT != unix.S_IFDIR {
		return rootCreationEvidence{}, ErrNativeIdentityUnavailable
	}
	out := rootCreationEvidence{Method: "linux-statx-btime", BirthSec: x.Btime.Sec, BirthNsec: x.Btime.Nsec}
	if x.Mask&unix.STATX_MNT_ID != 0 {
		mountID := x.Mnt_id
		out.MountID = &mountID
	}
	return out, nil
}
