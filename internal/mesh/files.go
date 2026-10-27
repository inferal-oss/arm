package mesh

import (
	"encoding/json"
	"fmt"
	"getarm.org/internal/local"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func directory(path string) (*os.File, error) {
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	canonical := filepath.Join(parent, filepath.Base(path))
	if e != nil {
		return nil, e
	}
	canonical, e = filepath.Abs(canonical)
	if e != nil {
		return nil, e
	}
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	for _, part := range strings.Split(strings.TrimPrefix(canonical, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	file := os.NewFile(uintptr(fd), canonical)
	info, e := file.Stat()
	if e != nil {
		file.Close()
		return nil, e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		file.Close()
		return nil, fmt.Errorf("mesh directory must be user-owned and private")
	}
	return file, nil
}
func privateFile(path string) ([]byte, error) {
	dir, e := directory(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	fd, e := unix.Openat(int(dir.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return nil, e
	}
	return readPrivate(file, info)
}
func readPrivate(f *os.File, info os.FileInfo) ([]byte, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return nil, fmt.Errorf("mesh file must be single-link, regular, private and user-owned")
	}
	return io.ReadAll(io.LimitReader(f, 1<<20+1))
}
func writePrivate(dir *os.File, name string, data []byte, exclusive bool) error {
	flags := unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
	temporary := name
	if !exclusive {
		temporary = ".arm-write-" + local.ID()
	}
	fd, e := unix.Openat(int(dir.Fd()), temporary, flags, 0600)
	if e != nil {
		return e
	}
	file := os.NewFile(uintptr(fd), temporary)
	defer file.Close()
	if !exclusive {
		defer unix.Unlinkat(int(dir.Fd()), temporary, 0)
	}
	if _, e = file.Write(data); e == nil {
		e = file.Sync()
	}
	if e != nil {
		return e
	}
	if !exclusive {
		var st unix.Stat_t
		e = unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW)
		if e == nil && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&0077 != 0) {
			return fmt.Errorf("refuse replacing unsafe mesh file")
		}
		if e != nil && e != unix.ENOENT {
			return e
		}
		if e = unix.Renameat(int(dir.Fd()), temporary, int(dir.Fd()), name); e != nil {
			return e
		}
	}
	return dir.Sync()
}
func save(path string, value any) error {
	raw, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	if len(raw)+1 > 1<<20 {
		return fmt.Errorf("mesh journal/config exceeds 1MiB bound")
	}
	dir, e := directory(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return writePrivate(dir, filepath.Base(path), append(raw, '\n'), false)
}
