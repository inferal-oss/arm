package leaseclock

import (
	"fmt"
	"golang.org/x/sys/unix"
	"syscall"
	"testing"
)

func observeStopped(pid int) (bool, error) {
	var info unix.Siginfo
	if err := unix.Waitid(unix.P_PID, pid, &info, unix.WSTOPPED|unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil); err != nil {
		return false, err
	}
	if info.Signo == 0 {
		return false, nil
	}
	if info.Signo != int32(syscall.SIGCHLD) || info.Errno != 0 || info.Code != 5 {
		return false, fmt.Errorf("child not stopped: non-reaping siginfo %+v", info)
	}
	return true, nil
}

func TestInitialTimeNamespaceRejectsReusedOrUnverifiedScopes(t *testing.T) {
	if err := initialTimeNamespace(0xEFFFFFFA, unix.NSFS_MAGIC, unix.CLONE_NEWTIME); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		inode uint64
		fs    int64
		kind  int
	}{{12, unix.NSFS_MAGIC, unix.CLONE_NEWTIME}, {0xEFFFFFFA, 0, unix.CLONE_NEWTIME}, {0xEFFFFFFA, unix.NSFS_MAGIC, unix.CLONE_NEWCGROUP}} {
		if err := initialTimeNamespace(v.inode, v.fs, v.kind); err == nil {
			t.Fatal(v)
		}
	}
}
