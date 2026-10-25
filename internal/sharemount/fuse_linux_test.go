package sharemount

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/shareview"
	"github.com/yrashk/arm/internal/storage"
)

func verified(t *testing.T) (*shareview.Subtree, *atomic.Bool, []byte) {
	t.Helper()
	ctx := context.Background()
	p, err := storage.Open(filepath.Join(t.TempDir(), "managed"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	name := strings.Repeat("a", 32)
	identity, err := p.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	a := model.Artifact{Name: name, Path: p.Path(name), Identity: &identity, Phase: "owned"}
	if err = os.Mkdir(filepath.Join(a.Path, "selected"), 0700); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("verified origin payload\n"), 100000)
	if err = os.WriteFile(filepath.Join(a.Path, "selected", "payload"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.Path, "outside"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := sharefs.New(sharefs.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	b := sharefs.Binding{Authority: strings.Repeat("b", 32), Owner: uint32(os.Geteuid()), Storage: name, StorageRevision: 1, Share: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)}
	m, err := e.Build(ctx, p, a, b)
	if err != nil {
		t.Fatal(err)
	}
	deny := new(atomic.Bool)
	o, err := e.Open(ctx, p, a, m, sharefs.Scope{Source: strings.Repeat("e", 32), EnrollmentRevision: 1, Consumer: strings.Repeat("f", 32), Session: strings.Repeat("1", 32)}, func(context.Context, sharefs.Access) error {
		if deny.Load() {
			return sharefs.ErrDenied
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		o.BeginClose()
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := o.WaitDrained(ctx); err != nil {
			t.Error(err)
		}
	})
	v, err := shareview.New(ctx, o, "selected")
	if err != nil {
		t.Fatal(err)
	}
	return v, deny, data
}

func TestReadOnlyNodeAndRevoke(t *testing.T) {
	v, deny, data := verified(t)
	ctx := context.Background()
	tree, err := New(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	fs.NewNodeFS(tree.Root(), Options())
	var out fuse.EntryOut
	inode, code := tree.root.Lookup(ctx, "payload", &out)
	if code != 0 || out.Mode != syscall.S_IFREG|0400 {
		t.Fatal(code, out.Mode)
	}
	n := inode.Operations().(*node)
	for _, flags := range []uint32{syscall.O_WRONLY, syscall.O_RDWR, syscall.O_RDONLY | syscall.O_TRUNC} {
		if _, _, code = n.Open(ctx, flags); code != syscall.EROFS {
			t.Fatal("write open", code)
		}
	}
	h, flags, code := n.Open(ctx, syscall.O_RDONLY)
	if code != 0 || flags != fuse.FOPEN_DIRECT_IO {
		t.Fatal(code, flags)
	}
	f := h.(*file)
	r, code := f.Read(ctx, make([]byte, 4096), 0)
	if code != 0 {
		t.Fatal(code)
	}
	got, status := r.Bytes(nil)
	if status != fuse.OK || !bytes.Equal(got, data[:4096]) {
		t.Fatal(status)
	}
	r.Done()
	dh, _, code := tree.root.OpendirHandle(ctx, 0)
	if code != 0 {
		t.Fatal(code)
	}
	d := dh.(*directory)
	entry, code := d.Readdirent(ctx)
	if code != 0 || entry.Name != "payload" {
		t.Fatal(entry, code)
	}
	deny.Store(true)
	if r, code = f.Read(ctx, make([]byte, 4096), 0); r != nil || code != syscall.EACCES {
		t.Fatal("revoked read", r, code)
	}
	if _, code = d.Readdirent(ctx); code != syscall.EACCES {
		t.Fatal("revoked directory", code)
	}
	if code = f.Release(ctx); code != 0 {
		t.Fatal(code)
	}
	d.Releasedir(ctx, 0)
	tree.BeginClose()
	if err = tree.WaitDrained(ctx); err != nil {
		t.Fatal(err)
	}
	if count, err := tree.HandleStatus(); count != 0 || err != nil || v.Outstanding() != 0 {
		t.Fatal("handle leak", count, err, v.Outstanding())
	}
	if _, _, code = n.Open(ctx, syscall.O_RDONLY); code != syscall.ESTALE {
		t.Fatal("closed tree", code)
	}
}

type failedClose struct{ shareview.Backend }

func (f failedClose) CloseRead(string) error { return errors.New("unknown close outcome") }
func TestReleaseFailureRetainsObligation(t *testing.T) {
	v, _, _ := verified(t)
	ctx := context.Background()
	tree, err := New(ctx, failedClose{v})
	if err != nil {
		t.Fatal(err)
	}
	f := &file{tree: tree, token: "not-retired"}
	tree.reserve()
	if code := f.Release(ctx); code != syscall.EIO {
		t.Fatal(code)
	}
	if count, err := tree.HandleStatus(); count != 1 || err == nil {
		t.Fatal("unknown obligation discarded", count, err)
	}
}

type lateRead struct {
	shareview.Backend
	entered, complete chan struct{}
}

func (b lateRead) Read(context.Context, string, int64, int) ([]byte, error) {
	close(b.entered)
	<-b.complete
	return []byte("late"), nil
}
func TestCloseWaitsAndDiscardsLateBytes(t *testing.T) {
	v, _, _ := verified(t)
	ctx := context.Background()
	b := lateRead{Backend: v, entered: make(chan struct{}), complete: make(chan struct{})}
	tree, err := New(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	h, err := v.OpenRead(ctx, "payload")
	if err != nil {
		t.Fatal(err)
	}
	tree.reserve()
	f := &file{tree: tree, token: h}
	result := make(chan syscall.Errno, 1)
	go func() {
		r, code := f.Read(ctx, make([]byte, 4), 0)
		if r != nil {
			result <- syscall.EIO
			return
		}
		result <- code
	}()
	<-b.entered
	tree.BeginClose()
	deadline, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if err = tree.WaitDrained(deadline); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unjoined operation settled", err)
	}
	if count, _ := tree.HandleStatus(); count != 1 {
		t.Fatal("handle discharged before join", count)
	}
	close(b.complete)
	if code := <-result; code != syscall.EINTR {
		t.Fatal("late bytes after close", code)
	}
	if err = tree.WaitDrained(ctx); err != nil {
		t.Fatal(err)
	}
	if code := f.Release(ctx); code != 0 {
		t.Fatal(code)
	}
	if count, err := tree.HandleStatus(); count != 0 || err != nil {
		t.Fatal(count, err)
	}
}

// Opt-in native test: requires the already available non-root /dev/fuse and
// fusermount facility. It installs nothing, never uses allow_other/force/lazy,
// and tests a private real Origin rather than claiming public consumer support.
func TestNativeFUSEReadOnlyVerifiedOrigin(t *testing.T) {
	if os.Getenv("ARM_NATIVE_FUSE_TEST") != "1" {
		t.Skip("set ARM_NATIVE_FUSE_TEST=1 for the native capability gate")
	}
	if os.Geteuid() == 0 {
		t.Fatal("must exercise unprivileged mount")
	}
	v, deny, data := verified(t)
	ctx := context.Background()
	tree, err := New(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.MkdirTemp("", "arm-mount-private-")
	if err != nil {
		t.Fatal(err)
	}
	point := filepath.Join(base, "mount")
	if err = os.Mkdir(point, 0700); err != nil {
		t.Fatal(err)
	}
	server, err := fs.Mount(point, tree.Root(), Options())
	if err != nil {
		os.RemoveAll(base)
		t.Fatal(err)
	}
	mounted := true
	t.Cleanup(func() {
		if mounted {
			if err := server.Unmount(); err != nil {
				t.Errorf("ordinary unmount failed; retained %s: %v", base, err)
				return
			}
			server.Wait()
			mounted = false
		}
		tree.BeginClose()
		bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tree.WaitDrained(bounded); err != nil {
			t.Error(err)
			return
		}
		if count, err := tree.HandleStatus(); count != 0 || err != nil {
			t.Errorf("unsettled adapter handles: %d %v", count, err)
			return
		}
		if err := os.RemoveAll(base); err != nil {
			t.Error(err)
		}
	})
	entries, err := os.ReadDir(point)
	if err != nil || len(entries) != 1 || entries[0].Name() != "payload" {
		t.Fatal(entries, err)
	}
	if _, err = os.Stat(filepath.Join(point, "outside")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("outside path visible", err)
	}
	f, err := os.Open(filepath.Join(point, "payload"))
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	want := sha256.Sum256(data)
	if !bytes.Equal(h.Sum(nil), want[:]) {
		f.Close()
		t.Fatal("mounted bytes mismatch")
	}
	if _, err = os.OpenFile(filepath.Join(point, "payload"), os.O_WRONLY|os.O_TRUNC, 0600); err == nil {
		f.Close()
		t.Fatal("readonly mount allowed write")
	}
	if err = server.Unmount(); err == nil {
		f.Close()
		mounted = false
		t.Fatal("ordinary unmount unexpectedly removed an open-file mount")
	}
	if count, _ := tree.HandleStatus(); count != 1 {
		f.Close()
		t.Fatal("open mount obligation lost", count)
	}
	probe := make([]byte, 128)
	if n, err := f.ReadAt(probe, 0); err != nil || n != len(probe) || !bytes.Equal(probe, data[:len(probe)]) {
		f.Close()
		t.Fatal("refused unmount did not retain exact usable mount", n, err)
	}
	t.Log("ordinary unmount refused with open FD; exact mount and counted handle retained")
	deny.Store(true)
	if n, err := f.ReadAt(make([]byte, 128), 0); n != 0 || err == nil {
		f.Close()
		t.Fatalf("revoked cached bytes escaped: %d %v", n, err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = server.Unmount(); err != nil {
		t.Fatal("ordinary unmount", err)
	}
	server.Wait()
	mounted = false
	t.Logf("native FUSE uid=%d verified_bytes=%d sha256=%x readonly=true revoke_denied=true ordinary_unmount=true server_joined=true", os.Geteuid(), len(data), want)
}
