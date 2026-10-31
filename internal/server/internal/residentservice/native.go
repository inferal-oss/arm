package residentservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const lockName = "resident-service.lock"
const journalName = "resident-service.journal"
const maxNativeFDs = 8

type retainedFD struct {
	number     int
	name       string
	file       *os.File
	id         NativeID
	mode       uint32
	closeTried bool
	closeError error
}

type nativeFiles struct {
	owner                                            *owner
	slots                                            [maxNativeFDs]*retainedFD
	home, lock, journal, parentChannel, childChannel *retainedFD
	opened, closed                                   uint64
}

func nativeID(stat *unix.Stat_t) NativeID {
	return NativeID{uint64(stat.Dev), uint64(stat.Ino), stat.Uid}
}

func (n *nativeFiles) registerRaw(fd int, name string) *retainedFD {
	for i, existing := range n.slots {
		if existing == nil {
			original := &retainedFD{number: fd, name: name}
			n.slots[i] = original
			n.opened++ // raw original recorded BEFORE os.NewFile/Fstat
			return original
		}
	}
	panic("resident service FD registry bound violated")
}
func (n *nativeFiles) materialize(original *retainedFD) error {
	n.progress("original_raw_file_retained_before_materialization")
	original.file = os.NewFile(uintptr(original.number), original.name)
	if original.file == nil {
		return ErrInvalid
	}
	n.progress("original_file_retained_before_stat")
	var stat unix.Stat_t
	if err := unix.Fstat(original.number, &stat); err != nil {
		return err
	}
	original.id = nativeID(&stat)
	original.mode = uint32(stat.Mode)
	return nil
}
func (n *nativeFiles) retain(fd int, name string) (*retainedFD, error) {
	original := n.registerRaw(fd, name)
	return original, n.materialize(original)
}

func (n *nativeFiles) capacity(count int) error {
	free := 0
	for _, f := range n.slots {
		if f == nil {
			free++
		}
	}
	if free < count {
		return fmt.Errorf("resident service native FD bound reached")
	}
	return nil
}

func (n *nativeFiles) closeAuxiliary(original *retainedFD) error {
	if original == nil || original.closeTried {
		return ErrInvalid
	}
	n.progress("original_auxiliary_close_pending")
	original.closeTried = true
	original.closeError = original.file.Close()
	if original.closeError != nil {
		return original.closeError
	} // uncertain original stays registered, no retry
	for i, f := range n.slots {
		if f == original {
			n.slots[i] = nil
			n.closed++
			return nil
		}
	}
	panic("resident service original FD lost")
}

func (n *nativeFiles) openAt(parent int, name string, flags int) (*retainedFD, error) {
	if err := n.capacity(1); err != nil {
		return nil, err
	}
	n.progress("original_service_open_pending")
	fd, err := unix.Openat(parent, name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return n.retain(fd, name)
}

func (n *nativeFiles) walkHome(home string) (*retainedFD, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" || len(home) > 4096 || strings.ContainsRune(home, 0) {
		return nil, ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(home, "/"), "/")
	if len(parts) > 64 {
		return nil, ErrInvalid
	}
	current, err := n.openAt(unix.AT_FDCWD, "/", unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return current, err
	}
	for _, part := range parts {
		next, err := n.openAt(int(current.file.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY)
		if err != nil {
			return next, err
		}
		if err = n.closeAuxiliary(current); err != nil {
			return next, err
		}
		current = next
	}
	return current, privateNative(current, true)
}

func (n *nativeFiles) openHome(home string) error {
	original, err := n.walkHome(home)
	n.home = original
	return err
}

func privateNative(original *retainedFD, directory bool) error {
	if original == nil || original.closeTried {
		return ErrInvalid
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(original.file.Fd()), &stat); err != nil {
		return err
	}
	want := uint32(unix.S_IFREG) | 0600
	if directory {
		want = uint32(unix.S_IFDIR) | 0700
	}
	if nativeID(&stat) != original.id || uint32(stat.Mode) != want || stat.Uid != uint32(os.Geteuid()) || !directory && stat.Nlink != 1 {
		return ErrInvalid
	}
	return nil
}

func (n *nativeFiles) openPrivateAt(directory *retainedFD, name string, flags int) (*retainedFD, error) {
	// Refuse FIFO/device/link before any read/write open; O_NONBLOCK also bounds
	// a cooperative rename race into FIFO before the retained post-open Fstat.
	n.progress("original_service_entry_prestat_pending")
	var before unix.Stat_t
	if err := unix.Fstatat(int(directory.file.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	if uint32(before.Mode) != uint32(unix.S_IFREG)|0600 || before.Uid != uint32(os.Geteuid()) || before.Nlink != 1 {
		return nil, ErrInvalid
	}
	original, err := n.openAt(int(directory.file.Fd()), name, flags|unix.O_NONBLOCK)
	if err != nil {
		return original, err
	}
	if err = privateNative(original, false); err != nil {
		return original, err
	}
	if original.id != nativeID(&before) {
		return original, ErrInvalid
	}
	return original, nil
}

func (n *nativeFiles) openExistingAuthorized(home string) error {
	if err := n.openHome(home); err != nil {
		return err
	}
	var err error
	n.lock, err = n.openPrivateAt(n.home, lockName, unix.O_RDWR)
	if err != nil {
		return err
	}
	if err = privateNative(n.lock, false); err != nil {
		return err
	}
	// Only the original parent acquires this lock. Child inherits the actual
	// open-file description and NEVER reacquires/reasserts/adopts its lock.
	n.progress("original_service_lock_pending")
	if err = unix.Flock(int(n.lock.file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	n.journal, err = n.openPrivateAt(n.home, journalName, unix.O_RDWR)
	if err != nil {
		return err
	}
	return privateNative(n.journal, false)
}

func (n *nativeFiles) validateCurrent(home string) (resultErr error) {
	for _, item := range []struct {
		f         *retainedFD
		directory bool
	}{{n.home, true}, {n.lock, false}, {n.journal, false}} {
		if err := privateNative(item.f, item.directory); err != nil {
			return err
		}
	}
	// The SAME original registry owns every independent current-path FD, including
	// partial walks or uncertain closes. No independent owner/hidden cleanup exists.
	current, err := n.walkHome(home)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, n.closeAuxiliary(current)) }()
	if current.id != n.home.id {
		return ErrInvalid
	}
	for _, entry := range []struct {
		name     string
		original *retainedFD
	}{{lockName, n.lock}, {journalName, n.journal}} {
		actual, err := n.openPrivateAt(current, entry.name, unix.O_RDONLY)
		if err != nil {
			return err
		}
		if err = privateNative(actual, false); err != nil {
			return err
		}
		if actual.id != entry.original.id {
			return ErrInvalid
		}
		if err = n.closeAuxiliary(actual); err != nil {
			return err
		}
	}
	return nil
}
