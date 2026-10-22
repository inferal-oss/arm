package sharecodec

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"github.com/yrashk/arm/internal/sharefs"
)

// Serve joins the maintained codec's workers before returning. abort must bypass
// its sendPacket mutex; no claim is made that a stuck native read is cancellable.
// The caller retains its lease until Serve returns and its own workers join.
func Serve(ctx context.Context, o *sharefs.Origin, c io.ReadWriteCloser, abort func()) error {
	if o == nil {
		return fmt.Errorf("verified origin required")
	}
	defer func() {
		o.BeginClose()
		// A cancelled request scope cannot turn an unfinished native/cache operation
		// into a join. The authority retains its lease while this waits.
		_ = o.WaitDrained(context.Background())
	}()
	b, err := NewBridge(ctx, c, abort)
	if err != nil {
		return err
	}
	defer b.CloseAndJoin()
	h := &handlers{ctx: ctx, origin: o}
	server := sftp.NewRequestServer(b, sftp.Handlers{FileGet: h, FilePut: h, FileCmd: h, FileList: h}, sftp.WithRSMaxTxPacket(MaxData))
	// The retained socket watcher, not RequestServer.Close, handles cancellation.
	err = server.Serve()
	o.Close()
	return err
}

type handlers struct {
	ctx     context.Context
	origin  *sharefs.Origin
	mu      sync.Mutex
	handles int
}

func (h *handlers) take() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.handles >= 64 {
		return os.ErrPermission
	}
	h.handles++
	return nil
}
func (h *handlers) drop() { h.mu.Lock(); h.handles--; h.mu.Unlock() }

// SFTP owns a virtual POSIX root. Its canonical aliases never name a host root.
func virtual(p string) (string, error) {
	if !strings.HasPrefix(p, "/") {
		return "", os.ErrInvalid
	}
	p = path.Clean(p)
	if p == "/" {
		return "", nil
	}
	return strings.TrimPrefix(p, "/"), nil
}
func (h *handlers) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	if r.Flags != 1 {
		return nil, os.ErrPermission
	}
	p, err := virtual(r.Filepath)
	if err != nil {
		return nil, err
	}
	if err = h.take(); err != nil {
		return nil, err
	}
	token, err := h.origin.OpenRead(h.ctx, p)
	if err != nil {
		h.drop()
		return nil, os.ErrPermission
	}
	return &fileReader{owner: h, token: token}, nil
}
func (*handlers) Filewrite(*sftp.Request) (io.WriterAt, error) { return nil, os.ErrPermission }
func (*handlers) Filecmd(*sftp.Request) error                  { return os.ErrPermission }
func (h *handlers) RealPath(p string) (string, error) {
	p = path.Clean("/" + p)
	local, err := virtual(p)
	if err != nil {
		return "", err
	}
	if _, err = h.origin.Stat(h.ctx, local); err != nil {
		return "", os.ErrPermission
	}
	return p, nil
}
func (h *handlers) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	p, err := virtual(r.Filepath)
	if err != nil {
		return nil, err
	}
	switch r.Method {
	case "List":
		i, err := h.origin.Stat(h.ctx, p)
		if err != nil || !i.Directory {
			return nil, os.ErrPermission
		}
		if err = h.take(); err != nil {
			return nil, err
		}
		return &directory{owner: h, path: p}, nil
	case "Stat", "Lstat":
		i, err := h.origin.Stat(h.ctx, p)
		if err != nil {
			return nil, os.ErrPermission
		}
		v, err := metadata(i)
		if err != nil {
			return nil, err
		}
		return singleInfo{v}, nil
	default:
		return nil, os.ErrPermission
	}
}

type fileReader struct {
	owner *handlers
	token string
	once  sync.Once
}

func (r *fileReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) > MaxData {
		return 0, ErrProtocol
	}
	data, err := r.owner.origin.Read(r.owner.ctx, r.token, off, len(p))
	if len(data) > len(p) {
		return 0, ErrProtocol
	}
	copy(p, data)
	return len(data), err
}
func (r *fileReader) Close() error {
	r.once.Do(func() { _ = r.owner.origin.CloseRead(r.token); r.owner.drop() })
	return nil
}

type directory struct {
	owner  *handlers
	path   string
	once   sync.Once
	mu     sync.Mutex
	closed bool
}

func (d *directory) ListAt(out []os.FileInfo, off int64) (int, error) {
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return 0, os.ErrClosed
	}
	if off < 0 || off > sharefs.MaxEntries || len(out) == 0 {
		return 0, os.ErrInvalid
	}
	items, _, err := d.owner.origin.List(d.owner.ctx, d.path, int(off), min(256, len(out)))
	if err != nil {
		return 0, os.ErrPermission
	}
	for i, v := range items {
		out[i], err = metadata(v)
		if err != nil {
			return 0, err
		}
	}
	d.mu.Lock()
	closed = d.closed
	d.mu.Unlock()
	if closed {
		return 0, os.ErrClosed
	}
	if err = d.owner.ctx.Err(); err != nil {
		return 0, err
	}
	if len(items) < min(256, len(out)) {
		return len(items), io.EOF
	}
	return len(items), nil
}
func (d *directory) Close() error {
	d.once.Do(func() { d.mu.Lock(); d.closed = true; d.mu.Unlock(); d.owner.drop() })
	return nil
}

type fileInfo struct{ sharefs.Info }

// SFTPv3 times have whole-second uint32 representation. Never wrap old/future
// origin dates into a plausible different date; nanoseconds are explicitly lost
// by this codec, while the common manifest retains the native metadata.
func metadata(i sharefs.Info) (os.FileInfo, error) {
	if i.ModifiedSeconds < 0 || i.ModifiedSeconds > math.MaxUint32 {
		return nil, fmt.Errorf("origin timestamp is outside SFTPv3 range")
	}
	return fileInfo{i}, nil
}

func (i fileInfo) Name() string {
	if i.Path == "" {
		return "/"
	}
	return path.Base(i.Path)
}
func (i fileInfo) Size() int64 { return i.Info.Size }
func (i fileInfo) Mode() os.FileMode {
	m := os.FileMode(i.Info.Mode)
	if i.Directory {
		m |= os.ModeDir
	}
	return m
}
func (i fileInfo) ModTime() time.Time { return time.Unix(i.ModifiedSeconds, i.ModifiedNanos) }
func (i fileInfo) IsDir() bool        { return i.Directory }
func (i fileInfo) Sys() any           { return nil }

type singleInfo []os.FileInfo

func (s singleInfo) ListAt(out []os.FileInfo, off int64) (int, error) {
	if off < 0 || off > int64(len(s)) {
		return 0, os.ErrInvalid
	}
	n := copy(out, s[off:])
	if n < len(out) {
		return n, io.EOF
	}
	return n, nil
}
