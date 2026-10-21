package observe

import "testing"

func TestMeminfoUsesKernelAvailabilityEstimate(t *testing.T) {
	r := meminfo([]byte("MemTotal: 1000 kB\nMemFree: 10 kB\nMemAvailable: 200 kB\nCached: 300 kB\n"))
	if r.Total.Value == nil || *r.Total.Value != 1000*1024 || r.Available.Value == nil || *r.Available.Value != 200*1024 || *r.Used.Value != 800*1024 {
		t.Fatalf("incorrect or double-counted memory: %+v", r)
	}
	if r.Available.Quality != "estimated" {
		t.Fatal("kernel estimate presented as exact")
	}
	for _, bad := range []string{"MemFree: 10 kB", "MemTotal: 1000 kB\nMemAvailable: 1001 kB", "MemTotal: 1000 kB\nMemAvailable: -2 kB", "MemTotal: 1000 kB\nMemAvailable: 3 MB"} {
		if v := meminfo([]byte(bad)); v.Available.Value != nil || v.Used.Value != nil {
			t.Fatal("invented available memory from incomplete counters")
		}
	}
}
