package syncfs

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func surveyMountFD(fd int) (string, error) {
	var s unix.Statfs_t
	if err := unix.Fstatfs(fd, &s); err != nil {
		return "", err
	}
	return surveyDarwinMountIdentity(s)
}

// Darwin exposes no STATX_MNT_ID equivalent here. Pair its descriptor-reported
// filesystem ID with the nonempty reported mountpoint, including same-Fsid
// mounts that a filesystem-ID-only comparison would conflate.
func surveyDarwinMountIdentity(s unix.Statfs_t) (string, error) {
	name := unix.ByteSliceToString(s.Mntonname[:])
	if name == "" {
		return "", fmt.Errorf("workspace mountpoint identity unavailable: %w", ErrUnknown)
	}
	return fmt.Sprintf("darwin-mount:%v:%s", s.Fsid, name), nil
}

func surveyMountAt(fd int, name string) (string, error) {
	child, err := unix.Openat(fd, name, unix.O_EVTONLY|unix.O_SYMLINK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	defer unix.Close(child)
	return surveyMountFD(child)
}
