package sharefs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/storage"
	"golang.org/x/sys/unix"
)

// Engine owns a global I/O gate shared by all its origin sessions. Cancellation
// never frees a slot while a native read is still using its bounded buffer.
type Engine struct {
	limits   Limits
	gate     chan struct{}
	mu       sync.Mutex
	sessions int
}

func New(l Limits) (*Engine, error) {
	if err := l.validate(); err != nil {
		return nil, err
	}
	return &Engine{limits: l, gate: make(chan struct{}, l.Reads)}, nil
}
func (e *Engine) enter(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case e.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			e.leave()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (e *Engine) leave() { <-e.gate }

type Origin struct {
	engine   *Engine
	provider *storage.Provider
	artifact model.Artifact
	binding  Binding
	scope    Scope
	guard    Guard
	entries  map[string]Entry
	children map[string][]string
	mu       sync.Mutex // Only session/handle bookkeeping; never held across I/O/guard.
	closed   bool
	handles  map[string]*Reader
}

func (e *Engine) Open(ctx context.Context, p *storage.Provider, a model.Artifact, m Manifest, s Scope, g Guard) (*Origin, error) {
	if err := e.enter(ctx); err != nil {
		return nil, err
	}
	defer e.leave()
	if g == nil {
		return nil, ErrDenied
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	if err := m.Validate(e.limits); err != nil {
		return nil, err
	}
	if a.Identity == nil || a.Name != m.Binding.Storage || *a.Identity != m.Entries[0].Version.Identity {
		return nil, ErrDenied
	}
	e.mu.Lock()
	if e.sessions >= e.limits.Sessions {
		e.mu.Unlock()
		return nil, ErrLimit
	}
	e.sessions++
	e.mu.Unlock()
	opened := false
	defer func() {
		if !opened {
			e.mu.Lock()
			e.sessions--
			e.mu.Unlock()
		}
	}()
	// Own the evidence; a caller mutating a decoded manifest cannot widen access.
	copyArtifact := a
	id := *a.Identity
	copyArtifact.Identity = &id
	o := &Origin{engine: e, provider: p, artifact: copyArtifact, binding: m.Binding, scope: s, guard: g, entries: map[string]Entry{}, children: map[string][]string{}, handles: map[string]*Reader{}}
	for _, v := range m.Entries {
		v.Chunks = append([]string{}, v.Chunks...)
		o.entries[v.Path] = v
		if v.Path != "" {
			o.children[parent(v.Path)] = append(o.children[parent(v.Path)], v.Path)
		}
	}
	if err := o.authorize(ctx, "session", ""); err != nil {
		return nil, err
	}
	f, err := o.resolve(ctx, "")
	if err != nil {
		return nil, err
	}
	f.Close()
	if err = o.finish(ctx, "session", ""); err != nil {
		return nil, err
	}
	opened = true
	return o, nil
}
func (o *Origin) authorize(ctx context.Context, operation, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	o.mu.Lock()
	closed := o.closed
	o.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if err := validPath(path); err != nil {
		return err
	}
	if err := o.guard(ctx, Access{o.binding, o.scope, operation, path}); err != nil {
		return fmt.Errorf("%w: %v", ErrDenied, err)
	}
	o.mu.Lock()
	closed = o.closed
	o.mu.Unlock()
	if closed {
		return ErrClosed
	}
	return ctx.Err()
}
func (o *Origin) finish(ctx context.Context, operation, path string) error {
	if err := o.authorize(ctx, operation, path); err != nil {
		return err
	}
	// Rewalk native identity after the operation, including the named root.
	f, err := o.resolve(ctx, path)
	if f != nil {
		f.Close()
	}
	if err != nil {
		return err
	}
	return o.authorize(ctx, operation, path)
}
func (o *Origin) resolve(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validPath(path); err != nil {
		return nil, err
	}
	if _, ok := o.entries[path]; !ok {
		return nil, os.ErrNotExist
	}
	f, err := o.provider.OpenArtifactReadOnly(ctx, o.artifact)
	if err != nil {
		return nil, err
	}
	check := func(f *os.File, p string) error {
		v, err := version(int(f.Fd()))
		if err != nil {
			return err
		}
		x, ok := o.entries[p]
		if !ok || v != x.Version {
			return ErrChanged
		}
		return nil
	}
	if err = check(f, ""); err != nil {
		f.Close()
		return nil, err
	}
	current := ""
	if path != "" {
		for _, component := range strings.Split(path, "/") {
			if err = ctx.Err(); err != nil {
				f.Close()
				return nil, err
			}
			if current != "" {
				current += "/"
			}
			current += component
			x, ok := o.entries[current]
			if !ok {
				f.Close()
				return nil, ErrChanged
			}
			flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
			if x.Directory {
				flags |= unix.O_DIRECTORY
			}
			fd, openErr := unix.Openat(int(f.Fd()), component, flags, 0)
			f.Close()
			if openErr != nil {
				return nil, openErr
			}
			f = os.NewFile(uintptr(fd), component)
			if err = check(f, current); err != nil {
				f.Close()
				return nil, err
			}
		}
	}
	return f, nil
}

func (o *Origin) Stat(ctx context.Context, path string) (Info, error) {
	if err := o.engine.enter(ctx); err != nil {
		return Info{}, err
	}
	defer o.engine.leave()
	if err := o.authorize(ctx, "stat", path); err != nil {
		return Info{}, err
	}
	x, ok := o.entries[path]
	if !ok {
		return Info{}, os.ErrNotExist
	}
	if err := o.finish(ctx, "stat", path); err != nil {
		return Info{}, err
	}
	return info(x), nil
}

// List returns a fixed manifest page, never live names from the backing tree.
// next is an integer cursor for this exact immutable generation, not a path.
func (o *Origin) List(ctx context.Context, path string, start, count int) (items []Info, next int, err error) {
	if start < 0 || count < 1 || count > 256 {
		return nil, 0, ErrLimit
	}
	if err = o.engine.enter(ctx); err != nil {
		return nil, 0, err
	}
	defer o.engine.leave()
	if err = o.authorize(ctx, "list", path); err != nil {
		return nil, 0, err
	}
	x, ok := o.entries[path]
	if !ok {
		return nil, 0, os.ErrNotExist
	}
	if !x.Directory {
		return nil, 0, fmt.Errorf("not a share directory")
	}
	children := o.children[path]
	if start > len(children) {
		return nil, 0, ErrLimit
	}
	end := min(len(children), start+count)
	items = make([]Info, 0, end-start)
	for _, p := range children[start:end] {
		items = append(items, info(o.entries[p]))
	}
	if err = o.finish(ctx, "list", path); err != nil {
		return nil, 0, err
	}
	return items, end, nil
}

func (o *Origin) OpenRead(ctx context.Context, path string) (string, error) {
	if err := o.engine.enter(ctx); err != nil {
		return "", err
	}
	defer o.engine.leave()
	if err := o.authorize(ctx, "open", path); err != nil {
		return "", err
	}
	x, ok := o.entries[path]
	if !ok {
		return "", os.ErrNotExist
	}
	if x.Directory {
		return "", fmt.Errorf("not a regular share file")
	}
	if err := o.finish(ctx, "open", path); err != nil {
		return "", err
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return "", ErrClosed
	}
	if len(o.handles) >= o.engine.limits.Handles {
		return "", ErrLimit
	}
	if _, ok := o.handles[token]; ok {
		return "", ErrLimit
	}
	o.handles[token] = &Reader{origin: o, path: path}
	return token, nil
}

func (o *Origin) CloseRead(token string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	r, ok := o.handles[token]
	if !ok {
		return ErrClosed
	}
	delete(o.handles, token)
	r.closed = true
	return nil
}
func (o *Origin) Close() {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	o.closed = true
	for k, r := range o.handles {
		r.closed = true
		delete(o.handles, k)
	}
	o.mu.Unlock()
	o.engine.mu.Lock()
	o.engine.sessions--
	o.engine.mu.Unlock()
}
