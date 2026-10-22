// Package database provides bounded maintenance of an existing ARM ledger.
// It never opens a manager or adopts managed artifacts.
package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const MaxDatabaseBytes int64 = 8 << 30
const MaxRecordBytes = 1 << 20
const MaxEvidenceRows = 1000000

// File records namespace identity and observations, not exclusive physical use.
type File struct {
	Name      string `json:"name"`
	Present   bool   `json:"present"`
	Device    uint64 `json:"device,omitempty"`
	Inode     uint64 `json:"inode,omitempty"`
	Owner     uint32 `json:"owner"`
	Mode      uint32 `json:"mode,omitempty"`
	Size      int64  `json:"size_bytes"`
	Allocated int64  `json:"allocated_bytes"`
	Modified  int64  `json:"modified_unix_ns,omitempty"`
	Changed   int64  `json:"changed_unix_ns,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type Directory struct {
	Path     string
	FD       *os.File
	identity File
}

func OpenDirectory(path string) (*Directory, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, fmt.Errorf("directory path must contain no symlinks: %w", e)
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), path)
	d := &Directory{Path: path, FD: f}
	identity, err := nativeFile(f, ".", true)
	if err != nil {
		f.Close()
		return nil, err
	}
	d.identity = identity
	return d, nil
}
func (d *Directory) Close() error { return d.FD.Close() }
func (d *Directory) Sync() error  { return d.FD.Sync() }
func (d *Directory) Validate() error {
	current, err := OpenDirectory(d.Path)
	if err != nil {
		return err
	}
	defer current.Close()
	if !sameIdentity(d.identity, current.identity) {
		return fmt.Errorf("private directory identity changed")
	}
	return nil
}
func sameIdentity(a, b File) bool {
	return a.Present == b.Present && a.Device == b.Device && a.Inode == b.Inode && a.Owner == b.Owner && a.Mode == b.Mode
}
func safeName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}
func (d *Directory) Open(name string, flags int) (*os.File, File, error) {
	return d.open(name, flags, false)
}
func (d *Directory) openSource(name string) (*os.File, File, error) {
	return d.open(name, unix.O_RDONLY, true)
}
func (d *Directory) open(name string, flags int, source bool) (*os.File, File, error) {
	if !safeName(name) {
		return nil, File{}, fmt.Errorf("one exact file name required")
	}
	fd, err := unix.Openat(int(d.FD.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, File{Name: name}, nil
		}
		return nil, File{}, err
	}
	f := os.NewFile(uintptr(fd), filepath.Join(d.Path, name))
	v, err := nativeFilePolicy(f, name, false, source)
	if err != nil {
		f.Close()
		return nil, File{}, err
	}
	return f, v, nil
}
func (d *Directory) Observe(name string, hash bool) (File, error) {
	f, v, err := d.Open(name, unix.O_RDONLY)
	if err != nil || f == nil {
		return v, err
	}
	defer f.Close()
	if hash {
		v.SHA256, err = hashFile(context.Background(), f)
	}
	return v, err
}
func hashFile(ctx context.Context, f *os.File) (string, error) {
	if _, err := f.Seek(0, 0); err != nil {
		return "", err
	}
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		total += int64(n)
		if total > MaxDatabaseBytes {
			return "", fmt.Errorf("database file exceeds 8 GiB maintenance bound")
		}
		if n > 0 {
			h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func nativeFile(f *os.File, name string, dir bool) (File, error) {
	return nativeFilePolicy(f, name, dir, false)
}
func nativeFilePolicy(f *os.File, name string, dir, source bool) (File, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return File{}, err
	}
	expected := uint32(unix.S_IFREG)
	if dir {
		expected = unix.S_IFDIR
	}
	badPermissions := st.Mode&0077 != 0
	if source {
		badPermissions = st.Mode&0022 != 0
	}
	if uint32(st.Mode)&unix.S_IFMT != expected || st.Uid != uint32(os.Geteuid()) || badPermissions || (!dir && st.Nlink != 1) {
		return File{}, fmt.Errorf("%s must be a private same-user %s without file links", name, map[bool]string{true: "directory", false: "regular file"}[dir])
	}
	return File{Name: name, Present: true, Device: uint64(st.Dev), Inode: st.Ino, Owner: st.Uid, Mode: uint32(st.Mode), Size: st.Size, Allocated: st.Blocks * 512, Modified: statModified(&st), Changed: statChanged(&st)}, nil
}
func SnapshotTime() time.Time { return time.Now().UTC() }
