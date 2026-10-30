// Package nativeowner owns original resident authority descriptors. Only the
// trusted Server subtree may import its issuer; borrower aliases cannot retire.
package nativeowner

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"math"
	"os"
	"strings"
)

const (
	maxPathBytes = 4096
	maxPathParts = 64
	maxOwnedFDs  = 4
)

type ownedFD struct {
	file         *os.File
	slot         int
	identity     nativeIdentity
	closeTried   bool
	closeFailure error
}
type nativeIdentity struct {
	device, inode  uint64
	uid, gid, mode uint32
}
type nativeClosure struct {
	opened, closed         uint64
	unknown                int
	lockAcquired, complete bool
}

// Only the ONE registered original effect may read/mutate this state. Inspector
// and Seal NEVER read it; they use mutex-only published values in nativeOwner.
type nativeState struct {
	registry              *nativeOwner
	path                  string
	uid                   uint32
	directory, lock       *ownedFD
	slots                 [maxOwnedFDs]*ownedFD
	opened, closed        uint64
	lockAcquired, retired bool
	failure               error
	retirement            nativeClosure
	retireError           error
}

func (n *nativeState) progress(phase string) {
	o := n.registry
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		panic("native operation has no original registered effect")
	}
	o.active.phase = phase
	o.publishNativeLocked()
}
func (n *nativeState) permitOriginalOpen() error {
	o := n.registry
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.active == nil {
		return errNotOriginal
	}
	if o.sealed {
		return errSealed
	}
	return nil
}
func (o *nativeState) fail(err error) error {
	if err != nil && o.failure == nil {
		o.failure = err
	}
	return o.failure
}

func identity(s *unix.Stat_t) nativeIdentity {
	return nativeIdentity{device: uint64(s.Dev), inode: uint64(s.Ino), uid: s.Uid, gid: s.Gid, mode: uint32(s.Mode)}
}

func (o *nativeState) open(parent *ownedFD, name string, directory bool) (*ownedFD, error) {
	if err := o.permitOriginalOpen(); err != nil {
		return nil, err
	}
	o.progress("native_open")
	slot := -1
	for i, f := range o.slots {
		if f == nil {
			slot = i
			break
		}
	}
	if slot < 0 || o.opened == math.MaxUint64 {
		return nil, fmt.Errorf("bounded original descriptor capacity exhausted")
	}
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	} else {
		flags = flags&^unix.O_RDONLY | unix.O_RDWR
	}
	var fd int
	var err error
	if parent == nil {
		fd, err = unix.Open(name, flags, 0)
	} else {
		fd, err = unix.Openat(int(parent.file.Fd()), name, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	f := &ownedFD{file: os.NewFile(uintptr(fd), name), slot: slot}
	o.slots[slot] = f // retain immediately, before any failing validation
	o.opened++
	o.progress("native_open_registered")
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	f.identity = identity(&stat)
	if f.identity.inode == 0 || uint64(stat.Nlink) == 0 {
		return nil, fmt.Errorf("native identity/link unavailable")
	}
	return f, nil
}

func (o *nativeState) close(f *ownedFD) error {
	if f.closeTried {
		return f.closeFailure // never retry an ambiguous native close/reused number
	}
	f.closeTried = true
	o.progress("native_close_pending")
	if err := f.file.Close(); err != nil {
		f.closeFailure = fmt.Errorf("original native close uncertain: %w", err)
		return f.closeFailure // preserve slot, exact file and uncertainty
	}
	o.closed++
	o.slots[f.slot] = nil
	o.progress("native_close_returned")
	return nil
}

func (o *nativeState) openDirectory() (*ownedFD, error) {
	current, err := o.open(nil, "/", true)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(o.path, "/"), "/") {
		var before unix.Stat_t
		if err := unix.Fstatat(int(current.file.Fd()), part, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, err
		}
		if uint32(before.Mode)&unix.S_IFMT != unix.S_IFDIR {
			return nil, fmt.Errorf("directory path must contain only real directories")
		}
		next, err := o.open(current, part, true)
		if err != nil {
			return nil, err
		}
		if next.identity != identity(&before) {
			return nil, fmt.Errorf("directory changed during original open")
		}
		if err := o.close(current); err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

func (o *nativeState) checkPrivate(f *ownedFD, directory bool) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(f.file.Fd()), &stat); err != nil {
		return err
	}
	wantType, wantMode := uint32(unix.S_IFREG), uint32(0600)
	if directory {
		wantType, wantMode = unix.S_IFDIR, 0700
	}
	if identity(&stat) != f.identity || stat.Uid != o.uid || uint32(stat.Mode)&unix.S_IFMT != wantType ||
		uint32(stat.Mode)&07777 != wantMode || uint64(stat.Nlink) == 0 || (!directory && uint64(stat.Nlink) != 1) {
		return fmt.Errorf("original private native type/mode/owner/link identity changed")
	}
	return nil
}

func (o *nativeState) openLock(directory *ownedFD) (*ownedFD, error) {
	var before unix.Stat_t
	if err := unix.Fstatat(int(directory.file.Fd()), "daemon.lock", &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	if before.Uid != o.uid || uint32(before.Mode)&unix.S_IFMT != unix.S_IFREG ||
		uint32(before.Mode)&07777 != 0600 || uint64(before.Nlink) != 1 {
		return nil, fmt.Errorf("existing daemon.lock must be private, regular, owned and singly linked")
	}
	f, err := o.open(directory, "daemon.lock", false)
	if err != nil {
		return nil, err
	}
	if f.identity != identity(&before) {
		return nil, fmt.Errorf("daemon.lock changed during original open")
	}
	if err := o.checkPrivate(f, false); err != nil {
		return nil, err
	}
	return f, nil
}

func (o *nativeState) validateLocked() (resultErr error) {
	if uint32(os.Geteuid()) != o.uid {
		return fmt.Errorf("original effective account changed")
	}
	if !o.lockAcquired {
		return fmt.Errorf("original exclusive lock was not acquired")
	}
	if err := o.checkPrivate(o.directory, true); err != nil {
		return err
	}
	if err := o.checkPrivate(o.lock, false); err != nil {
		return err
	}
	// Independent original opens establish path consistency, never lock/adopt it.
	// On any failed open, close every auxiliary we actually opened; unknown closes
	// stay registered and are handled only by retirement's original accounting.
	defer func() {
		for _, f := range o.slots {
			if f != nil && f != o.directory && f != o.lock {
				resultErr = errors.Join(resultErr, o.close(f))
			}
		}
	}()
	directory, err := o.openDirectory()
	if err != nil {
		return err
	}
	if directory.identity != o.directory.identity {
		return fmt.Errorf("directory pathname no longer names original authority")
	}
	lock, err := o.openLock(directory)
	if err != nil {
		return err
	}
	if lock.identity != o.lock.identity {
		return fmt.Errorf("lock pathname no longer names original authority")
	}
	return nil
}

func (o *nativeState) retireLocked() (nativeClosure, error) {
	if o.retired {
		return o.retirement, o.retireError
	}
	o.retired = true // withdraw before any close; no borrowers in this stage
	err := o.failure
	// Close the original lock first; no separate unlock/reacquisition exists.
	// A successful close is the native release. Error is uncertain, not credit.
	if o.lock != nil {
		err = errors.Join(err, o.close(o.lock))
	}
	for _, f := range o.slots {
		if f != nil && f != o.lock {
			err = errors.Join(err, o.close(f))
		}
	}
	unknown := 0
	for _, f := range o.slots {
		if f != nil {
			unknown++
		}
	}
	o.retirement = nativeClosure{opened: o.opened, closed: o.closed, unknown: unknown,
		lockAcquired: o.lockAcquired, complete: unknown == 0 && o.opened == o.closed}
	o.retireError = err
	return o.retirement, err
}

func lockOriginalNative(n *nativeState) error {
	if err := n.permitOriginalOpen(); err != nil {
		return err
	}
	err := unix.Flock(int(n.lock.file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		n.lockAcquired = true
	}
	n.progress("native_lock_returned")
	return err
}
