// Package syncfs provides native, held-root observations and publication for
// reviewed sync actions. It does not grant authority, journal an operation, or
// confirm a reconciliation baseline.
package syncfs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/inferal-oss/arm/internal/syncreconcile"
	"golang.org/x/sys/unix"
)

var ErrChanged = errors.New("sync filesystem binding or entry changed")

// Root holds every named ancestor from / through the selected directory.
// Symlinks in that chain are refused. Operations use held descriptors and
// recheck the entire named binding before and after native cuts.
type Root struct {
	path  string
	parts []string
	chain []*os.File
}

func OpenRoot(path string) (*Root, error) {
	if !filepath.IsAbs(path) || len(path) > syncreconcile.MaxPathBytes || !utf8.ValidString(path) || filepath.Clean(path) != path || path == string(os.PathSeparator) {
		return nil, fmt.Errorf("absolute non-filesystem root required")
	}
	parts := strings.Split(strings.TrimPrefix(path, string(os.PathSeparator)), string(os.PathSeparator))
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	r := &Root{path: path, parts: parts, chain: []*os.File{os.NewFile(uintptr(fd), "/")}}
	for _, part := range parts {
		if err = validComponent(part); err != nil {
			r.Close()
			return nil, err
		}
		fd, err = unix.Openat(int(r.chain[len(r.chain)-1].Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			r.Close()
			return nil, err
		}
		r.chain = append(r.chain, os.NewFile(uintptr(fd), part))
	}
	if err = r.Check(); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func (r *Root) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}
func (r *Root) FD() int { return int(r.chain[len(r.chain)-1].Fd()) }
func (r *Root) Close() error {
	if r == nil {
		return nil
	}
	var first error
	for i := len(r.chain) - 1; i >= 0; i-- {
		if e := r.chain[i].Close(); first == nil {
			first = e
		}
	}
	r.chain = nil
	return first
}

func sameObject(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Mode&unix.S_IFMT == b.Mode&unix.S_IFMT
}
func statFD(fd int) (unix.Stat_t, error) { var s unix.Stat_t; e := unix.Fstat(fd, &s); return s, e }
func statNamed(fd int, name string) (unix.Stat_t, error) {
	var s unix.Stat_t
	e := unix.Fstatat(fd, name, &s, unix.AT_SYMLINK_NOFOLLOW)
	return s, e
}
func (r *Root) Check() error {
	if r == nil || len(r.chain) != len(r.parts)+1 {
		return ErrChanged
	}
	for i, part := range r.parts {
		named, e := statNamed(int(r.chain[i].Fd()), part)
		if e != nil {
			return fmt.Errorf("root ancestor %q: %w", part, ErrChanged)
		}
		held, e := statFD(int(r.chain[i+1].Fd()))
		if e != nil || !sameObject(named, held) || named.Mode&unix.S_IFMT != unix.S_IFDIR {
			return fmt.Errorf("root ancestor %q: %w", part, ErrChanged)
		}
	}
	return nil
}
func validComponent(s string) error {
	if s == "" || s == "." || s == ".." || len(s) > syncreconcile.MaxComponentBytes || !utf8.ValidString(s) || strings.ContainsAny(s, "/\x00") {
		return fmt.Errorf("invalid relative path component")
	}
	return nil
}
func validateRelative(p string) error {
	if p == "" || len(p) > syncreconcile.MaxPathBytes || filepath.IsAbs(p) || filepath.Clean(p) != p {
		return fmt.Errorf("invalid relative path %q", p)
	}
	parts := strings.Split(p, "/")
	if len(parts) > syncreconcile.MaxDepth {
		return fmt.Errorf("relative path depth exceeded")
	}
	for _, part := range parts {
		if e := validComponent(part); e != nil {
			return e
		}
	}
	return nil
}

type parentHandle struct {
	fd    int
	held  []int
	root  *Root
	parts []string
}

func (h *parentHandle) Close() {
	for i := len(h.held) - 1; i >= 0; i-- {
		_ = unix.Close(h.held[i])
	}
}
func (h *parentHandle) Check() error {
	if e := h.root.Check(); e != nil {
		return e
	}
	parent := h.root.FD()
	for i, part := range h.parts {
		named, e := statNamed(parent, part)
		if e != nil {
			return ErrChanged
		}
		held, e := statFD(h.held[i])
		if e != nil || !sameObject(named, held) {
			return ErrChanged
		}
		parent = h.held[i]
	}
	return nil
}
func (r *Root) parent(path string) (*parentHandle, string, error) {
	if e := validateRelative(path); e != nil {
		return nil, "", e
	}
	if e := r.Check(); e != nil {
		return nil, "", e
	}
	parts := strings.Split(path, "/")
	h := &parentHandle{fd: r.FD(), root: r, parts: parts[:len(parts)-1]}
	for _, part := range h.parts {
		fd, e := unix.Openat(h.fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			h.Close()
			return nil, "", e
		}
		h.held = append(h.held, fd)
		h.fd = fd
	}
	if e := h.Check(); e != nil {
		h.Close()
		return nil, "", e
	}
	return h, parts[len(parts)-1], nil
}
