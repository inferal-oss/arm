package shareview

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/storage"
)

func origin(t *testing.T) (*sharefs.Origin, *atomic.Bool) {
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
	for path, data := range map[string]string{"selected/payload": "verified bytes", "outside": "not in the view"} {
		if err = os.WriteFile(filepath.Join(a.Path, path), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
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
		if err := o.WaitDrained(ctx); err != nil {
			t.Error(err)
		}
	})
	return o, deny
}

func TestRealOriginSubtreeConfinementAndRevocation(t *testing.T) {
	o, deny := origin(t)
	ctx := context.Background()
	v, err := New(ctx, o, "selected")
	if err != nil {
		t.Fatal(err)
	}
	i, err := v.Stat(ctx, "")
	if err != nil || !i.Directory || i.Path != "" {
		t.Fatalf("root: %+v %v", i, err)
	}
	items, next, err := v.List(ctx, "", 0, 256)
	if err != nil || next != 1 || len(items) != 1 || items[0].Path != "payload" {
		t.Fatalf("list: %+v %d %v", items, next, err)
	}
	for _, path := range []string{"..", "../outside", "/outside", "selected/../../outside", "payload/", "payload\\..", "payload\x00", "\u202eoutside"} {
		if _, err = v.Stat(ctx, path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if _, err = v.Stat(ctx, "outside"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside visible: %v", err)
	}
	h, err := v.OpenRead(ctx, "payload")
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.Read(ctx, h, 0, 256)
	if !errors.Is(err, io.EOF) || !bytes.Equal(got, []byte("verified bytes")) {
		t.Fatalf("read %q %v", got, err)
	}
	other, err := New(ctx, o, "selected")
	if err != nil {
		t.Fatal(err)
	}
	if got, err = other.Read(ctx, h, 0, 1); len(got) != 0 || !errors.Is(err, sharefs.ErrClosed) {
		t.Fatal("handle escaped view")
	}
	deny.Store(true)
	if got, err = v.Read(ctx, h, 0, 1); got != nil || !errors.Is(err, sharefs.ErrDenied) {
		t.Fatalf("revoked bytes %q %v", got, err)
	}
	if _, err = v.Stat(ctx, ""); !errors.Is(err, sharefs.ErrDenied) {
		t.Fatalf("revoked metadata %v", err)
	}
	if err = v.CloseRead(h); err != nil || v.Outstanding() != 0 {
		t.Fatalf("close %v %d", err, v.Outstanding())
	}
}

type forged struct {
	Backend
	list     []sharefs.Info
	next     int
	data     []byte
	err      error
	closeErr error
}

func (f *forged) List(context.Context, string, int, int) ([]sharefs.Info, int, error) {
	return f.list, f.next, f.err
}
func (f *forged) Read(context.Context, string, int64, int) ([]byte, error) { return f.data, f.err }
func (f *forged) CloseRead(h string) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	return f.Backend.CloseRead(h)
}

func TestBoundaryRejectsForgedPagesAndPartialErrors(t *testing.T) {
	o, _ := origin(t)
	ctx := context.Background()
	f := &forged{Backend: o}
	v, err := New(ctx, f, "selected")
	if err != nil {
		t.Fatal(err)
	}
	good := sharefs.Info{Path: "selected/payload", Mode: 0400, Size: 14}
	for _, tt := range []struct {
		items []sharefs.Info
		next  int
	}{{[]sharefs.Info{{Path: "outside", Mode: 0400}}, 1}, {[]sharefs.Info{{Path: "selected/sub/file", Mode: 0400}}, 1}, {[]sharefs.Info{good, good}, 2}, {[]sharefs.Info{good}, 0}, {[]sharefs.Info{{Path: "selected/payload", Mode: 0777}}, 1}} {
		f.list, f.next = tt.items, tt.next
		if got, _, err := v.List(ctx, "", 0, 256); got != nil || !errors.Is(err, sharefs.ErrDenied) {
			t.Fatalf("accepted forged page %+v %v", got, err)
		}
	}
	h, err := v.OpenRead(ctx, "payload")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		data []byte
		err  error
	}{{[]byte("leak"), sharefs.ErrChanged}, {[]byte("too much"), nil}, {[]byte("x"), nil}} {
		f.data, f.err = tt.data, tt.err
		if got, err := v.Read(ctx, h, 0, 4); got != nil || err == nil {
			t.Fatalf("accepted %q %v", got, err)
		}
	}
	f.closeErr = errors.New("unknown remote outcome")
	if err = v.CloseRead(h); err == nil || v.Outstanding() != 1 {
		t.Fatal("failed close lost obligation")
	}
	f.closeErr = nil
	if err = v.CloseRead(h); err != nil || v.Outstanding() != 0 {
		t.Fatal("exact retry failed", err)
	}
}

func TestFileRootAndBoundedHandles(t *testing.T) {
	o, _ := origin(t)
	ctx := context.Background()
	v, err := New(ctx, o, "selected/payload")
	if err != nil {
		t.Fatal(err)
	}
	if i, err := v.Stat(ctx, ""); err != nil || i.Directory || i.Size != 14 {
		t.Fatal(i, err)
	}
	var handles []string
	for range 64 {
		h, err := v.OpenRead(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, h)
	}
	if _, err = v.OpenRead(ctx, ""); !errors.Is(err, sharefs.ErrLimit) {
		t.Fatal("unbounded handles", err)
	}
	for _, h := range handles {
		if err = v.CloseRead(h); err != nil {
			t.Fatal(err)
		}
	}
	for _, prefix := range []string{"", ".", "..", "selected/", "/selected"} {
		if _, err = New(ctx, o, prefix); err == nil {
			t.Fatal("bad prefix", prefix)
		}
	}
}
