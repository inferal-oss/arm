// Package authoritylifetime contains an unbound native ownership primitive.
// All constructors and operations are private; no production caller can obtain
// one. Native closure here says nothing about future Manager/Server borrowers.
package authoritylifetime

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const (
	maxPathBytes = 4096
	maxPathParts = 64
	// Two originals plus at most a parent/child or directory/lock validation
	// pair. A failed close occupies its slot and stops further native opening.
	maxOwnedFDs = 4
)

var (
	errNotOriginal = errors.New("original unbound native lifetime required")
	errRetired     = errors.New("native lifetime is retired")
)

// Copying this small handle does not copy native ownership or a mutex. The
// original pointer in owner is the only accepted receiver, including retirement.
type lifetime struct{ owner *nativeOwner }

type nativeOwner struct {
	mu           sync.Mutex
	original     *lifetime
	path         string
	uid          uint32
	directory    *ownedFD
	lock         *ownedFD
	slots        [maxOwnedFDs]*ownedFD
	opened       uint64
	closed       uint64
	lockAcquired bool
	failure      error // first validation/native failure is sticky
	retired      bool
	retirement   nativeClosure
	retireError  error
}

type ownedFD struct {
	file         *os.File
	slot         int
	identity     nativeIdentity
	closeTried   bool
	closeFailure error
}

type nativeIdentity struct {
	device, inode uint64
	uid, gid      uint32
	mode          uint32
}

// This is only an original FD/lock closure report, not a join witness or a
// capability for future borrowers. Unknown closes retain their original objects.
type nativeClosure struct {
	opened, closed uint64
	unknown        int
	lockAcquired   bool
	complete       bool
}

type acquisitionFailure struct {
	cause   error
	closure nativeClosure
}

func (e *acquisitionFailure) Error() string {
	return "native lifetime acquisition refused: " + e.cause.Error()
}
func (e *acquisitionFailure) Unwrap() error { return e.cause }

// acquire opens an explicit EXISTING real private directory and EXISTING
// daemon.lock; it never creates, repairs, adopts, unlinks or resolves symlinks.
// A nonnil handle WITH an error retains uncertain original cleanup and must be
// kept by its caller. No positive construction/test bypass or FD wrapper exists.
func acquire(path string) (result *lifetime, resultErr error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" ||
		len(path) > maxPathBytes || strings.ContainsRune(path, 0) ||
		len(strings.Split(strings.TrimPrefix(path, "/"), "/")) > maxPathParts {
		return nil, fmt.Errorf("bounded canonical absolute existing directory required")
	}
	o := &nativeOwner{path: path, uid: uint32(os.Geteuid())}
	h := &lifetime{owner: o}
	o.original = h
	defer func() {
		if resultErr == nil {
			return
		}
		o.fail(resultErr)
		closure, cleanupErr := o.retireLocked()
		resultErr = &acquisitionFailure{cause: cleanupErr, closure: closure}
		if closure.complete {
			result = nil
		} else {
			result = h // never drop an uncertain original
		}
	}()
	var err error
	o.directory, err = o.openDirectory()
	if err != nil {
		return nil, err
	}
	if err = o.checkPrivate(o.directory, true); err != nil {
		return nil, err
	}
	o.lock, err = o.openLock(o.directory)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(o.lock.file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, fmt.Errorf("exclusive existing authority lock: %w", err)
	}
	o.lockAcquired = true
	if err = o.validateLocked(); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *lifetime) validate() error {
	if h == nil || h.owner == nil {
		return errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.original != h {
		return errNotOriginal
	}
	if o.retired {
		return errors.Join(errRetired, o.retireError)
	}
	if o.failure != nil {
		return o.failure
	}
	return o.fail(o.validateLocked())
}

func (h *lifetime) retire() (nativeClosure, error) {
	if h == nil || h.owner == nil {
		return nativeClosure{}, errNotOriginal
	}
	o := h.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.original != h {
		return nativeClosure{}, errNotOriginal
	}
	return o.retireLocked()
}

func (o *nativeOwner) fail(err error) error {
	if err != nil && o.failure == nil {
		o.failure = err
	}
	return o.failure
}

func identity(s *unix.Stat_t) nativeIdentity {
	return nativeIdentity{device: uint64(s.Dev), inode: uint64(s.Ino), uid: s.Uid, gid: s.Gid, mode: uint32(s.Mode)}
}

func (o *nativeOwner) open(parent *ownedFD, name string, directory bool) (*ownedFD, error) {
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

func (o *nativeOwner) close(f *ownedFD) error {
	if f.closeTried {
		return f.closeFailure // never retry an ambiguous native close/reused number
	}
	f.closeTried = true
	if err := f.file.Close(); err != nil {
		f.closeFailure = fmt.Errorf("original native close uncertain: %w", err)
		return f.closeFailure // preserve slot, exact file and uncertainty
	}
	o.closed++
	o.slots[f.slot] = nil
	return nil
}

func (o *nativeOwner) openDirectory() (*ownedFD, error) {
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

func (o *nativeOwner) checkPrivate(f *ownedFD, directory bool) error {
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

func (o *nativeOwner) openLock(directory *ownedFD) (*ownedFD, error) {
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

func (o *nativeOwner) validateLocked() (resultErr error) {
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

func (o *nativeOwner) retireLocked() (nativeClosure, error) {
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
