package local

import (
	"fmt"
	"os"
	"syscall"
)

// ResetFenceName is the durable stop marker for a database reset. Presence
// always fences startup and new client intents, even if its contents claim a
// completed phase. Only a separately reviewed reset continuation may remove it.
const ResetFenceName = "database-reset-pending.json"

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
		return nil
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

func privateFenceDirectory(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && st.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0
}
