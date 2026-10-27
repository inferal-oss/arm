package leaseclock

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// CLOCK_BOOTTIME includes suspend. Linux time namespaces can offset it, so the
// current thread must prove the initial time namespace for restart continuity.
func Read() (Sample, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	f, err := os.Open("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return Sample{}, err
	}
	b, readErr := io.ReadAll(io.LimitReader(f, 128))
	err = f.Close()
	if readErr != nil {
		return Sample{}, readErr
	}
	if err != nil {
		return Sample{}, err
	}
	boot := strings.TrimSpace(string(b))
	id, err := uuid.Parse(boot)
	if err != nil || id.String() != boot {
		return Sample{}, fmt.Errorf("invalid boot UUID")
	}
	path := fmt.Sprintf("/proc/self/task/%d/ns/time", unix.Gettid())
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return Sample{}, err
	}
	defer unix.Close(fd)
	var ns unix.Stat_t
	if err = unix.Fstat(fd, &ns); err != nil {
		return Sample{}, err
	}
	var fs unix.Statfs_t
	if err = unix.Fstatfs(fd, &fs); err != nil {
		return Sample{}, err
	}
	kind, err := unix.IoctlRetInt(fd, unix.NS_GET_NSTYPE)
	if err != nil {
		return Sample{}, err
	}
	if err = initialTimeNamespace(ns.Ino, fs.Type, kind); err != nil {
		return Sample{}, err
	}

	var ts unix.Timespec
	if err = unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts); err != nil {
		return Sample{}, err
	}
	n, err := tick(ts.Sec, ts.Nsec)
	return Sample{Boot: fmt.Sprintf("linux:%s:time:%d:%d", boot, ns.Dev, ns.Ino), Tick: n, Wall: time.Now().UTC()}, err
}

// Linux reserves this inode for init_time_ns. Dynamically allocated time
// namespace inodes can be reused after destruction; persisting one across
// restart would not establish clock continuity even with the same boot UUID.
func initialTimeNamespace(inode uint64, filesystem int64, kind int) error {
	if inode != 0xEFFFFFFA || filesystem != unix.NSFS_MAGIC || kind != unix.CLONE_NEWTIME {
		return fmt.Errorf("initial Linux time namespace provenance unavailable")
	}
	return nil
}
