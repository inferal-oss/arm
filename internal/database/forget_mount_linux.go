//go:build linux

package database

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

// Mount ID distinguishes same-device bind mounts, unlike st_dev. Kernels that
// cannot supply it fail closed; there is no device-number fallback.
func forgetMountIdentity(f *os.File) (string, error) {
	var st unix.Statx_t
	if err := unix.Statx(int(f.Fd()), "", unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &st); err != nil {
		return "", fmt.Errorf("forget requires descriptor mount identity: %w", err)
	}
	if st.Mask&unix.STATX_MNT_ID == 0 || st.Mnt_id == 0 {
		return "", fmt.Errorf("forget requires supported Linux STATX_MNT_ID")
	}
	return fmt.Sprintf("linux-mount:%d", st.Mnt_id), nil
}
