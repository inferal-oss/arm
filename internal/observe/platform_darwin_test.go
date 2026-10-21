package observe

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestVMFreeDoesNotSumOverlappingLists(t *testing.T) {
	data := []byte("Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 10.\nPages inactive: 200.\nPages speculative: 5.\nPages purgeable: 100.\n")
	got, err := vmFree(data)
	if err != nil || got != 10*16384 {
		t.Fatalf("double counted lists: %f %v", got, err)
	}
	if _, err := vmFree([]byte("Pages free: 10.")); err == nil {
		t.Fatal("invented page size")
	}
	if _, err := vmFree([]byte("Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: broken.")); err == nil {
		t.Fatal("accepted malformed free counter")
	}
}

func TestAPFSCloneRemainsUnknown(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fs := Filesystem(ctx, root)
	if fs.Filesystem == nil || fs.Filesystem.Type != "apfs" {
		t.Skip("APFS is not available for the fixture")
	}
	original := filepath.Join(root, "original")
	clone := filepath.Join(root, "clone")
	if err := os.WriteFile(original, make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Clonefile(original, clone, 0); err != nil {
		t.Fatal(err)
	}
	r, err := Scan(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Logical.Value == nil || *r.Logical.Value != 2<<20 || r.HardLinksSkipped != 0 {
		t.Fatalf("clone confused with a hard link: %+v", r)
	}
	if r.Reclaimable.Value != nil || r.Exclusive.Value != nil {
		t.Fatal("clone allocation presented as exclusive/reclaimable")
	}
}

func TestAPFSVolumesShareContainer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a := Filesystem(ctx, "/")
	b := Filesystem(ctx, "/System/Volumes/Data")
	if a.Filesystem == nil || b.Filesystem == nil || a.Filesystem.Type != "apfs" || b.Filesystem.Type != "apfs" {
		t.Skip("system/data APFS pair unavailable")
	}
	if a.Filesystem.Pool == "" || b.Filesystem.Pool == "" {
		if os.Getenv("ARM_TEST_NATIVE_APFS") == "1" {
			t.Fatalf("required native APFS metadata unavailable: %+v %+v", a.Filesystem, b.Filesystem)
		}
		t.Skip("native APFS metadata unavailable; run ARM_TEST_NATIVE_APFS=1 outside the sandbox to require it")
	}
	if a.Filesystem.Pool != b.Filesystem.Pool || a.Filesystem.Volume == b.Filesystem.Volume || !a.Filesystem.Shared || !b.Filesystem.Shared {
		t.Fatalf("wrong shared-container identity: %+v %+v", a.Filesystem, b.Filesystem)
	}
	if a.Filesystem.PoolTotal.Value == nil || b.Filesystem.PoolTotal.Value == nil || *a.Filesystem.PoolTotal.Value != *b.Filesystem.PoolTotal.Value {
		t.Fatal("pool capacity missing or inconsistent")
	}
}
