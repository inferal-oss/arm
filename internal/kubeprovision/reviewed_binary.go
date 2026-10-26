package kubeprovision

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// OpenReviewedBinary reopens a selected local artifact without allowing a
// reviewed regular file to be replaced by a blocking FIFO or a final symlink.
// The returned descriptor stays held while ShipBinary spools and rehashes it.
func OpenReviewedBinary(path string, size int64, digest string) (*os.File, error) {
	if size < 1 || size > MaxBinaryBytes || !digestPattern.MatchString(digest) {
		return nil, errors.New("invalid reviewed Kubernetes binary")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	failed := func(err error) (*os.File, error) { file.Close(); return nil, err }
	before, err := file.Stat()
	if err != nil {
		return failed(err)
	}
	stat, ok := before.Sys().(*syscall.Stat_t)
	if !ok || !before.Mode().IsRegular() || before.Size() != size || before.Mode().Perm()&0111 == 0 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return failed(errors.New("reviewed Kubernetes binary source changed"))
	}
	h := sha256.New()
	if n, err := io.Copy(h, io.LimitReader(file, size+1)); err != nil || n != size || hex.EncodeToString(h.Sum(nil)) != digest {
		return failed(errors.New("reviewed Kubernetes binary bytes changed"))
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
		return failed(errors.New("reviewed Kubernetes binary changed while opening"))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return failed(err)
	}
	return file, nil
}
