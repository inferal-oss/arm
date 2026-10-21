package observe

import (
	"math"
	"os"
	"testing"
	"time"
)

func TestDarwinAvailabilityOnlyAddsDisjointFreeAndFileBackedCounters(t *testing.T) {
	r := darwinAvailability(16384*1000, 16384, 10, 100, time.Now())
	if r.Available == nil || *r.Available != 16384*110 || r.Quality != "estimated" {
		t.Fatal("wrong estimate", r)
	}
	for _, args := range [][4]uint64{{100, 0, 1, 1}, {100, 10, 8, 8}, {100, 10, math.MaxUint64, 1}, {math.MaxUint64, 4096, 1, 1}} {
		if r := darwinAvailability(args[0], args[1], args[2], args[3], time.Now()); r.Available != nil {
			t.Fatal("invalid counters accepted", args, r)
		}
	}
}

func TestNativeDarwinAvailability(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_MONITOR") == "" {
		t.Skip("enable required native observation")
	}
	r := MemoryAvailability(t.Context())
	if r.Total == nil || *r.Total <= 0 || r.Available == nil || *r.Available < 0 || *r.Available > *r.Total || r.Quality != "estimated" {
		t.Fatalf("native availability unavailable: %+v", r)
	}
}
