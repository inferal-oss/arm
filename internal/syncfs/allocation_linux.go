package syncfs

import (
	"errors"
	"strconv"

	"golang.org/x/sys/unix"
)

func surveyMountFD(fd int) (string, error) {
	var s unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &s); err != nil {
		return "", err
	}
	if s.Mask&unix.STATX_MNT_ID == 0 {
		return "", errors.New("workspace mount identity unavailable")
	}
	return strconv.FormatUint(s.Mnt_id, 10), nil
}

func surveyMountAt(fd int, name string) (string, error) {
	var s unix.Statx_t
	if err := unix.Statx(fd, name, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_MNT_ID, &s); err != nil {
		return "", err
	}
	if s.Mask&unix.STATX_MNT_ID == 0 {
		return "", errors.New("workspace child mount identity unavailable")
	}
	return strconv.FormatUint(s.Mnt_id, 10), nil
}
