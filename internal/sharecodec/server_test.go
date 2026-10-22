package sharecodec

import (
	"bytes"
	"context"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/storage"
)

func originFixture(t *testing.T) (*sharefs.Origin, string, []byte) {
	t.Helper()
	p, err := storage.Open(filepath.Join(t.TempDir(), "managed"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	name := strings.Repeat("a", 32)
	id, err := p.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	a := model.Artifact{Name: name, Path: p.Path(name), Phase: "owned", Identity: &id}
	payload := bytes.Repeat([]byte("published origin data"), 100000)
	if err = os.WriteFile(filepath.Join(a.Path, "file"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(a.Path, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	e, err := sharefs.New(sharefs.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	binding := sharefs.Binding{Authority: strings.Repeat("b", 32), Owner: uint32(os.Geteuid()), Storage: name, StorageRevision: 1, Share: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)}
	m, err := e.Build(context.Background(), p, a, binding)
	if err != nil {
		t.Fatal(err)
	}
	o, err := e.Open(context.Background(), p, a, m, sharefs.Scope{Source: strings.Repeat("e", 32), EnrollmentRevision: 1, Consumer: strings.Repeat("f", 32), Session: strings.Repeat("1", 32)}, func(context.Context, sharefs.Access) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(o.Close)
	return o, a.Path, payload
}
func clientFixture(t *testing.T, o *sharefs.Origin) (*sftp.Client, <-chan error) {
	t.Helper()
	server, peer := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, o, server, func() { server.Close() }) }()
	c, err := sftp.NewClientPipe(peer, peer, sftp.MaxPacketUnchecked(MaxData), sftp.MaxConcurrentRequestsPerFile(Credits))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); peer.Close(); c.Close() })
	return c, done
}
func TestActualReadonlySFTPUsesVerifiedOrigin(t *testing.T) {
	o, root, want := originFixture(t)
	c, done := clientFixture(t, o)
	info, err := c.Stat("/file")
	if err != nil || info.Size() != int64(len(want)) || info.Mode().Perm() != 0400 {
		t.Fatal("stat", info, err)
	}
	names, err := c.ReadDir("/")
	if err != nil || len(names) != 2 {
		t.Fatalf("list %#v %v", names, err)
	}
	for _, p := range []string{"/file", "dir/../file", "../../file"} {
		f, err := c.Open(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(f)
		f.Close()
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("namespace alias/read %s len%d %v", p, len(got), err)
		}
	}
	if _, err := c.Open("../../outside-sentinel"); err == nil {
		t.Fatal("outside namespace opened")
	}
	if _, err := c.Create("/new-file"); err == nil {
		t.Fatal("write OPEN accepted")
	}
	if _, err = os.Stat(filepath.Join(root, "new-file")); !os.IsNotExist(err) {
		t.Fatal("codec wrote origin")
	}
	f, err := c.Open("/file")
	if err != nil {
		t.Fatal(err)
	}
	for _, off := range []int64{0, 5, MaxData - 3, sharefs.ChunkBytes - 17} {
		buf := make([]byte, MaxData)
		n, err := f.ReadAt(buf, off)
		if err != nil || n != MaxData || !bytes.Equal(buf, want[off:off+MaxData]) {
			t.Fatalf("exact-frame/random-unaligned read offset%d len%d: %v", off, n, err)
		}
	}
	buf := make([]byte, 61)
	n, err := f.ReadAt(buf, MaxData-15)
	f.Close()
	if err != nil || !bytes.Equal(buf[:n], want[MaxData-15:MaxData-15+61]) {
		t.Fatal("unaligned read", n, err)
	}
	c.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("normal codec close did not join")
	}
}

func TestDirectoryAndReadHandlesShareOneBound(t *testing.T) {
	o, _, _ := originFixture(t)
	h := &handlers{ctx: context.Background(), origin: o}
	var closers []io.Closer
	for i := 0; i < 64; i++ {
		if i%2 == 0 {
			r, err := h.Fileread(&sftp.Request{Method: "Get", Filepath: "/file", Flags: 1})
			if err != nil {
				t.Fatal(err)
			}
			closers = append(closers, r.(io.Closer))
		} else {
			r, err := h.Filelist(&sftp.Request{Method: "List", Filepath: "/dir"})
			if err != nil {
				t.Fatal(err)
			}
			closers = append(closers, r.(io.Closer))
		}
	}
	if _, err := h.Filelist(&sftp.Request{Method: "List", Filepath: "/dir"}); err == nil {
		t.Fatal("directory handles bypassed64 bound")
	}
	if _, err := h.Fileread(&sftp.Request{Method: "Get", Filepath: "/file", Flags: 1}); err == nil {
		t.Fatal("read handles bypassed shared bound")
	}
	for _, c := range closers {
		c.Close()
		c.Close()
	}
	if h.handles != 0 {
		t.Fatal("handle accounting leak")
	}
}

func TestMetadataTimestampMappingDoesNotWrap(t *testing.T) {
	for _, sec := range []int64{-1, 0, math.MaxUint32, math.MaxUint32 + 1} {
		v, err := metadata(sharefs.Info{Path: "file", ModifiedSeconds: sec})
		if sec < 0 || sec > math.MaxUint32 {
			if err == nil {
				t.Fatal("wrapped unsupported timestamp", sec)
			}
		} else if err != nil || v.ModTime().Unix() != sec {
			t.Fatal("timestamp boundary", sec, err)
		}
	}
}

type stalledWriteConn struct {
	net.Conn
	mu      sync.Mutex
	started chan struct{}
	once    sync.Once
}

func (c *stalledWriteConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(p) > 1024 {
		c.once.Do(func() { close(c.started) })
	}
	return c.Conn.Write(p)
}
func (c *stalledWriteConn) Close() error { c.mu.Lock(); defer c.mu.Unlock(); return c.Conn.Close() }

type fullReader struct{}

func (fullReader) ReadAt(p []byte, _ int64) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestActualSocketAbortPrecedesLockedCodecClose(t *testing.T) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	peer, err := net.Dial("tcp4", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err = peer.(*net.TCPConn).SetReadBuffer(1024); err != nil {
		t.Fatal(err)
	}
	raw, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if err = raw.(*net.TCPConn).SetWriteBuffer(1024); err != nil {
		t.Fatal(err)
	}
	conn := &stalledWriteConn{Conn: raw, started: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := NewBridge(ctx, conn, func() { raw.Close() })
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	rs := sftp.NewRequestServer(b, sftp.Handlers{FileGet: readerHandler{fullReader{}}}, sftp.WithRSMaxTxPacket(MaxData))
	done := make(chan error, 1)
	go func() { done <- rs.Serve() }()
	if err = writeFull(peer, frame(1, 3)); err != nil {
		t.Fatal(err)
	}
	rawPacket(t, peer)
	open := append(str("/file"), []byte{0, 0, 0, 1, 0, 0, 0, 0}...)
	if err = writeFull(peer, frame(3, 1, open)); err != nil {
		t.Fatal(err)
	}
	rawPacket(t, peer)
	if err = writeFull(peer, readRequest(2, 0, MaxData)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("large socket write never entered")
	}
	// c.Close shares a mutex with Write, mirroring upstream's deadlock hazard.
	// Abort MUST close the held raw socket first, independently of that mutex.
	closed := make(chan struct{})
	go func() { b.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close blocked behind socket writer")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("native codec workers did not join")
	}
}
