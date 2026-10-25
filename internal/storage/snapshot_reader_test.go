package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/yrashk/arm/internal/model"
)

// Native storage component only: these callbacks stand in for a separately
// admitted/persisted Manager operation and do not prove public authority.
func snapshotReaderFixture(t testing.TB, count int) (*Provider, model.Artifact, SnapshotGeneration, []byte) {
	t.Helper()
	p, err := Open(filepath.Join(t.TempDir(), "storage"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})
	name := "0123456789abcdef0123456789abcdef"
	id, err := p.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	source := model.Artifact{Name: name, Path: p.Path(name), Identity: &id, Phase: "owned"}
	content := bytes.Repeat([]byte("snapshot reader contents\n"), 128)
	selections := make([]model.ExportSelection, count)
	for i := range selections {
		name := fmt.Sprintf("file-%02d", i)
		if err := os.WriteFile(filepath.Join(source.Path, name), content, 0600); err != nil {
			t.Fatal(err)
		}
		selections[i] = model.ExportSelection{Source: name, Path: name}
	}
	current := func(ctx context.Context) error { return ctx.Err() }
	plan, err := p.PlanSnapshot(context.Background(), source, selections, MaxExportBytes, SnapshotVerifiedCopy, current)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.CopySnapshot(context.Background(), snapshotTestIntent(plan), SnapshotCallbacks{
		Current: current,
		Intent:  func(context.Context, SnapshotCopyIntent) error { return nil },
		Owned:   func(_ context.Context, _ SnapshotCopyIntent, a model.Artifact) error { return p.Check(a) },
		Prepared: func(_ context.Context, _ SnapshotCopyIntent, result SnapshotCopyResult) error {
			return result.Native.validateClosed()
		},
	})
	if err != nil || result.Phase != "prepared" || !result.FilesClosed || result.UnknownClose {
		t.Fatal("real copy did not prepare", result.Phase, err)
	}
	return p, source, result.Generation, content
}

func TestSnapshotReaderFreezesManifestAndOwnsReturnedBuffers(t *testing.T) {
	p, source, generation, content := snapshotReaderFixture(t, 2)
	r, err := p.NewSnapshotReader(generation, func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	// All nested slices/pointers came from the caller. Changing them must not
	// affect the private commitment, path index or future native checks.
	generation.Files[0].Source.Chunks[0] = "changed"
	generation.Files[0].Output.Path = "../outside"
	generation.Artifact.Identity.Inode++
	if _, err := p.NewSnapshotReader(generation, func(context.Context) error { return nil }); err == nil {
		t.Fatal("changed manifest accepted")
	}
	if err := os.RemoveAll(source.Path); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		read, err := r.ReadChunk(context.Background(), "file-00", 0)
		if err != nil || !bytes.Equal(read.Data, content) || read.Native.validateClosed() != nil {
			t.Fatal("frozen reader or independently copied bytes changed", i, err)
		}
		read.Data[0] ^= 0xff
	}
}

func TestSnapshotReaderRechecksAuthorityAndNativeBacking(t *testing.T) {
	for _, scenario := range []string{"initial-revoke", "final-revoke", "tamper", "replaced-root", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			p, _, generation, _ := snapshotReaderFixture(t, 1)
			var calls atomic.Int32
			var revoke atomic.Bool
			current := func(ctx context.Context) error {
				n := calls.Add(1)
				if revoke.Load() && (scenario != "final-revoke" || n%2 == 0) {
					return errors.New("exact generation authority revoked")
				}
				return ctx.Err()
			}
			r, err := p.NewSnapshotReader(generation, current)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.ReadChunk(context.Background(), "file-00", 0); err != nil || calls.Load() != 2 {
				t.Fatal("initial actual read/check pair failed", err, calls.Load())
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "initial-revoke", "final-revoke":
				revoke.Store(true)
			case "tamper":
				path := filepath.Join(generation.Artifact.Path, "file-00")
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("changed copied contents"), 0400); err != nil {
					t.Fatal(err)
				}
			case "replaced-root":
				if err := os.Rename(generation.Artifact.Path, generation.Artifact.Path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(generation.Artifact.Path, 0700); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			}
			read, err := r.ReadChunk(ctx, "file-00", 0)
			if err == nil || read.Data != nil {
				t.Fatal("reused reader disclosed changed or unauthorized bytes", err)
			}
			if scenario == "final-revoke" && (calls.Load() != 4 || read.Native.validateClosed() != nil) {
				t.Fatal("final refusal did not close its original descriptors", calls.Load(), read.Native)
			}
		})
	}
}

func TestSnapshotReaderConcurrentReadsRemainIndependent(t *testing.T) {
	p, _, generation, content := snapshotReaderFixture(t, 2)
	var checks atomic.Int32
	r, err := p.NewSnapshotReader(generation, func(ctx context.Context) error {
		checks.Add(1)
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	for i := range 8 {
		go func() {
			read, err := r.ReadChunk(context.Background(), fmt.Sprintf("file-%02d", i%2), 0)
			if err == nil && (!bytes.Equal(read.Data, content) || read.Native.validateClosed() != nil) {
				err = fmt.Errorf("independent native read/result mismatch")
			}
			if len(read.Data) > 0 {
				read.Data[0] ^= byte(i + 1)
			}
			results <- err
		}()
	}
	for range 8 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if checks.Load() != 16 {
		t.Fatal("an authorization verdict was reused", checks.Load())
	}
}

func TestSnapshotReaderInvalidRequestsRefuse(t *testing.T) {
	p, _, generation, _ := snapshotReaderFixture(t, 1)
	if _, err := p.NewSnapshotReader(generation, nil); err == nil {
		t.Fatal("missing authority accepted")
	}
	var noProvider *Provider
	if _, err := noProvider.NewSnapshotReader(generation, func(context.Context) error { return nil }); err == nil {
		t.Fatal("missing provider accepted")
	}
	r, err := p.NewSnapshotReader(generation, func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		path  string
		chunk int
	}{{"../file-00", 0}, {"file-00", -1}, {"file-00", 1}, {"file-01", 0}} {
		if read, err := r.ReadChunk(context.Background(), request.path, request.chunk); err == nil || read.Data != nil {
			t.Fatal("invalid request returned bytes", request, err)
		}
	}
	if read, err := r.ReadChunk(nil, "file-00", 0); err == nil || read.Data != nil {
		t.Fatal("nil context accepted")
	}
	var absent *SnapshotReader
	if read, err := absent.ReadChunk(context.Background(), "file-00", 0); err == nil || read.Data != nil {
		t.Fatal("missing reader accepted")
	}
}

// This measures a native component with 64 real copied small files, rather
// than link/mount throughput. Both cases perform fresh native reads/checks.
func BenchmarkSnapshotReader(b *testing.B) {
	p, _, generation, content := snapshotReaderFixture(b, MaxExportFiles)
	current := func(ctx context.Context) error { return ctx.Err() }
	r, err := p.NewSnapshotReader(generation, current)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("single-call-manifest", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			read, err := p.ReadSnapshotChunk(context.Background(), generation, "file-00", 0, current)
			if err != nil || !bytes.Equal(read.Data, content) {
				b.Fatal("read failed", err)
			}
		}
	})
	b.Run("reused-immutable-manifest", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			read, err := r.ReadChunk(context.Background(), "file-00", 0)
			if err != nil || !bytes.Equal(read.Data, content) {
				b.Fatal("read failed", err)
			}
		}
	})
}
