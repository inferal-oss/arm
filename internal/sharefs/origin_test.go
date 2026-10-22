package sharefs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/storage"
	"golang.org/x/sys/unix"
)

func fixture(t testing.TB) (*Engine, *storage.Provider, model.Artifact, Binding) {
	t.Helper()
	// sockaddr_un has a short native path bound; do not skip the socket gate.
	base, err := os.MkdirTemp("/tmp", "arm-sharefs-")
	if err != nil {
		t.Fatal(err)
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	p, err := storage.Open(filepath.Join(base, "managed"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	name := strings.Repeat("a", 32)
	id, err := p.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	a := model.Artifact{Name: name, Path: p.Path(name), Identity: &id, Phase: "owned"}
	b := Binding{strings.Repeat("b", 32), uint32(os.Geteuid()), name, 1, strings.Repeat("c", 32), strings.Repeat("d", 32)}
	e, err := New(DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return e, p, a, b
}
func scope() Scope {
	return Scope{strings.Repeat("e", 32), 1, strings.Repeat("f", 32), strings.Repeat("1", 32)}
}
func permit(context.Context, Access) error { return nil }
func write(t testing.TB, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func built(t testing.TB, e *Engine, p *storage.Provider, a model.Artifact, b Binding) Manifest {
	t.Helper()
	m, err := e.Build(context.Background(), p, a, b)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func opened(t testing.TB, e *Engine, p *storage.Provider, a model.Artifact, m Manifest, g Guard) *Origin {
	t.Helper()
	o, err := e.Open(context.Background(), p, a, m, scope(), g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(o.Close)
	return o
}
func handle(t testing.TB, o *Origin, path string) string {
	t.Helper()
	h, err := o.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestImmutableReadPagesOffsetsAndMetadata(t *testing.T) {
	e, p, a, b := fixture(t)
	if err := os.Mkdir(filepath.Join(a.Path, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("published bytes"), ChunkBytes/7)
	write(t, filepath.Join(a.Path, "dir", "payload"), data)
	write(t, filepath.Join(a.Path, "zero"), nil)
	m := built(t, e, p, a, b)
	o := opened(t, e, p, a, m, permit)
	items, next, err := o.List(context.Background(), "", 0, 1)
	if err != nil || next != 1 || len(items) != 1 || items[0].Path != "dir" || items[0].Mode != 0500 {
		t.Fatalf("first page: %#v %d %v", items, next, err)
	}
	items, next, err = o.List(context.Background(), "", next, 10)
	if err != nil || next != 2 || len(items) != 1 || items[0].Path != "zero" {
		t.Fatalf("second page: %#v %d %v", items, next, err)
	}
	i, err := o.Stat(context.Background(), "dir/payload")
	if err != nil || i.Size != int64(len(data)) || i.Mode != 0400 {
		t.Fatalf("metadata: %#v %v", i, err)
	}
	h := handle(t, o, "dir/payload")
	for _, off := range []int64{0, ChunkBytes - 5, ChunkBytes + 10, int64(len(data)) - 7, int64(len(data)), int64(len(data)) + 100} {
		got, err := o.Read(context.Background(), h, off, 12345)
		end := min(int64(len(data)), off+12345)
		var want []byte
		if off < int64(len(data)) {
			want = data[off:end]
		}
		if !bytes.Equal(got, want) || len(want) < 12345 && !errors.Is(err, io.EOF) || len(want) == 12345 && err != nil {
			t.Fatalf("read offset%d len%d error%v", off, len(got), err)
		}
	}
	if got, err := o.Read(context.Background(), handle(t, o, "zero"), 0, 1); len(got) != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("empty: %v %v", got, err)
	}
	if got, err := o.Read(context.Background(), h, math.MaxInt64, 1); got != nil || !errors.Is(err, ErrLimit) {
		t.Fatalf("overflow: %v %v", got, err)
	}
	if got, err := o.Read(context.Background(), h, 0, ChunkBytes+1); got != nil || !errors.Is(err, ErrLimit) {
		t.Fatal("oversized read accepted")
	}
	if err := o.CloseRead(h); err != nil {
		t.Fatal(err)
	}
	if got, err := o.Read(context.Background(), h, 0, 1); got != nil || !errors.Is(err, ErrClosed) {
		t.Fatal("closed handle read")
	}
}

func TestUnpublishedBytesNeverEscapeMutation(t *testing.T) {
	for _, where := range []string{"before-read", "during-read-guard", "after-read-guard"} {
		t.Run(where, func(t *testing.T) {
			e, p, a, b := fixture(t)
			path := filepath.Join(a.Path, "payload")
			write(t, path, []byte("approved"))
			m := built(t, e, p, a, b)
			var reads int
			g := func(_ context.Context, x Access) error {
				if x.Operation == "read" {
					reads++
					if where == "during-read-guard" && reads == 2 || where == "after-read-guard" && reads == 3 {
						write(t, path, []byte("SECRET!!"))
					}
				}
				return nil
			}
			o := opened(t, e, p, a, m, g)
			h := handle(t, o, "payload")
			if where == "before-read" {
				write(t, path, []byte("SECRET!!"))
			}
			got, err := o.Read(context.Background(), h, 0, 8)
			if err == nil || len(got) != 0 {
				t.Fatalf("unpublished bytes returned: %q %v", got, err)
			}
		})
	}
}

func TestChunkDigestRequiredEvenWhenMetadataMatches(t *testing.T) {
	e, p, a, b := fixture(t)
	path := filepath.Join(a.Path, "payload")
	write(t, path, []byte("approved"))
	m := built(t, e, p, a, b)
	write(t, path, []byte("SECRET!!"))
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := version(int(f.Fd()))
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Independently check chunk verification by giving the metadata gate the
	// changed version, retaining the original committed content digest.
	m.Entries[1].Version = v
	m.SHA256, _, _ = manifestHash(m)
	o := opened(t, e, p, a, m, permit)
	got, err := o.Read(context.Background(), handle(t, o, "payload"), 0, 8)
	if got != nil || !errors.Is(err, ErrChanged) {
		t.Fatalf("digest bypass: %q %v", got, err)
	}
}

func TestNativeLinksSpecialFilesAndBoundsRefused(t *testing.T) {
	for _, kind := range []string{"outside-symlink", "inside-symlink", "outside-hardlink", "inside-hardlink", "fifo", "socket", "special-mode", "group-write"} {
		t.Run(kind, func(t *testing.T) {
			e, p, a, b := fixture(t)
			outside := filepath.Join(t.TempDir(), "secret")
			write(t, outside, []byte("secret"))
			write(t, filepath.Join(a.Path, "file"), []byte("valid"))
			path := filepath.Join(a.Path, "hostile")
			var err error
			switch kind {
			case "outside-symlink":
				err = os.Symlink(outside, path)
			case "inside-symlink":
				err = os.Symlink("file", path)
			case "outside-hardlink":
				err = os.Link(outside, path)
			case "inside-hardlink":
				err = os.Link(filepath.Join(a.Path, "file"), path)
			case "fifo":
				err = unix.Mkfifo(path, 0600)
			case "socket":
				fd, e := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
				if e != nil {
					t.Fatal(e)
				}
				defer unix.Close(fd)
				err = unix.Bind(fd, &unix.SockaddrUnix{Name: path})
			case "special-mode":
				err = unix.Chmod(filepath.Join(a.Path, "file"), 04600)
			case "group-write":
				err = os.Chmod(filepath.Join(a.Path, "file"), 0660)
			}
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if _, err = e.Build(context.Background(), p, a, b); err == nil {
				t.Fatal("unsafe input accepted")
			}
			if time.Since(start) > time.Second {
				t.Fatal("special-file rejection blocked")
			}
		})
	}
}

func TestNativeProviderRootAndArtifactChangesRefused(t *testing.T) {
	for _, kind := range []string{"wrong-provider", "wrong-identity", "wrong-path", "quarantine", "root-replace", "artifact-replace", "closed-provider"} {
		t.Run(kind, func(t *testing.T) {
			e, p, a, b := fixture(t)
			write(t, filepath.Join(a.Path, "file"), []byte("approved"))
			m := built(t, e, p, a, b)
			o := opened(t, e, p, a, m, permit)
			h := handle(t, o, "file")
			switch kind {
			case "wrong-provider":
				q, err := storage.Open(filepath.Join(t.TempDir(), "other"), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer q.Close()
				p = q
			case "wrong-identity":
				v := *a.Identity
				v.Inode++
				a.Identity = &v
			case "wrong-path":
				a.Path = filepath.Dir(a.Path)
			case "quarantine":
				a.Phase = "quarantined"
			case "root-replace":
				base := filepath.Dir(a.Path)
				if err := os.Rename(base, base+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(base, 0700); err != nil {
					t.Fatal(err)
				}
			case "artifact-replace":
				if err := os.Rename(a.Path, a.Path+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(a.Path, 0700); err != nil {
					t.Fatal(err)
				}
			case "closed-provider":
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Open(context.Background(), p, a, m, scope(), permit); err == nil {
				t.Fatal("stale/wrong anchor accepted")
			}
			if kind == "root-replace" || kind == "artifact-replace" || kind == "closed-provider" {
				if got, err := o.Read(context.Background(), h, 0, 8); got != nil || err == nil {
					t.Fatalf("old handle returned%q error%v", got, err)
				}
			}
		})
	}
}

func TestSessionHandlesAndCurrentGuard(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	m := built(t, e, p, a, b)
	var deny atomic.Bool
	g := func(_ context.Context, x Access) error {
		if x.Binding != b || x.Scope != scope() {
			return fmt.Errorf("wrong authority")
		}
		if deny.Load() {
			return ErrDenied
		}
		return nil
	}
	o := opened(t, e, p, a, m, g)
	h := handle(t, o, "file")
	other := opened(t, e, p, a, m, permit)
	if got, err := other.Read(context.Background(), h, 0, 8); got != nil || !errors.Is(err, ErrClosed) {
		t.Fatal("cross-session handle accepted")
	}
	deny.Store(true)
	if _, err := o.Stat(context.Background(), "file"); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked stat")
	}
	if _, _, err := o.List(context.Background(), "", 0, 10); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked list")
	}
	if _, err := o.OpenRead(context.Background(), "file"); !errors.Is(err, ErrDenied) {
		t.Fatal("revoked open")
	}
	if got, err := o.Read(context.Background(), h, 0, 8); got != nil || !errors.Is(err, ErrDenied) {
		t.Fatal("revoked read")
	}
	var calls int
	denyLast := func(_ context.Context, x Access) error {
		if x.Operation == "read" {
			calls++
			if calls == 4 {
				return ErrDenied
			}
		}
		return nil
	}
	last := opened(t, e, p, a, m, denyLast)
	if got, err := last.Read(context.Background(), handle(t, last, "file"), 0, 8); got != nil || !errors.Is(err, ErrDenied) {
		t.Fatalf("final guard: %q %v calls%d", got, err, calls)
	}
}

func TestBoundsCancellationAndManifestDefensiveCopy(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	m := built(t, e, p, a, b)
	l := DefaultLimits()
	l.Handles = 1
	l.Reads = 1
	small, err := New(l)
	if err != nil {
		t.Fatal(err)
	}
	o := opened(t, small, p, a, m, permit)
	h := handle(t, o, "file")
	if _, err := o.OpenRead(context.Background(), "file"); !errors.Is(err, ErrLimit) {
		t.Fatal("handle cap")
	}
	m.Entries[1].Chunks[0] = strings.Repeat("0", 64)
	if got, err := o.Read(context.Background(), h, 0, 8); string(got) != "approved" || err != nil {
		t.Fatal("caller mutated session manifest")
	}
	if err := small.enter(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got, err := o.Read(ctx, h, 0, 8); got != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queued I/O ignored cancellation")
	}
	small.leave()
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := small.Build(ctx, p, a, b); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled build")
	}
	for _, which := range []string{"entries", "chunks", "manifest"} {
		t.Run(which, func(t *testing.T) {
			l := DefaultLimits()
			switch which {
			case "entries":
				l.Entries = 1
			case "chunks":
				l.Chunks = 1
				write(t, filepath.Join(a.Path, "large"), make([]byte, ChunkBytes+1))
			case "manifest":
				l.ManifestBytes = 100
			}
			limited, err := New(l)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := limited.Build(context.Background(), p, a, b); !errors.Is(err, ErrLimit) {
				t.Fatalf("cap %s: %v", which, err)
			}
		})
	}
}

func TestManifestCanonicalPathsGraphAndCommitment(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	original := built(t, e, p, a, b)
	for _, path := range []string{"/file", "../file", "./file", "file/", "file//x", "file\\x", "file\x00", "a/../file", strings.Repeat("a", 256)} {
		t.Run(fmt.Sprintf("path-%q", path), func(t *testing.T) {
			if err := validPath(path); err == nil {
				t.Fatal("unsafe path")
			}
		})
	}
	for _, kind := range []string{"duplicate", "parent-file", "nil-chunks", "hash", "root", "foreign-owner", "cross-mount", "huge-size"} {
		t.Run(kind, func(t *testing.T) {
			m := original
			m.Entries = append([]Entry{}, original.Entries...)
			m.Entries[1].Chunks = append([]string{}, m.Entries[1].Chunks...)
			switch kind {
			case "duplicate":
				m.Entries = append(m.Entries, m.Entries[1])
			case "parent-file":
				x := m.Entries[1]
				x.Path = "file/child"
				m.Entries = append(m.Entries, x)
			case "nil-chunks":
				m.Entries[1].Chunks = nil
			case "hash":
				m.Entries[1].Chunks[0] = "x"
			case "root":
				m.Entries[0].Path = "root"
			case "foreign-owner":
				m.Entries[1].Version.Identity.Owner++
			case "cross-mount":
				m.Entries[1].Version.Mount += "other"
			case "huge-size":
				m.Entries[1].Version.Size = math.MaxInt64
			}
			m.SHA256, _, _ = manifestHash(m)
			if err := m.Validate(DefaultLimits()); err == nil {
				t.Fatal("bad manifest accepted")
			}
		})
	}
}

func TestHostileRenameLinkRaceNeverDisclosesOutsideBytes(t *testing.T) {
	e, p, a, b := fixture(t)
	if err := os.Mkdir(filepath.Join(a.Path, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(a.Path, "dir", "file"), []byte("approved"))
	outside := t.TempDir()
	write(t, filepath.Join(outside, "file"), []byte("SECRET!!"))
	m := built(t, e, p, a, b)
	o := opened(t, e, p, a, m, permit)
	h := handle(t, o, "dir/file")
	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if os.Rename(filepath.Join(a.Path, "dir"), filepath.Join(a.Path, "hold")) != nil {
				continue
			}
			_ = os.Symlink(outside, filepath.Join(a.Path, "dir"))
			_ = os.Remove(filepath.Join(a.Path, "dir"))
			_ = os.Rename(filepath.Join(a.Path, "hold"), filepath.Join(a.Path, "dir"))
		}
	}()
	for i := 0; i < 500; i++ {
		got, err := o.Read(context.Background(), h, 0, 8)
		if bytes.Contains(got, []byte("SECRET")) || err != nil && got != nil || err == nil && string(got) != "approved" {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("race disclosed: %q %v", got, err)
		}
	}
	stop.Store(true)
	wg.Wait()
	if got, err := os.ReadFile(filepath.Join(outside, "file")); err != nil || string(got) != "SECRET!!" {
		t.Fatal("outside source changed")
	}
}

func TestInjectedNamesStaleGenerationAndProviderCloseRace(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	m := built(t, e, p, a, b)
	o := opened(t, e, p, a, m, permit)
	write(t, filepath.Join(a.Path, "SECRET-NAME"), []byte("secret"))
	if items, _, err := o.List(context.Background(), "", 0, 10); items != nil || err == nil {
		t.Fatal("changed generation listed names")
	}
	if _, err := o.OpenRead(context.Background(), "SECRET-NAME"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unpublished path exposed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, _ := p.OpenArtifactReadOnly(context.Background(), a)
			if f != nil {
				f.Close()
			}
		}()
	}
	_ = p.Close()
	wg.Wait()
}
