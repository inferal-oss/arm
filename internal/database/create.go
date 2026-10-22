package database

import (
	"fmt"
	"os"
	"syscall"
)

// PrepareNewFile gives a newly created ledger private mode before SQLite can
// create WAL companions. Existing permissions are observed, never repaired.
func PrepareNewFile(home string) error {
	before, err := os.Lstat(home)
	if err != nil {
		return err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("state directory must be private and real")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return fmt.Errorf("state directory changed during database preparation")
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	_, err = nativeFile(directory, ".", true)
	if err != nil {
		return err
	}
	f, err := root.OpenFile("state.db", os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err == nil {
		if e := f.Sync(); e != nil {
			f.Close()
			return e
		}
		if e := f.Close(); e != nil {
			return e
		}
		return directory.Sync()
	}
	if !os.IsExist(err) {
		return err
	}
	f, err = root.OpenFile("state.db", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = nativeFilePolicy(f, "state.db", false, true)
	return err
}
