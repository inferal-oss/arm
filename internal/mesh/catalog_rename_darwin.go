package mesh

import "golang.org/x/sys/unix"

func renameCatalogExclusive(fd int, from, to string) error {
	return unix.RenameatxNp(fd, from, fd, to, unix.RENAME_EXCL)
}
