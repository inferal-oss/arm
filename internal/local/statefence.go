package local

import (
	"fmt"
	"io"
	"os"
	"syscall"
)

// ResetFenceName is the durable stop marker for a database reset. Presence
// always fences startup and new client intents, even if its contents claim a
// completed phase. Only a separately reviewed reset continuation may remove it.
const ResetFenceName = "database-reset-pending.json"
const ResetSeedPointerName = "database-reset-seed.json"
const ResetHistoryName = "database-maintenance"

// StateFenceError deliberately contains no receipt contents or credentials.
// A malformed or unsafe marker cannot be interpreted as permission to start.
type StateFenceError struct{ Detail string }

func (e *StateFenceError) Error() string {
	return "database_reset_incomplete: " + e.Detail + "; inspect the reset receipt and explicitly complete recovery before starting ARM or sending another request"
}

// CheckStateFence observes an existing private namespace without creating,
// chmodding, parsing SQLite, acquiring/creating locks, or repairing anything.
// Missing home/marker is allowed for a never-initialized installation.
func CheckStateFence(home string) error {
	before, err := os.Lstat(home)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !privateFenceDirectory(before) {
		return &StateFenceError{Detail: "state directory is not private, same-user and real"}
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return &StateFenceError{Detail: "state directory changed while checking reset marker"}
	}
	f, err := root.OpenFile(ResetFenceName, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		// Recheck the named home as well as the anchored descriptor. A moved
		// old directory must not authorize writes to its replacement.
		now, e := os.Lstat(home)
		if e != nil || !privateFenceDirectory(now) || !os.SameFile(opened, now) {
			return &StateFenceError{Detail: "state directory changed while checking reset marker"}
		}
		return checkResetHistoryPointer(root)
	}
	if err != nil {
		return &StateFenceError{Detail: "reset marker cannot be read safely"}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return &StateFenceError{Detail: "reset marker cannot be inspected"}
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 {
		return &StateFenceError{Detail: "reset marker is unsafe or exceeds its 64 KiB bound"}
	}
	return &StateFenceError{Detail: fmt.Sprintf("a retained %s requires recovery", ResetFenceName)}
}

// A lost authority pointer cannot make retained reset history look like a
// never-initialized installation. Observe it through the same anchored home,
// with a complete bounded directory read and no namespace/permission writes.
func checkResetHistoryPointer(root *os.Root) error {
	if _, err := root.Lstat(ResetSeedPointerName); err == nil {
		f, err := root.OpenFile(ResetSeedPointerName, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return &StateFenceError{Detail: "authority seed pointer cannot be safely observed"}
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return &StateFenceError{Detail: "authority seed pointer cannot be inspected"}
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
			return &StateFenceError{Detail: "authority seed pointer is unsafe or exceeds its bound"}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := root.OpenFile(ResetHistoryName, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return &StateFenceError{Detail: "reset history cannot be safely observed while its authority pointer is missing"}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !privateFenceDirectory(info) {
		return &StateFenceError{Detail: "retained reset history is not a private same-user real directory"}
	}
	names, err := f.Readdirnames(65)
	if err != nil && err != io.EOF {
		return &StateFenceError{Detail: "retained reset history cannot be completely enumerated"}
	}
	if len(names) > 0 {
		return &StateFenceError{Detail: "retained database-maintenance history has no authority seed pointer; preserve its receipts and restore the exact reviewed authority evidence, never initialize a replacement database"}
	}
	now, err := root.Lstat(ResetHistoryName)
	if err != nil || !privateFenceDirectory(now) || !os.SameFile(info, now) {
		return &StateFenceError{Detail: "reset history changed while checking authority pointer absence"}
	}
	return nil
}

func privateFenceDirectory(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && st.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0
}
