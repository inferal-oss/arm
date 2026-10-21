package observe

import (
	"os"
	"testing"
	"time"
)

func TestLinuxAvailabilityExactParsingAndUnknownCounters(t *testing.T) {
	r := parseMemoryAvailability([]byte("MemTotal: 100000 kB\nMemAvailable: 45000 kB\n"), time.Now())
	if r.Total == nil || *r.Total != 102400000 || r.Available == nil || *r.Available != 46080000 || r.Quality != "estimated" {
		t.Fatal(r)
	}
	for _, text := range []string{"", "MemTotal: 100 kB\nMemFree: 80 kB", "MemTotal: 100 kB\nMemAvailable: 101 kB", "MemTotal: 100 kB\nMemAvailable: -1 kB", "MemTotal: 100 kB\nMemAvailable: 1 kB\nMemAvailable: 2 kB", "MemTotal: 18446744073709551615 kB\nMemAvailable: 1 kB"} {
		if r := parseMemoryAvailability([]byte(text), time.Now()); r.Available != nil {
			t.Fatal("invalid/unknown estimate accepted", text, r)
		}
	}
	for _, bad := range []string{"MemAvailable:", "MemAvailable: 1 MB", "MemAvailable: 1 kB extra", "MemTotal: 1 bytes"} {
		text := "MemTotal: 100 kB\nMemAvailable: 1 kB\n" + bad
		if r := parseMemoryAvailability([]byte(text), time.Now()); r.Available != nil {
			t.Fatal("malformed duplicate counter bypassed validation", text, r)
		}
		if r := meminfo([]byte(text)); r.Available.Value != nil || r.Used.Value != nil {
			t.Fatal("malformed counter bypassed telemetry validation", text, r)
		}
	}
}

func TestNativeLinuxAvailability(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_MONITOR") == "" {
		t.Skip("enable required native observation")
	}
	r := MemoryAvailability(t.Context())
	if r.Total == nil || *r.Total <= 0 || r.Available == nil || *r.Available < 0 || *r.Available > *r.Total {
		t.Fatalf("native availability unavailable: %+v", r)
	}
}
