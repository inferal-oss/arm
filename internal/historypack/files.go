package historypack

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func identity(f *os.File) (Identity, error) {
	var st unix.Stat_t
	if e := unix.Fstat(int(f.Fd()), &st); e != nil {
		return Identity{}, e
	}
	return Identity{uint64(st.Dev), st.Ino, st.Uid, uint32(st.Mode)}, nil
}
func validDir(v Identity) bool {
	return v.Inode != 0 && v.Owner == uint32(os.Geteuid()) && v.Mode == unix.S_IFDIR|0700
}
func validFile(v Identity) bool {
	return v.Inode != 0 && v.Owner == uint32(os.Geteuid()) && v.Mode == unix.S_IFREG|0600
}
func checkFile(f *os.File) (Identity, error) {
	var st unix.Stat_t
	if e := unix.Fstat(int(f.Fd()), &st); e != nil {
		return Identity{}, e
	}
	v := Identity{uint64(st.Dev), st.Ino, st.Uid, uint32(st.Mode)}
	if !validFile(v) || st.Nlink != 1 {
		return v, fmt.Errorf("private owned0600 single-link regular file required")
	}
	return v, nil
}

// Every path component is opened with O_NOFOLLOW, including system aliases.
// Callers must pass an absolute clean physical path (e.g. /private/tmp on macOS).
func openRoot(path string) (*os.File, Identity, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || strings.IndexByte(path, 0) >= 0 || !validText(path, 4096, false) {
		return nil, Identity{}, fmt.Errorf("absolute clean physical root path required")
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, Identity{}, e
	}
	f := os.NewFile(uintptr(fd), "/")
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, e := unix.Openat(int(f.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		f.Close()
		if e != nil {
			return nil, Identity{}, e
		}
		f = os.NewFile(uintptr(next), part)
	}
	v, e := identity(f)
	if e != nil || !validDir(v) {
		f.Close()
		return nil, v, fmt.Errorf("private owned0700 root required: %v", e)
	}
	return f, v, nil
}
func sameRoot(path string, want Identity) error {
	f, v, e := openRoot(path)
	if e != nil {
		return e
	}
	defer f.Close()
	if v != want {
		return fmt.Errorf("canonical root identity changed")
	}
	return nil
}
func openDir(parent *os.File, name string, want *Identity) (*os.File, Identity, error) {
	fd, e := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, Identity{}, e
	}
	f := os.NewFile(uintptr(fd), name)
	v, e := identity(f)
	if e != nil || !validDir(v) || want != nil && v != *want {
		f.Close()
		return nil, v, fmt.Errorf("born private directory identity differs")
	}
	return f, v, nil
}
func openFile(parent *os.File, name string, create bool) (*os.File, Identity, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if create {
		flags = unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	}
	fd, e := unix.Openat(int(parent.Fd()), name, flags, 0600)
	if e != nil {
		return nil, Identity{}, e
	}
	f := os.NewFile(uintptr(fd), name)
	v, e := checkFile(f)
	if e != nil {
		f.Close()
		return nil, v, e
	}
	return f, v, nil
}
func boundedRead(ctx context.Context, f *os.File, limit uint64) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if info.Size() < 0 || uint64(info.Size()) > limit {
		return nil, fmt.Errorf("file exceeds bounded size")
	}
	raw, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil {
		return nil, e
	}
	if uint64(len(raw)) > limit {
		return nil, fmt.Errorf("file grew beyond bound")
	}
	return raw, ctx.Err()
}
func checkName(parent *os.File, name string, want Identity) error {
	f, v, e := openFile(parent, name, false)
	if e != nil {
		return e
	}
	defer f.Close()
	if v != want {
		return fmt.Errorf("canonical file identity changed")
	}
	return nil
}
func directoryNames(dir *os.File) error {
	fd, e := unix.Openat(int(dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), ".")
	defer f.Close()
	names, e := f.Readdirnames(3)
	if e != nil && e != io.EOF {
		return e
	}
	if len(names) != 2 || !(names[0] == "manifest.json" && names[1] == "records.bin" || names[1] == "manifest.json" && names[0] == "records.bin") {
		return fmt.Errorf("pack must contain exactly manifest.json and records.bin")
	}
	return nil
}
func writeAll(ctx context.Context, f *os.File, raw []byte) error {
	for len(raw) > 0 {
		if e := ctx.Err(); e != nil {
			return e
		}
		n, e := f.Write(raw)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		raw = raw[n:]
	}
	return nil
}
