package historypack

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const lockName = ".historypack.lock"

type rootLock struct {
	file     *os.File
	identity Identity
}

func acquire(ctx context.Context, root *os.File, create bool, want *Identity) (*rootLock, error) {
	f, id, e := openFile(root, lockName, false)
	if errors.Is(e, unix.ENOENT) && create {
		f, id, e = openFile(root, lockName, true)
		if errors.Is(e, unix.EEXIST) {
			f, id, e = openFile(root, lockName, false)
		}
		if e == nil {
			e = root.Sync()
		}
	}
	if e != nil {
		if f != nil {
			f.Close()
		}
		return nil, e
	}
	fail := func(e error) (*rootLock, error) { f.Close(); return nil, e }
	if want != nil && id != *want {
		return fail(fmt.Errorf("publication lock identity changed"))
	}
	info, e := f.Stat()
	if e != nil || info.Size() != 0 {
		return fail(fmt.Errorf("publication lock must be empty"))
	}
	for {
		if e = ctx.Err(); e != nil {
			return fail(e)
		}
		e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if e == nil {
			break
		}
		if !errors.Is(e, unix.EWOULDBLOCK) && !errors.Is(e, unix.EAGAIN) {
			return fail(e)
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fail(ctx.Err())
		case <-timer.C:
		}
	}
	l := &rootLock{f, id}
	if e = l.check(root); e != nil {
		l.close()
		return nil, e
	}
	return l, nil
}
func checkLock(root *os.File, want Identity) error {
	f, id, e := openFile(root, lockName, false)
	if e != nil {
		return e
	}
	defer f.Close()
	if id != want {
		return fmt.Errorf("publication lock identity changed")
	}
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if info.Size() != 0 {
		return fmt.Errorf("publication lock is not empty")
	}
	return nil
}
func (l *rootLock) check(root *os.File) error { return checkLock(root, l.identity) }
func (l *rootLock) close()                    { unix.Flock(int(l.file.Fd()), unix.LOCK_UN); l.file.Close() }
