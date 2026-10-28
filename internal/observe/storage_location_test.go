package observe

import (
	"context"
	"getarm.org/internal/storage"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageLocationNativeExistingAndMissing(t *testing.T) {
	p, e := StorageLocation(t.Context(), t.TempDir())
	if e != nil || p.Volume == "" || p.Mount == "" || p.FilesystemID == "" || p.Filesystem == "" {
		t.Fatal(p, e)
	}
	if _, e = StorageLocation(t.Context(), filepath.Join(t.TempDir(), "absent")); e == nil {
		t.Fatal("missing location adopted")
	}
}
func TestStorageBenchmarkNativeMetricsAndExactScratchCleanup(t *testing.T) {
	path := t.TempDir()
	sentinel := filepath.Join(path, "unrelated")
	if e := os.WriteFile(sentinel, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	r, e := BenchmarkStorage(ctx, path, "fixture", 8<<20)
	if e != nil || r.ReadBytesPerSecond <= 0 || r.WriteBytesPerSecond <= 0 || r.RandomReadIOPS <= 0 || r.RandomReadLatencyNS <= 0 || r.Method == "" {
		t.Fatal(r, e)
	}
	entries, e := os.ReadDir(path)
	if e != nil || len(entries) != 1 || entries[0].Name() != "unrelated" {
		t.Fatal(entries, e)
	}
	stopped, stop := context.WithCancel(t.Context())
	stop()
	if _, e = BenchmarkStorage(stopped, path, "fixture", 8<<20); e != context.Canceled {
		t.Fatal(e)
	}
}

func TestStorageBenchmarkOwnedRejectsDirectoryReplacementBeforeScratch(t *testing.T) {
	base := t.TempDir()
	selected := filepath.Join(base, "selected")
	outside := filepath.Join(base, "outside")
	if e := os.Mkdir(selected, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(outside, 0700); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(selected)
	if e != nil {
		t.Fatal(e)
	}
	identity, e := storage.DirectoryIdentity(int(f.Fd()))
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(selected, filepath.Join(base, "original")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, selected); e != nil {
		t.Fatal(e)
	}
	if _, e = BenchmarkStorageOwned(t.Context(), selected, "original-volume", 8<<20, identity); e == nil {
		t.Fatal("replacement root received benchmark writes")
	}
	entries, e := os.ReadDir(outside)
	if e != nil || len(entries) != 0 {
		t.Fatal("unrelated directory changed", entries, e)
	}
}
