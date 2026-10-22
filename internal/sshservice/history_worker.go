package sshservice

import (
	"errors"
	"fmt"
	"os"

	"github.com/yrashk/arm/internal/historypack"
	"golang.org/x/sys/unix"
)

const sourceHistoryWorkerLockName = ".worker.lock"

type historyWorker struct{ file *os.File }

func acquireHistoryWorker(c *historyCatalog) (*historyWorker, error) {
	if e := c.guard(); e != nil {
		return nil, e
	}
	fd, e := unix.Openat(int(c.directory.Fd()), sourceHistoryWorkerLockName, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), sourceHistoryWorkerLockName)
	id, _, e := sourceFileIdentity(c.directory, sourceHistoryWorkerLockName, 0)
	if e != nil || id != c.marker.WorkerLock {
		f.Close()
		return nil, fmt.Errorf("source history worker lock changed")
	}
	actual, e := statState(f)
	if e != nil || historyID(actual) != id {
		f.Close()
		return nil, fmt.Errorf("source history worker lock descriptor changed")
	}
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		if e == unix.EWOULDBLOCK || e == unix.EAGAIN {
			return nil, ErrHistoryBusy
		}
		return nil, e
	}
	if e = c.guard(); e != nil {
		unix.Flock(fd, unix.LOCK_UN)
		f.Close()
		return nil, e
	}
	return &historyWorker{f}, nil
}
func (w *historyWorker) Close() error {
	return errors.Join(unix.Flock(int(w.file.Fd()), unix.LOCK_UN), w.file.Close())
}

func (w *historyWorker) check(c *historyCatalog) error {
	id, size, e := sourceFileIdentity(c.directory, sourceHistoryWorkerLockName, 0)
	if e != nil || size != 0 || id != c.marker.WorkerLock {
		return fmt.Errorf("history worker lock named identity changed")
	}
	var st unix.Stat_t
	if e = unix.Fstat(int(w.file.Fd()), &st); e != nil {
		return e
	}
	actual := historypack.Identity{Device: uint64(st.Dev), Inode: st.Ino, Owner: st.Uid, Mode: uint32(st.Mode)}
	if actual != id || st.Size != 0 || st.Nlink != 1 {
		return fmt.Errorf("held history worker lock descriptor changed")
	}
	return nil
}
