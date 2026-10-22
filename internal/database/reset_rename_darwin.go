//go:build darwin

package database

import "golang.org/x/sys/unix"

func renameResetExclusive(fromFD int, from string, toFD int, to string) error {
	return unix.RenameatxNp(fromFD, from, toFD, to, unix.RENAME_EXCL)
}
