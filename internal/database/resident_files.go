package database

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const residentSQLFDLimit = 4

type residentSQLIdentity struct {
	device, inode uint64
	uid, mode     uint32
}
type residentSQLFD struct {
	file       *os.File
	identity   residentSQLIdentity
	slot       int
	closeTried bool
	closeError error
}

// Only the actual original database startup task touches these records. The
// root retains them on every error; no constructor cleanup drops an original.
type residentSQLFiles struct {
	owner             *ResidentOpen
	path              string
	uid               uint32
	slots             [residentSQLFDLimit]*residentSQLFD
	directory, ledger *residentSQLFD
	opened, closed    uint64
}

func residentSQLStat(s *unix.Stat_t) residentSQLIdentity {
	return residentSQLIdentity{uint64(s.Dev), uint64(s.Ino), s.Uid, uint32(s.Mode)}
}
func (n *residentSQLFiles) open(parent *residentSQLFD, name string, directory bool) (*residentSQLFD, error) {
	slot := -1
	for i, f := range n.slots {
		if f == nil {
			slot = i
			break
		}
	}
	if slot < 0 || n.opened == math.MaxUint64 {
		return nil, fmt.Errorf("original SQL file registry is full")
	}
	flags := unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC | unix.O_RDONLY
	if directory {
		flags |= unix.O_DIRECTORY
	} else {
		flags = flags&^unix.O_RDONLY | unix.O_RDWR
	}
	// A malformed prepared ledger must refuse before O_RDWR can affect a FIFO
	// or device. The actual post-open object still needs exact identity checks.
	var before unix.Stat_t
	if !directory {
		if parent == nil {
			return nil, fmt.Errorf("original ledger parent required")
		}
		if err := unix.Fstatat(int(parent.file.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, err
		}
		if uint32(before.Mode)&unix.S_IFMT != unix.S_IFREG || uint32(before.Mode)&07777 != 0600 || before.Uid != n.uid || uint64(before.Nlink) != 1 || before.Ino == 0 {
			return nil, fmt.Errorf("prepared ledger must be a private single-link regular file before open")
		}
	}
	n.owner.publishFileProgress("native_open_pending")
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
	f := &residentSQLFD{file: os.NewFile(uintptr(fd), name), slot: slot}
	n.slots[slot] = f
	n.opened++
	n.owner.publishFileProgress("native_open_registered")
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return f, err
	}
	f.identity = residentSQLStat(&st)
	if !directory && (f.identity != residentSQLStat(&before) || uint64(st.Nlink) != 1) {
		return f, fmt.Errorf("original prepared ledger changed during open")
	}
	if st.Ino == 0 || uint64(st.Nlink) == 0 {
		return f, fmt.Errorf("original SQL native identity unavailable")
	}
	return f, nil
}
func (n *residentSQLFiles) closeAuxiliary(f *residentSQLFD) error {
	if f.closeTried {
		return f.closeError
	}
	f.closeTried = true
	n.owner.publishFileProgress("native_auxiliary_close_pending")
	if err := f.file.Close(); err != nil {
		f.closeError = err
		return err
	}
	n.closed++
	n.slots[f.slot] = nil
	n.owner.publishFileProgress("native_auxiliary_close_returned")
	return nil
}
func (n *residentSQLFiles) openHome() (*residentSQLFD, error) {
	current, err := n.open(nil, "/", true)
	if err != nil {
		return current, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(n.path, "/"), "/") {
		var before unix.Stat_t
		if err = unix.Fstatat(int(current.file.Fd()), part, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return current, err
		}
		if uint32(before.Mode)&unix.S_IFMT != unix.S_IFDIR {
			return current, fmt.Errorf("original home path contains non-directory")
		}
		next, err := n.open(current, part, true)
		if err != nil {
			return next, err
		}
		if next.identity != residentSQLStat(&before) {
			return next, fmt.Errorf("original home path changed during open")
		}
		if err = n.closeAuxiliary(current); err != nil {
			return next, err
		}
		current = next
	}
	return current, nil
}
func (n *residentSQLFiles) private(f *residentSQLFD, directory bool) error {
	var s unix.Stat_t
	if err := unix.Fstat(int(f.file.Fd()), &s); err != nil {
		return err
	}
	kind, mode := uint32(unix.S_IFREG), uint32(0600)
	if directory {
		kind = unix.S_IFDIR
		mode = 0700
	}
	if residentSQLStat(&s) != f.identity || s.Uid != n.uid || uint32(s.Mode)&unix.S_IFMT != kind || uint32(s.Mode)&07777 != mode || uint64(s.Nlink) == 0 || (!directory && uint64(s.Nlink) != 1) {
		return fmt.Errorf("original SQL private file identity changed")
	}
	return nil
}
func (n *residentSQLFiles) openCanonicalFresh() error {
	home, err := n.openHome()
	if err != nil {
		return err
	}
	if err = n.private(home, true); err != nil {
		return err
	}
	n.directory, err = n.open(home, "state.db", true)
	if err != nil {
		return err
	}
	if err = n.private(n.directory, true); err != nil {
		return err
	}
	// The canonical directory is retained; the independently opened home is an
	// auxiliary, never the issuer's original home/lock and never reflocked.
	if err = n.closeAuxiliary(home); err != nil {
		return err
	}
	n.ledger, err = n.open(n.directory, "ledger.sqlite", false)
	if err != nil {
		return err
	}
	if err = n.private(n.ledger, false); err != nil {
		return err
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(n.ledger.file.Fd()), &st); err != nil {
		return err
	}
	if st.Size != 0 {
		return fmt.Errorf("explicit fresh resident ledger must be empty; no adoption/recovery")
	}
	for _, name := range []string{"ledger.sqlite-wal", "ledger.sqlite-shm", "ledger.sqlite-journal"} {
		var st unix.Stat_t
		err = unix.Fstatat(int(n.directory.file.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil {
			return fmt.Errorf("fresh resident ledger has an existing sidecar")
		}
		if !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	return nil
}
func (n *residentSQLFiles) validateCanonical() error {
	if n.directory == nil || n.ledger == nil {
		return fmt.Errorf("actual canonical SQL originals unavailable")
	}
	if err := n.private(n.directory, true); err != nil {
		return err
	}
	if err := n.private(n.ledger, false); err != nil {
		return err
	}
	home, err := n.openHome()
	if err != nil {
		return err
	}
	// Aux names are never adopted as originals and never acquire/reassert flock.
	directory, err := n.open(home, "state.db", true)
	if err != nil {
		return err
	}
	if err = n.closeAuxiliary(home); err != nil {
		return err
	}
	if directory.identity != n.directory.identity {
		return fmt.Errorf("canonical SQL directory name changed")
	}
	var ledger unix.Stat_t
	err = unix.Fstatat(int(directory.file.Fd()), "ledger.sqlite", &ledger, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil && residentSQLStat(&ledger) != n.ledger.identity {
		err = fmt.Errorf("canonical SQL ledger name changed")
	}
	closeErr := n.closeAuxiliary(directory)
	return errors.Join(err, closeErr)
}
func canonicalResidentSQLPath(home string) (string, error) {
	if len(home) > 4096 || !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" || strings.ContainsRune(home, 0) || len(strings.Split(strings.TrimPrefix(home, "/"), "/")) > 64 {
		return "", fmt.Errorf("bounded canonical original home required")
	}
	path := filepath.Join(home, "state.db", "ledger.sqlite")
	if len(path) > 4096 {
		return "", fmt.Errorf("bounded canonical ledger path required")
	}
	return path, nil
}
