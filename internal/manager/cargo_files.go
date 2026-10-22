package manager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// New Cargo execution gets an exclusive log, never an append to an adopted
// symlink/FIFO/preexisting file. This runs only in the admitted async worker.
func cargoOpenLog(home, id string) (*os.File, error) {
	if !cargoHex(id, 32) {
		return nil, fmt.Errorf("Cargo log identity is invalid")
	}
	before, e := os.Lstat(home)
	if e != nil {
		return nil, e
	}
	root, e := os.Open(home)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	opened, e := root.Stat()
	if e != nil || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("Cargo log home changed")
	}
	check := func(fd int) error {
		var s unix.Stat_t
		if e := unix.Fstat(fd, &s); e != nil {
			return e
		}
		if s.Mode != unix.S_IFDIR|0700 || s.Uid != uint32(os.Geteuid()) {
			return fmt.Errorf("Cargo log namespace must be private/owned")
		}
		return nil
	}
	if e = check(int(root.Fd())); e != nil {
		return nil, e
	}
	if e = unix.Mkdirat(int(root.Fd()), "logs", 0700); e != nil && !errors.Is(e, unix.EEXIST) {
		return nil, e
	}
	fd, e := unix.Openat(int(root.Fd()), "logs", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	dir := os.NewFile(uintptr(fd), "logs")
	defer dir.Close()
	if e = check(fd); e != nil {
		return nil, e
	}
	target, e := unix.Openat(fd, id+".log", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	file := os.NewFile(uintptr(target), filepath.Join(home, "logs", id+".log"))
	named, e := os.Lstat(home)
	if e != nil || !os.SameFile(opened, named) {
		file.Close()
		return nil, fmt.Errorf("Cargo log home changed during creation")
	}
	if e = dir.Sync(); e != nil {
		file.Close()
		return nil, e
	}
	if e = root.Sync(); e != nil {
		file.Close()
		return nil, e
	}
	return file, nil
}
