package sharemount

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/shareview"
)

// Tree owns only the adapter's operations and handle bookkeeping. Its caller
// must retain the consumer lease until ordinary unmount, server join, remote
// session drain and durable settlement all succeed. A canceled wait proves none
// of those facts. The Backend must already be scoped to the authorized subtree.
type Tree struct {
	view        shareview.Backend
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	closed      bool
	active      int
	handles     int
	closeErrors []error
	drained     chan struct{}
	once        sync.Once
	gate        chan struct{}
	inodes      map[string]uint64
	root        *node
	reads       atomic.Uint64
	readBytes   atomic.Uint64
	maxRead     atomic.Uint64
}

// ReadStats describes actual kernel read requests, not remote transport packets
// or permission to bypass operation checks. It contains no paths or contents.
type ReadStats struct{ Requests, Bytes, MaximumRequest uint64 }

func (t *Tree) ReadStats() ReadStats {
	return ReadStats{t.reads.Load(), t.readBytes.Load(), t.maxRead.Load()}
}

type node struct {
	fs.Inode
	tree *Tree
	path string
}

func New(ctx context.Context, view shareview.Backend) (*Tree, error) {
	if view == nil || ctx == nil {
		return nil, sharefs.ErrDenied
	}
	i, err := view.Stat(ctx, "")
	if err != nil {
		return nil, err
	}
	if !i.Directory || i.Path != "" || i.Mode != 0500 {
		return nil, syscall.ENOTDIR
	}
	scope, cancel := context.WithCancel(ctx)
	t := &Tree{view: view, ctx: scope, cancel: cancel, gate: make(chan struct{}, 8), drained: make(chan struct{}), inodes: map[string]uint64{"": 1}}
	t.root = &node{tree: t}
	return t, nil
}

func (t *Tree) Root() fs.InodeEmbedder { return t.root }

// Options never enables allow_other, direct privileged mounting, writeback,
// passthrough or kernel data/directory caching. There is no automatic fallback.
// Read buffers are <=1 MiB, <=8 application operations run concurrently, and
// FUSE request buffers have an explicit 16 MiB aggregate admission ceiling.
func Options() *fs.Options {
	zero := time.Duration(0)
	return &fs.Options{EntryTimeout: &zero, AttrTimeout: &zero, NegativeTimeout: &zero,
		MountOptions: fuse.MountOptions{Name: "arm", FsName: "arm-verified-readonly", Options: []string{"ro", "nosuid", "nodev", "noexec", "default_permissions"},
			MaxWrite: sharefs.ChunkBytes, MaxReadAhead: 0, MaxBackground: 8, MaxInflightRequestBytes: 16 << 20, DisableReadDirPlus: true, DisableSplice: true}}
}

func (t *Tree) enter(ctx context.Context) (context.Context, func(), error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, nil, sharefs.ErrClosed
	}
	t.active++
	t.mu.Unlock()
	op, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.ctx, cancel)
	leave := func() {
		stop()
		cancel()
		t.mu.Lock()
		t.active--
		t.signal()
		t.mu.Unlock()
	}
	select {
	case t.gate <- struct{}{}:
		if err := op.Err(); err != nil {
			<-t.gate
			leave()
			return nil, nil, err
		}
		return op, func() { <-t.gate; leave() }, nil
	case <-op.Done():
		leave()
		return nil, nil, op.Err()
	}
}
func (t *Tree) signal() {
	if t.closed && t.active == 0 {
		t.once.Do(func() { close(t.drained) })
	}
}

// BeginClose denies new operations and cancels in-flight contexts. It neither
// unmounts nor closes the authority-owned Backend. Already copied bytes cannot
// be revoked; direct I/O avoids retaining a kernel page cache for later reads.
func (t *Tree) BeginClose() { t.mu.Lock(); t.closed = true; t.signal(); t.mu.Unlock(); t.cancel() }
func (t *Tree) WaitDrained(ctx context.Context) error {
	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()
	if !closed {
		return sharefs.ErrDenied
	}
	select {
	case <-t.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// HandleStatus must still be checked after the FUSE server joins. A failed file
// release remains a recorded obligation, even if the kernel ignores RELEASE's
// error. Zero handles is not, by itself, consumer settlement evidence.
func (t *Tree) HandleStatus() (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.handles, errors.Join(t.closeErrors...)
}
func (t *Tree) reserve() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.handles >= 64 {
		return false
	}
	t.handles++
	return true
}
func (t *Tree) released(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil {
		t.handles--
	} else if len(t.closeErrors) < 64 {
		t.closeErrors = append(t.closeErrors, err)
	}
}

func errno(err error) syscall.Errno {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, sharefs.ErrDenied), errors.Is(err, os.ErrPermission):
		return syscall.EACCES
	case errors.Is(err, sharefs.ErrChanged), errors.Is(err, sharefs.ErrClosed):
		return syscall.ESTALE
	case errors.Is(err, sharefs.ErrLimit):
		return syscall.EAGAIN
	case errors.Is(err, os.ErrNotExist):
		return syscall.ENOENT
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return syscall.EINTR
	default:
		return syscall.EIO
	}
}
func mode(i sharefs.Info) uint32 {
	if i.Directory {
		return syscall.S_IFDIR | 0500
	}
	return syscall.S_IFREG | 0400
}
func (t *Tree) inode(path string) (uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n, ok := t.inodes[path]; ok {
		return n, nil
	}
	if len(t.inodes) >= sharefs.MaxEntries {
		return 0, sharefs.ErrLimit
	}
	n := uint64(len(t.inodes) + 1)
	t.inodes[path] = n
	return n, nil
}

func attrs(i sharefs.Info, number uint64, out *fuse.Attr) {
	out.Ino = number
	out.Mode = mode(i)
	out.Size = uint64(i.Size)
	out.Owner = fuse.Owner{Uid: uint32(os.Geteuid()), Gid: uint32(os.Getegid())}
	out.Nlink = 1
	if i.Directory {
		out.Nlink = 2
	}
	out.Mtime = uint64(max(i.ModifiedSeconds, 0))
	out.Mtimensec = uint32(i.ModifiedNanos)
}

func (n *node) Getattr(ctx context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	op, done, err := n.tree.enter(ctx)
	if err != nil {
		return errno(err)
	}
	defer done()
	i, err := n.tree.view.Stat(op, n.path)
	if err != nil {
		return errno(err)
	}
	number, err := n.tree.inode(i.Path)
	if err != nil {
		return errno(err)
	}
	attrs(i, number, &out.Attr)
	return 0
}
func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if name == "" || strings.Contains(name, "/") || shareview.ValidPath(name) != nil {
		return nil, syscall.EACCES
	}
	p := name
	if n.path != "" {
		p = n.path + "/" + name
	}
	op, done, err := n.tree.enter(ctx)
	if err != nil {
		return nil, errno(err)
	}
	defer done()
	i, err := n.tree.view.Stat(op, p)
	if err != nil {
		return nil, errno(err)
	}
	number, err := n.tree.inode(p)
	if err != nil {
		return nil, errno(err)
	}
	attrs(i, number, &out.Attr)
	return n.NewInode(ctx, &node{tree: n.tree, path: p}, fs.StableAttr{Mode: mode(i) & syscall.S_IFMT, Ino: number}), 0
}
func (n *node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if flags&(syscall.O_ACCMODE|syscall.O_TRUNC|syscall.O_APPEND|syscall.O_CREAT) != syscall.O_RDONLY {
		return nil, 0, syscall.EROFS
	}
	op, done, err := n.tree.enter(ctx)
	if err != nil {
		return nil, 0, errno(err)
	}
	defer done()
	if !n.tree.reserve() {
		return nil, 0, syscall.EMFILE
	}
	h, err := n.tree.view.OpenRead(op, n.path)
	if err != nil {
		n.tree.released(nil)
		return nil, 0, errno(err)
	}
	return &file{tree: n.tree, path: n.path, token: h}, fuse.FOPEN_DIRECT_IO, 0
}

type file struct {
	tree        *Tree
	path, token string
	mu          sync.Mutex
	closed      bool
}

func (f *file) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	f.tree.reads.Add(1)
	for old := f.tree.maxRead.Load(); uint64(len(dest)) > old; old = f.tree.maxRead.Load() {
		if f.tree.maxRead.CompareAndSwap(old, uint64(len(dest))) {
			break
		}
	}
	op, done, err := f.tree.enter(ctx)
	if err != nil {
		return nil, errno(err)
	}
	defer done()
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return nil, syscall.ESTALE
	}
	data, err := f.tree.view.Read(op, f.token, off, len(dest))
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, errno(err)
	}
	if len(data) > len(dest) {
		return nil, syscall.EIO
	}
	if err := op.Err(); err != nil {
		return nil, errno(err)
	}
	copy(dest, data)
	f.tree.readBytes.Add(uint64(len(data)))
	return fuse.ReadResultData(dest[:len(data)]), 0
}
func (f *file) Release(context.Context) syscall.Errno {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return syscall.ESTALE
	}
	f.closed = true
	f.mu.Unlock()
	// Mark locally closed before I/O, but retain the counted obligation on
	// every failed/unknown close. No per-handle mutex spans remote cleanup.
	err := f.tree.view.CloseRead(f.token)
	f.tree.released(err)
	return errno(err)
}
func (n *node) OpendirHandle(ctx context.Context, _ uint32) (fs.FileHandle, uint32, syscall.Errno) {
	op, done, err := n.tree.enter(ctx)
	if err != nil {
		return nil, 0, errno(err)
	}
	defer done()
	i, err := n.tree.view.Stat(op, n.path)
	if err != nil {
		return nil, 0, errno(err)
	}
	if !i.Directory {
		return nil, 0, syscall.ENOTDIR
	}
	if !n.tree.reserve() {
		return nil, 0, syscall.EMFILE
	}
	return &directory{tree: n.tree, path: n.path}, 0, 0
}

type directory struct {
	tree   *Tree
	path   string
	cursor int
	closed bool
	mu     sync.Mutex
}

func (d *directory) Readdirent(ctx context.Context) (*fuse.DirEntry, syscall.Errno) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, syscall.ESTALE
	}
	op, done, err := d.tree.enter(ctx)
	if err != nil {
		return nil, errno(err)
	}
	defer done()
	items, next, err := d.tree.view.List(op, d.path, d.cursor, 1)
	if err != nil {
		return nil, errno(err)
	}
	if len(items) == 0 {
		return nil, 0
	}
	i := items[0]
	d.cursor = next
	name := i.Path
	if d.path != "" {
		name = strings.TrimPrefix(name, d.path+"/")
	}
	number, err := d.tree.inode(i.Path)
	if err != nil {
		return nil, errno(err)
	}
	return &fuse.DirEntry{Name: name, Mode: mode(i), Ino: number, Off: uint64(next)}, 0
}
func (d *directory) Seekdir(ctx context.Context, off uint64) syscall.Errno {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return syscall.ESTALE
	}
	if off > sharefs.MaxEntries {
		return syscall.EINVAL
	}
	op, done, err := d.tree.enter(ctx)
	if err != nil {
		return errno(err)
	}
	defer done()
	if _, err = d.tree.view.Stat(op, d.path); err != nil {
		return errno(err)
	}
	d.cursor = int(off)
	return 0
}
func (d *directory) Releasedir(context.Context, uint32) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.closed = true
		d.tree.released(nil)
	}
}
