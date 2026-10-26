//go:build darwin

package syncfs

import "golang.org/x/sys/unix"

func renameNoReplace(fromFD int, from string, toFD int, to string) error {
	return unix.RenameatxNp(fromFD, from, toFD, to, unix.RENAME_EXCL)
}
func renameExchange(fromFD int, from string, toFD int, to string) error {
	return unix.RenameatxNp(fromFD, from, toFD, to, unix.RENAME_SWAP)
}
func statMtime(s unix.Stat_t) (int64, int64) { return s.Mtim.Sec, s.Mtim.Nsec }
func statCtime(s unix.Stat_t) (int64, int64) { return s.Ctim.Sec, s.Ctim.Nsec }
func checkCasefoldDir(fd int) error          { return nil }
