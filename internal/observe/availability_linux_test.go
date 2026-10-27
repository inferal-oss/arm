package observe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func TestCgroupMemoryBudgetDoesNotInventResidentUsage(t *testing.T) {
	total, available := int64(500), int64(100)
	r := memoryAvailabilityValues(model.MemoryAvailability{
		At: time.Now(), Total: &total, Available: &available,
		Scope: "host+complete-memory-cgroup-ancestry", Method: "cgroup", Quality: "estimated",
	})
	if r.Total.Scope != "minimum-host-and-memory-cgroup-capacity" || r.Available.Value == nil || *r.Available.Value != 100 || r.Used.Value != nil {
		t.Fatalf("cgroup headroom was presented as resident usage: %+v", r)
	}
}

func TestLinuxMemoryCgroupHeadroomUsesEveryAncestor(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "team", "worker")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(dir, name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "team"), "memory.max", "1000\n")
	write(filepath.Join(root, "team"), "memory.current", "900\n")
	write(child, "memory.max", "500\n")
	write(child, "memory.current", "100\n")
	membership := []byte("0::/team/worker\n")
	mounts := []byte(fmt.Sprintf("1 0 0:1 / %s rw - cgroup2 cgroup rw\n", root))
	capacity, free, limited, err := memoryCgroupV2Headroom(t.Context(), membership, mounts)
	if err != nil || !limited || capacity != 500 || free != 100 {
		t.Fatalf("ancestor limit must bound headroom: capacity=%d free=%d limited=%t err=%v", capacity, free, limited, err)
	}
	write(filepath.Join(root, "team"), "memory.current", "1200\n")
	_, free, _, err = memoryCgroupV2Headroom(t.Context(), membership, mounts)
	if err != nil || free != 0 {
		t.Fatalf("overdrawn ancestor must yield zero: free=%d err=%v", free, err)
	}
	write(filepath.Join(root, "team"), "memory.max", "max\n")
	_, free, _, err = memoryCgroupV2Headroom(t.Context(), membership, mounts)
	if err != nil || free != 400 {
		t.Fatalf("unlimited ancestor must not hide child limit: free=%d err=%v", free, err)
	}
	write(child, "memory.max", "max\n")
	capacity, free, limited, err = memoryCgroupV2Headroom(t.Context(), membership, mounts)
	if err != nil || limited || capacity != 0 || free != 0 {
		t.Fatalf("fully unlimited hierarchy must retain host estimate: capacity=%d free=%d limited=%t err=%v", capacity, free, limited, err)
	}
}

func TestLinuxMemoryCgroupHeadroomRefusesHiddenOrInvalidScope(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "worker")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	membership := []byte("0::/worker\n")
	mount := func(rootField string) []byte {
		return []byte(fmt.Sprintf("1 0 0:1 %s %s rw - cgroup2 cgroup rw\n", rootField, root))
	}
	for _, tc := range []struct {
		name   string
		member []byte
		mounts []byte
	}{
		{"hidden-ancestor", membership, mount("/worker")},
		{"unmapped-membership", []byte("0::/other\n"), mount("/")},
		{"v1-memory", []byte("0::/worker\n5:memory:/worker\n"), mount("/")},
		{"ambiguous-mount", membership, append(mount("/"), mount("/")...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := memoryCgroupV2Headroom(t.Context(), tc.member, tc.mounts); err == nil {
				t.Fatal("unsafe scope was accepted")
			}
		})
	}
	if err := os.WriteFile(filepath.Join(child, "memory.max"), []byte("500\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, usage := range []string{"", "max", "18446744073709551615", "invalid"} {
		if err := os.WriteFile(filepath.Join(child, "memory.current"), []byte(usage), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := memoryCgroupV2Headroom(t.Context(), membership, mount("/")); err == nil {
			t.Fatalf("invalid usage %q was accepted", usage)
		}
	}
}

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

func TestNativeLinuxMemoryHiddenAncestryRefusesAdmissionCredit(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_MEMORY_HIDDEN_SCOPE") != "1" {
		t.Skip("requires an actual non-initial cgroup namespace")
	}
	r := MemoryAvailability(t.Context())
	if r.Available != nil || r.Total != nil || r.Quality != "unavailable" || r.Scope != "memory-cgroup-unknown" ||
		!strings.Contains(r.Detail, "initial cgroup namespace") {
		t.Fatalf("hidden cgroup ancestry supplied memory credit or hid the reason: %+v", r)
	}
	t.Logf("native hidden scope refused: scope=%q detail=%q", r.Scope, r.Detail)
}

func TestNativeLinuxMemoryLimitedScopeCapsHostHeadroom(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_MEMORY_LIMITED_SCOPE") != "1" {
		t.Skip("requires an actual finite memory cgroup in the initial namespace")
	}
	capacity, _, limited, err := linuxMemoryCgroupHeadroom(t.Context())
	if err != nil || !limited || capacity <= 0 {
		t.Fatalf("finite complete native cgroup scope unavailable: capacity=%d limited=%t err=%v", capacity, limited, err)
	}
	r := MemoryAvailability(t.Context())
	if r.Total == nil || r.Available == nil || r.Quality != "estimated" || r.Scope != "host+complete-memory-cgroup-ancestry" ||
		*r.Total <= 0 || *r.Total > capacity || *r.Available < 0 || *r.Available > *r.Total {
		t.Fatalf("finite native cgroup allowance was not applied: capacity=%d reading=%+v", capacity, r)
	}
	values := memoryAvailabilityValues(r)
	if values.Used.Value != nil || values.Available.Value == nil {
		t.Fatalf("native cgroup headroom invented resident usage: %+v", values)
	}
	t.Logf("native finite scope: ancestor_capacity=%d total=%d available=%d scope=%q", capacity, *r.Total, *r.Available, r.Scope)
}
