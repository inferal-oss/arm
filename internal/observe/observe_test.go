package observe

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSparseHardLinksAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(outside, []byte("must remain outside measurement and cleanup"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sparse")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const length = 64 << 20
	if err := f.Truncate(length); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("payload"), length-4096); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := Scan(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Logical.Value == nil || *r.Logical.Value != length || !r.Logical.Complete || r.HardLinksSkipped != 1 || r.SymlinksSkipped != 1 {
		t.Fatalf("wrong inode/symlink accounting: %+v", r)
	}
	if r.Allocated.Value == nil || *r.Allocated.Value >= length {
		t.Fatalf("sparse allocation presented as logical length: %+v", r.Allocated)
	}
	if r.Exclusive.Value != nil || r.Reclaimable.Value != nil {
		t.Fatal("allocation incorrectly promised exclusive or reclaimable bytes")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "must remain outside measurement and cleanup" {
		t.Fatal("outside sentinel changed")
	}
}

func TestUnknownAndInvalidCounters(t *testing.T) {
	r := Filesystem(context.Background(), filepath.Join(t.TempDir(), "missing"))
	if r.Total.Value != nil || r.Total.Quality != "unavailable" || r.Total.Detail == "" {
		t.Fatalf("missing path became zero usage: %+v", r)
	}
	encoded, err := json.Marshal(r.Total)
	if err != nil || !strings.Contains(string(encoded), `"value":null`) {
		t.Fatalf("unknown wire contract: %s %v", encoded, err)
	}
	for _, r := range []struct{ blocks, free, available, size uint64 }{{10, 11, 0, 4096}, {10, 5, 6, 4096}, {math.MaxUint64, 1, 1, 4096}, {10, 1, 1, 0}} {
		v := filesystemValues("fixture", "statfs", r.blocks, r.free, r.available, r.size)
		if v.Total.Value != nil || v.Available.Value != nil || v.Used.Value != nil {
			t.Fatalf("invalid counters accepted: %+v", v)
		}
	}
}

func TestSnapshotKeepsScopesDistinct(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s := Snapshot(ctx, t.TempDir())
	if s["cpu"].Total.Value == nil || *s["cpu"].Total.Value < 1 {
		t.Fatal("visible CPU inventory missing")
	}
	if s["cpu"].Used.Value != nil {
		t.Fatal("CPU count confused with CPU usage")
	}
	disk := s["disk"]
	if disk.Filesystem == nil || disk.Total.Value == nil || disk.Total.At.IsZero() || !strings.Contains(disk.Available.Scope, "nonprivileged") {
		t.Fatalf("filesystem observation missing metadata: %+v", disk)
	}
}

func TestPropertyListAndPoolSelection(t *testing.T) {
	data := []byte(`<?xml version="1.0"?><plist><dict><key>Error</key><false/><key>Containers</key><array><dict><key>ContainerReference</key><string>disk9</string><key>APFSContainerUUID</key><string>UUID-9</string><key>CapacityCeiling</key><integer>1000</integer><key>CapacityFree</key><integer>200</integer></dict><dict><key>ContainerReference</key><string>disk3</string><key>APFSContainerUUID</key><string>UUID-3</string></dict></array></dict></plist>`)
	c, err := apfsPool(data, "disk9")
	if err != nil || c["APFSContainerUUID"] != "UUID-9" || c["CapacityCeiling"] != uint64(1000) {
		t.Fatalf("wrong container: %+v %v", c, err)
	}
	if _, err := apfsPool(data, "disk4"); err == nil {
		t.Fatal("invented pool identity for unknown container")
	}
	for _, bad := range []string{`<plist><dict><key>unfinished</key></dict></plist>`, `<plist><dict><string>not a key</string></dict></plist>`, `<plist><dict><key>x</key><array>` + strings.Repeat(`<array>`, 40) + strings.Repeat(`</array>`, 40) + `</array></dict></plist>`} {
		if _, err := plist([]byte(bad)); err == nil {
			t.Fatal("accepted malformed or excessively nested metadata")
		}
	}
}
