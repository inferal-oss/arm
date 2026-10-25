// Package shareview adapts an already authorized, verified origin to a confined
// virtual subtree. It grants no publication, transport, lease or cleanup rights.
package shareview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/sharefs"
)

// Backend must perform live authorization and generation checks before and
// after operations and disclose no unverified bytes. An arbitrary SFTP server
// is not a verified Backend. The consumer authority owns its session and drain.
type Backend interface {
	Stat(context.Context, string) (sharefs.Info, error)
	List(context.Context, string, int, int) ([]sharefs.Info, int, error)
	OpenRead(context.Context, string) (string, error)
	Read(context.Context, string, int64, int) ([]byte, error)
	CloseRead(string) error
}

// Subtree binds opaque read handles to one approved nonempty path. Empty paths
// address its virtual root; no filesystem path cleaning or symlink resolution
// occurs here. This object does not confer authority on its Backend.
type Subtree struct {
	backend Backend
	prefix  string
	mu      sync.Mutex
	handles map[string]*readHandle
	opening int
}
type readHandle struct {
	token   string
	closing bool
}

func New(ctx context.Context, backend Backend, prefix string) (*Subtree, error) {
	if ctx == nil || backend == nil || prefix == "" || ValidPath(prefix) != nil {
		return nil, sharefs.ErrDenied
	}
	v := &Subtree{backend: backend, prefix: prefix, handles: make(map[string]*readHandle)}
	if _, err := v.Stat(ctx, ""); err != nil {
		return nil, err
	}
	return v, nil
}

// ValidPath deliberately matches the origin's canonical relative path grammar.
// It rejects rather than cleans aliases and traversal.
func ValidPath(p string) error {
	if len(p) > 4096 || !utf8.ValidString(p) {
		return sharefs.ErrDenied
	}
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	if len(parts) > sharefs.MaxDepth {
		return sharefs.ErrLimit
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return sharefs.ErrDenied
		}
		for _, r := range part {
			if r == '\\' || unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
				return sharefs.ErrDenied
			}
		}
	}
	return nil
}

func (v *Subtree) path(p string) (string, error) {
	if err := ValidPath(p); err != nil {
		return "", err
	}
	full := v.prefix
	if p != "" {
		full += "/" + p
	}
	if err := ValidPath(full); err != nil {
		return "", err
	}
	return full, nil
}

func validInfo(i sharefs.Info, exact string) bool {
	mode := uint32(0400)
	if i.Directory {
		mode = 0500
	}
	return i.Path == exact && i.Mode == mode && i.Size >= 0 && (!i.Directory || i.Size == 0) && i.ModifiedNanos >= 0 && i.ModifiedNanos < 1e9
}

func (v *Subtree) Stat(ctx context.Context, p string) (sharefs.Info, error) {
	full, err := v.path(p)
	if err != nil {
		return sharefs.Info{}, err
	}
	i, err := v.backend.Stat(ctx, full)
	if err != nil {
		return sharefs.Info{}, err
	}
	if !validInfo(i, full) {
		return sharefs.Info{}, sharefs.ErrDenied
	}
	i.Path = p
	return i, nil
}

func (v *Subtree) List(ctx context.Context, p string, offset, limit int) ([]sharefs.Info, int, error) {
	full, err := v.path(p)
	if err != nil {
		return nil, offset, err
	}
	if offset < 0 || offset > sharefs.MaxEntries || limit < 1 || limit > 256 {
		return nil, offset, sharefs.ErrLimit
	}
	items, next, err := v.backend.List(ctx, full, offset, limit)
	if err != nil {
		return nil, offset, err
	}
	if len(items) > limit || next != offset+len(items) || next > sharefs.MaxEntries {
		return nil, offset, sharefs.ErrDenied
	}
	previous := ""
	for n, i := range items {
		if !strings.HasPrefix(i.Path, full+"/") {
			return nil, offset, sharefs.ErrDenied
		}
		name := strings.TrimPrefix(i.Path, full+"/")
		if name == "" || strings.Contains(name, "/") || ValidPath(i.Path) != nil || !validInfo(i, i.Path) || (n > 0 && name <= previous) {
			return nil, offset, sharefs.ErrDenied
		}
		previous = name
	}
	out := make([]sharefs.Info, len(items))
	for n, i := range items {
		out[n] = i
		out[n].Path = strings.TrimPrefix(i.Path, v.prefix+"/")
	}
	return out, next, nil
}

func (v *Subtree) OpenRead(ctx context.Context, p string) (string, error) {
	full, err := v.path(p)
	if err != nil {
		return "", err
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", err
	}
	local := hex.EncodeToString(raw[:])
	v.mu.Lock()
	if len(v.handles)+v.opening >= 64 {
		v.mu.Unlock()
		return "", sharefs.ErrLimit
	}
	// Reserve the bound before an underlying operation can allocate a handle.
	v.opening++
	v.mu.Unlock()
	token, err := v.backend.OpenRead(ctx, full)
	v.mu.Lock()
	v.opening--
	if err == nil {
		v.handles[local] = &readHandle{token: token}
	}
	v.mu.Unlock()
	if err != nil {
		return "", err
	}
	return local, nil
}

func (v *Subtree) Read(ctx context.Context, local string, offset int64, length int) ([]byte, error) {
	if offset < 0 || length < 0 || length > sharefs.ChunkBytes || offset > math.MaxInt64-int64(length) {
		return nil, sharefs.ErrLimit
	}
	v.mu.Lock()
	h, ok := v.handles[local]
	var token string
	if ok && !h.closing {
		token = h.token
	}
	v.mu.Unlock()
	if token == "" {
		return nil, sharefs.ErrClosed
	}
	data, err := v.backend.Read(ctx, token, offset, length)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(data) > length || (len(data) < length && err == nil) {
		return nil, sharefs.ErrDenied
	}
	return data, err
}

// CloseRead retains the obligation on an error. No unknown remote outcome is
// silently treated as a completed close. No bookkeeping lock spans backend I/O.
func (v *Subtree) CloseRead(local string) error {
	v.mu.Lock()
	h, ok := v.handles[local]
	if !ok {
		v.mu.Unlock()
		return sharefs.ErrClosed
	}
	if h.closing {
		v.mu.Unlock()
		return sharefs.ErrLimit
	}
	h.closing = true
	v.mu.Unlock()
	err := v.backend.CloseRead(h.token)
	v.mu.Lock()
	if err == nil {
		delete(v.handles, local)
	} else {
		h.closing = false
	}
	v.mu.Unlock()
	return err
}

// Outstanding is bookkeeping only, not a session drain or accounting receipt.
func (v *Subtree) Outstanding() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.handles) + v.opening
}
