package observe

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestDiskCountersKeepExactNonprivilegedBytesAndRejectOverflow(t *testing.T) {
	at := time.Now().UTC()
	reading := diskCounters("/fixture", "volume", "test", 9007199254741999, 9007199254741099, 9007199254740993, 1, at)
	if reading.Available == nil || *reading.Available != 9007199254740993 || reading.Total == nil || *reading.Total != 9007199254741999 || reading.Quality != "reported" {
		t.Fatal(reading)
	}
	for _, values := range [][4]uint64{{10, 11, 1, 1}, {10, 5, 6, 1}, {10, 5, 1, 0}, {math.MaxInt64, 1, 1, 2}, {0, 0, 0, 1}} {
		bad := diskCounters("/fixture", "v", "x", values[0], values[1], values[2], values[3], at)
		if bad.Available != nil {
			t.Fatal("invalid counters became availability", values, bad)
		}
	}
	zero := diskCounters("/fixture", "v", "x", 100, 10, 0, 4096, at)
	if zero.Available == nil || *zero.Available != 0 {
		t.Fatal("known full disk became unknown", zero)
	}
}
func TestDiskAPFSBoundIsOneSharedPoolAndOptionalFailureKeepsStatfs(t *testing.T) {
	reading := diskCounters("/fixture", "v", "apfs", 1000, 800, 700, 1, time.Now().UTC())
	bounded := boundDiskPool(reading, "apfs-container:one", 1000, 500)
	if bounded.Available == nil || *bounded.Available != 500 || !bounded.Shared || bounded.Pool != "apfs-container:one" {
		t.Fatal(bounded)
	}
	more := boundDiskPool(reading, "apfs-container:one", 1000, 900)
	if more.Available == nil || *more.Available != 700 {
		t.Fatal("container counter inflated user headroom", more)
	}
	unknown := boundDiskPool(reading, "", 1000, 0)
	if unknown.Available == nil || *unknown.Available != 700 {
		t.Fatal("optional identity failure invented zero", unknown)
	}
}
func TestDiskAvailabilityRealFilesystemAndCancelledContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	reading := DiskAvailability(ctx, t.TempDir())
	if reading.Available == nil || reading.Total == nil || *reading.Available < 0 || *reading.Available > *reading.Total || reading.Volume == "" {
		t.Fatal(reading)
	}
	stopped, stop := context.WithCancel(t.Context())
	stop()
	unavailable := DiskAvailability(stopped, t.TempDir())
	if unavailable.Available != nil {
		t.Fatal(unavailable)
	}
}
