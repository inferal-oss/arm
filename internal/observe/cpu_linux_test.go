package observe

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxCPUAdmissionExcludesIOWaitAndSteal(t *testing.T) {
	if _, e := parseAdmissionCPU([]byte("cpu0 1 2 3 4\n")); e == nil {
		t.Fatal("missing interrupt/steal counters silently became zero")
	}
	// guest fields are already included in user/nice and must not be added.
	rows, e := parseCPU([]byte("cpu0 10 0 10 50 20 0 0 10 999 999\n"), cpuObservationMaxCPUs)
	if e != nil {
		t.Fatal(e)
	}
	c := rows[0]
	c.idle -= c.parts[4]
	c.busy += c.parts[4]
	if c.idle != 50 || c.busy != 50 {
		t.Fatal(c)
	}
	for _, text := range []string{"cpu0 1 1 1 1\ncpu0 2 2 2 2\n", "cpu0 1 1 1 1\ncpu1 invalid\n"} {
		rows, e := parseCPU([]byte(text), cpuObservationMaxCPUs)
		if e == nil && validateCPURows(rows) == nil {
			t.Fatal("partial/duplicate inventory accepted", text)
		}
	}
	var big strings.Builder
	for i := 0; i < monitorCPUs+1; i++ {
		fmt.Fprintf(&big, "cpu%d 1 1 1 1 1 1 1 1\n", i)
	}
	if rows, e := parseCPU([]byte(big.String()), cpuObservationMaxCPUs); e != nil || len(rows) != monitorCPUs+1 {
		t.Fatal("admission reused display truncation", e)
	}
	if _, e := parseMonitorCPU([]byte(big.String())); e == nil {
		t.Fatal("display cap changed")
	}
}
func TestLinuxCPUQuotaParsers(t *testing.T) {
	for _, tc := range []struct {
		s        string
		v2       bool
		capacity float64
		finite   bool
	}{{"max 100000", true, 0, false}, {"50000 100000", true, .5, true}, {"-1 100000", false, 0, false}, {"200000 100000", false, 2, true}} {
		c, f, e := parseCPUQuota([]byte(tc.s), tc.v2)
		if e != nil || c != tc.capacity || f != tc.finite {
			t.Fatal(tc, c, f, e)
		}
	}
	for _, s := range []string{"", "max 0", "0 100", "-1 100", "10 0", "10 100 extra", "NaN 100"} {
		if _, _, e := parseCPUQuota([]byte(s), true); e == nil {
			t.Fatal("bad quota accepted", s)
		}
	}
	if n, e := parseCPUQuotaUsage([]byte("usage_usec 100\nuser_usec 90\nsystem_usec 10\n"), true); e != nil || n != 100000 {
		t.Fatal(n, e)
	}
	for _, s := range []string{"user_usec 1", "usage_usec 1\nusage_usec 2", "usage_usec -1", "usage_usec 18446744073709551615"} {
		if _, e := parseCPUQuotaUsage([]byte(s), true); e == nil {
			t.Fatal("bad ancestor usage accepted", s)
		}
	}
}
func TestLinuxCPUAncestorQuotaScopeAndFailures(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "leaf")
	if e := os.Mkdir(child, 0700); e != nil {
		t.Fatal(e)
	}
	write := func(dir, name, value string) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(root, "cpu.max", "50000 100000")
	write(root, "cpu.stat", "usage_usec 200000")
	write(child, "cpu.max", "max 100000")
	members := []byte("0::/leaf\n")
	mounts := []byte(fmt.Sprintf("1 2 0:1 / %s rw - cgroup2 cgroup rw\n", root))
	q, sig, e := linuxCPUQuotas(t.Context(), members, mounts)
	if e != nil || len(q) != 1 || q[0].capacity != .5 || q[0].used != 200000000 || sig == "" {
		t.Fatal(q, sig, e)
	}
	if q[0].started.IsZero() || q[0].ended.Before(q[0].started) {
		t.Fatal("actual ancestor read bounds absent")
	}
	write(child, "cpu.max", "25000 100000")
	write(child, "cpu.stat", "usage_usec 100000")
	if q, _, e := linuxCPUQuotas(t.Context(), members, mounts); e != nil || len(q) != 2 {
		t.Fatal(q, e)
	}
	if e := os.Remove(filepath.Join(root, "cpu.stat")); e != nil {
		t.Fatal(e)
	}
	if _, _, e := linuxCPUQuotas(t.Context(), members, mounts); e == nil {
		t.Fatal("unreadable finite ancestor silently unrestricted")
	}
	write(root, "cpu.stat", "usage_usec 1\nusage_usec 2")
	if _, _, e := linuxCPUQuotas(t.Context(), members, mounts); e == nil {
		t.Fatal("malformed finite ancestor silently unrestricted")
	}
	if _, _, e := linuxCPUQuotas(t.Context(), []byte("0::/leaf\n0::/other\n"), mounts); e == nil {
		t.Fatal("duplicate controller accepted")
	}
	if _, _, e := linuxCPUQuotas(t.Context(), []byte("0::/outside\n"), []byte(fmt.Sprintf("1 2 0:1 /hidden %s rw - cgroup2 cgroup rw\n", root))); e == nil {
		t.Fatal("unmapped hierarchy accepted")
	}
	v1 := []byte(fmt.Sprintf("1 2 0:1 / %s rw - cgroup cgroup rw,cpu\n", root))
	write(child, "cpu.cfs_quota_us", "50000")
	write(child, "cpu.cfs_period_us", "100000")
	if _, _, e := linuxCPUQuotas(t.Context(), []byte("2:cpu:/leaf\n"), v1); e == nil {
		t.Fatal("finite v1 quota without matching ancestor accounting accepted")
	}
}
func TestNativeLinuxCPUUniformAffinity(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_CPU") == "" {
		t.Skip("enable bounded native CPU observation")
	}
	ids, e := linuxCPUAffinity(t.Context())
	if e != nil || len(ids) == 0 {
		t.Fatal(ids, e)
	}
	s := NewCPUSampler()
	s.Sample(t.Context())
	time.Sleep(200 * time.Millisecond)
	r := s.Sample(t.Context())
	if !r.Known || r.EligibleCPUs == nil || *r.EligibleCPUs != len(ids) {
		t.Fatalf("native affinity/cgroup observation unavailable: %+v", r)
	}
	t.Logf("actual eligible IDs=%v; capacity=%g; available=%g", ids, *r.Capacity, *r.Available)
}

// A fresh exec inherits one actual kernel affinity mask before Go creates its
// threads. Only that child runs the short busy interval, never the host pool.
func TestNativeLinuxCPUSingleAffinityChild(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_CPU") == "" {
		t.Skip("enable bounded native CPU observation")
	}
	if os.Getenv("ARM_CPU_SINGLE_CHILD") == "1" {
		ids, e := linuxCPUAffinity(t.Context())
		if e != nil || len(ids) != 1 {
			t.Fatal("child is not limited to one real CPU", ids, e)
		}
		s := NewCPUSampler()
		s.Sample(t.Context())
		time.Sleep(150 * time.Millisecond)
		r := s.Sample(t.Context())
		if !r.Known || *r.EligibleCPUs != 1 || *r.Capacity > 1 {
			t.Fatalf("limited child observation unavailable: %+v", r)
		}
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				default:
				}
				for i := 0; i < 100000; i++ {
				}
			}
		}()
		time.Sleep(350 * time.Millisecond)
		loaded := s.Sample(t.Context())
		close(stop)
		<-done
		if !loaded.Known || *loaded.Busy < .5 || *loaded.Available > .5 {
			t.Fatalf("actual one-CPU work did not consume headroom: %+v", loaded)
		}
		t.Logf("kernel one-CPU affinity=%v idle-before=%.3f idle-during=%.3f busy-during=%.3f", ids, *r.Available, *loaded.Available, *loaded.Busy)
		return
	}
	ids, e := linuxCPUAffinity(t.Context())
	if e != nil || len(ids) == 0 {
		t.Fatal(ids, e)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "taskset", "-c", strconv.Itoa(ids[0]), exe, "-test.run=^TestNativeLinuxCPUSingleAffinityChild$", "-test.v")
	cmd.Env = append(os.Environ(), "ARM_CPU_SINGLE_CHILD=1", "GOMAXPROCS=2")
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("original affinity child failed: %v\n%s", e, out)
	}
	t.Log(string(out))
}

func TestNativeLinuxCPUQuotaChild(t *testing.T) {
	if os.Getenv("ARM_CPU_QUOTA_CHILD") != "1" {
		t.Skip("requires an existing delegated transient user CPU quota scope")
	}
	scope, e := linuxCPUCurrentScope(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if math.Abs(scope.capacity-.5) > 1e-9 || len(scope.quotas) == 0 {
		t.Fatalf("kernel did not apply the private half-core quota: %+v", scope)
	}
	s := NewCPUSampler()
	s.Sample(t.Context())
	time.Sleep(150 * time.Millisecond)
	before := s.Sample(t.Context())
	if !before.Known || *before.Capacity != .5 {
		t.Fatalf("actual quota observation unavailable: %+v", before)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for i := 0; i < 100000; i++ {
			}
		}
	}()
	time.Sleep(350 * time.Millisecond)
	loaded := s.Sample(t.Context())
	close(stop)
	<-done
	if !loaded.Known || *loaded.Capacity != .5 || *loaded.Available > .2 {
		t.Fatalf("actual quota consumption did not reduce available capacity: %+v", loaded)
	}
	after, e := linuxCPUCurrentScope(t.Context())
	if e != nil || len(after.quotas) != len(scope.quotas) {
		t.Fatal("native quota scope changed", e)
	}
	progress := false
	for i, q := range after.quotas {
		if q.used > scope.quotas[i].used {
			progress = true
		}
	}
	if !progress {
		t.Fatal("actual ancestor CPU usage did not progress")
	}
	t.Logf("actual finite ancestor quota=%.3f native idle-before=%.3f idle-during=%.3f, eligible CPU count=%d", *loaded.Capacity, *before.Available, *loaded.Available, *loaded.EligibleCPUs)
}

func TestLinuxCPUHiddenAncestorNamespaceRefused(t *testing.T) {
	if sameCPUThreadMembership([]byte("0::/parent\n"), []byte("0::/parent/child\n")) {
		t.Fatal("equal-affinity threads with different CPU cgroups were accepted")
	}
	valid, err := cpuQuotaNamespaceIdentity(4, initialCgroupNamespaceInode, unix.NSFS_MAGIC, unix.CLONE_NEWCGROUP)
	if err != nil || valid == "" {
		t.Fatal("initial kernel namespace was rejected", valid, err)
	}
	// A namespace-local PID1 and '/' mount root cannot establish that a visible
	// one-core limit has no hidden half-core parent. Only the kernel initial
	// namespace identity closes that ancestry proof.
	for _, case_ := range []struct {
		name       string
		inode      uint64
		filesystem int64
		kind       int
	}{
		{"nested", initialCgroupNamespaceInode + 1, unix.NSFS_MAGIC, unix.CLONE_NEWCGROUP},
		{"ordinary-file", initialCgroupNamespaceInode, 0x01021994, unix.CLONE_NEWCGROUP},
		{"wrong-kind", initialCgroupNamespaceInode, unix.NSFS_MAGIC, unix.CLONE_NEWPID},
	} {
		t.Run(case_.name, func(t *testing.T) {
			if _, err := cpuQuotaNamespaceIdentity(4, case_.inode, case_.filesystem, case_.kind); err == nil {
				t.Fatal("unverified hidden quota ancestry became eligible")
			}
			_, proofErr := cpuQuotaNamespaceIdentity(4, case_.inode, case_.filesystem, case_.kind)
			at := time.Now()
			sampler := &CPUSampler{previous: cpuFixture(at.Add(-time.Second), 0, 100), collect: func(context.Context) (cpuAvailabilityRaw, error) { return cpuFixture(at, 0, 200), proofErr }}
			if r := sampler.Sample(t.Context()); r.Known || r.Available != nil {
				t.Fatal("apparently idle visible one-core quota bypassed failed ancestry proof", r)
			}
		})
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cpu.max"), []byte("100000 100000"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := linuxCPUQuotaNamespaceFile(filepath.Join(root, "cpu.max")); err == nil {
		t.Fatal("ordinary file was accepted as a real cgroup namespace FD")
	}
	// Even the initial cgroup namespace cannot use a narrower bind mount that
	// hides a parent; it must walk a CPU hierarchy mounted at its real root.
	mounts := []byte(fmt.Sprintf("1 2 0:1 /hidden %s rw - cgroup2 cgroup rw\n", root))
	if _, _, err := linuxCPUQuotas(t.Context(), []byte("0::/hidden\n"), mounts); err == nil {
		t.Fatal("subtree mount omitted an unobserved ancestor")
	}
}

func TestNativeLinuxCPUQuotaNamespaceBoundary(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_CPU") != "1" {
		t.Skip("enable required native CPU namespace observation")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	identity, proofErr := linuxCPUQuotaNamespace()
	s := NewCPUSampler()
	if proofErr != nil {
		first := s.Sample(t.Context())
		time.Sleep(150 * time.Millisecond)
		after := s.Sample(t.Context())
		if first.Known || first.Available != nil || after.Known || after.Available != nil {
			t.Fatal("unverified native namespace invented CPU availability", first, after)
		}
		t.Logf("actual CPU quota ancestry unavailable; native sampler correctly remains unknown: %v", proofErr)
		return
	}
	if identity == "" {
		t.Fatal("empty successful native namespace proof")
	}

	s.Sample(t.Context())
	time.Sleep(150 * time.Millisecond)
	r := s.Sample(t.Context())
	if !r.Known || r.Available == nil {
		t.Fatalf("native complete-ancestry CPU pair unavailable: %+v", r)
	}
	t.Logf("actual initial cgroup namespace %s, CPU scope %s, headroom %.3f", identity, r.Scope, *r.Available)
}
