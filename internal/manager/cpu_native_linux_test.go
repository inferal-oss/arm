package manager

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/observe"
	"golang.org/x/sys/unix"
)

// Opt-in only: the runner must already have placed this original process in a
// transient half-core quota and one-CPU kernel affinity scope. This test never
// changes host affinity, cgroups, services or any other process's settings.
// The sampler must also prove the initial, complete cgroup hierarchy; a visible
// nested quota alone does not establish this positive test's preconditions.
func TestNativeManagerCPUExternalPressureQueuesAndRecoversFIFO(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_MANAGER_CPU") != "1" {
		t.Skip("requires an explicitly bounded native Linux CPU scope")
	}
	mask := unix.NewCPUSet(8192)
	if err := unix.SchedGetaffinityDynamic(0, mask); err != nil || mask.Count() != 1 {
		t.Fatalf("refusing native load without actual one-CPU affinity: count=%d err=%v", mask.Count(), err)
	}
	inventory := observe.NewCPUSampler().Sample(t.Context())
	if inventory.EligibleCPUs == nil || *inventory.EligibleCPUs != 1 || inventory.Capacity == nil || math.Abs(*inventory.Capacity-.5) > 1e-9 {
		t.Fatalf("refusing native load without a verified half-core quota: %+v", inventory)
	}
	if os.Getenv("ARM_NATIVE_MANAGER_CPU_HELPER") == "1" {
		ready := os.Getenv("ARM_NATIVE_MANAGER_CPU_READY")
		if !filepath.IsAbs(ready) {
			t.Fatal("helper requires its original private readiness marker")
		}
		if err := os.WriteFile(ready, []byte("original-helper-ready\n"), 0600); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(4500 * time.Millisecond)
		var n uint64
		for time.Now().Before(deadline) {
			for range 10000 {
				n = n*1664525 + 1013904223
			}
		}
		runtime.KeepAlive(n)
		return
	}

	m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	m.Start()
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error("original Manager close", err)
		}
	})
	status := func() model.Status {
		t.Helper()
		s, err := m.Status()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	wait := func(label string, limit time.Duration, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(limit)
		for time.Now().Before(deadline) {
			if predicate() {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("%s did not occur; native status=%+v", label, status().CPU)
	}
	wait("fresh initial idle headroom", 8*time.Second, func() bool {
		s := status()
		return s.CPU != nil && s.CPU.Known && !s.CPU.Stale && s.CPU.Capacity >= .4
	})
	initial := status()
	if len(initial.Leases) != 0 || initial.Reserved.CPU != 0 {
		t.Fatal("fixture was not initially free of ARM promises", initial.Reserved, initial.Leases)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "original-helper-ready")
	cmd := exec.Command(executable, "-test.run=^TestNativeManagerCPUExternalPressureQueuesAndRecoversFIFO$", "-test.timeout=8s")
	cmd.Env = append(os.Environ(), "ARM_NATIVE_MANAGER_CPU_HELPER=1", "ARM_NATIVE_MANAGER_CPU_READY="+ready, "GOMAXPROCS=2")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	joined := make(chan struct{})
	var helperErr error
	go func() { helperErr = cmd.Wait(); close(joined) }()
	t.Cleanup(func() {
		// The original helper is fixed to 4.5 seconds, with its own Go test
		// timeout as a second bound. No signal/adoption path is used.
		select {
		case <-joined:
			if helperErr != nil {
				t.Errorf("original helper Wait failed: %v\n%s", helperErr, output.String())
			}
		case <-time.After(10 * time.Second):
			t.Error("original helper join remains unknown; no substitute PID cleanup")
		}
	})
	wait("original helper readiness", 2*time.Second, func() bool {
		data, err := os.ReadFile(ready)
		return err == nil && string(data) == "original-helper-ready\n"
	})
	pressureStarted := time.Now().UTC()
	wait("fresh external CPU pressure", 3500*time.Millisecond, func() bool {
		s := status()
		return s.CPU != nil && s.CPU.Known && !s.CPU.Stale && s.CPU.Observation.At.After(pressureStarted) && s.CPU.Capacity < .05
	})
	select {
	case <-joined:
		t.Fatal("external helper ended before admission checks", helperErr, output.String())
	default:
	}
	pressured := status()
	if len(pressured.Leases) != 0 || pressured.Reserved.CPU != 0 {
		t.Fatal("native pressure was confused with an ARM promise", pressured.Leases, pressured.Reserved)
	}
	q := model.Request{Host: "localhost", Key: "native-direct-refusal", Reason: "native external CPU pressure must block unreserved work", Resources: model.Resources{CPU: .25}, TTL: time.Minute}
	if _, err := m.Acquire(q, nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("real external pressure did not refuse the direct CPU claim", err)
	}
	q.Key, q.Queue = "native-fifo-head", true
	head, err := m.Acquire(q, nil)
	if err != nil || head.State != "queued" {
		t.Fatal("head was not queued under measured external pressure", head, err)
	}
	q.Key, q.Resources.CPU = "native-fifo-tail", .1
	tail, err := m.Acquire(q, nil)
	if err != nil || tail.State != "queued" {
		t.Fatal("tail bypassed the native-pressure queue", tail, err)
	}
	wait("native CPU blocker and FIFO explanation", time.Second, func() bool {
		h, e1 := m.Inspect(head.ID)
		v, e2 := m.Inspect(tail.ID)
		if e1 != nil || e2 != nil || h.State != "queued" || v.State != "queued" || h.QueueInfo == nil || v.QueueInfo == nil || h.QueueInfo.Position != 1 || v.QueueInfo.Position != 2 {
			return false
		}
		pressure, order := false, false
		for _, b := range h.QueueInfo.Blockers {
			if b.Code == "cpu_headroom" && b.Unit == "logical-core-equivalents" && b.Requested != nil && b.Available != nil {
				requested, e1 := b.Requested.Float64()
				available, e2 := b.Available.Float64()
				pressure = e1 == nil && e2 == nil && requested == .25 && available < requested
			}
		}
		for _, b := range v.QueueInfo.Blockers {
			if b.Code == "queue_order" && len(b.Leases) == 1 && b.Leases[0] == head.ID {
				order = true
			}
		}
		return pressure && order
	})
	if s := status(); s.Reserved.CPU != 0 {
		t.Fatal("queued requests were charged as grants", s.Reserved)
	}
	select {
	case <-joined:
		if helperErr != nil {
			t.Fatalf("original helper did not exit normally: %v\n%s", helperErr, output.String())
		}
	case <-time.After(6 * time.Second):
		t.Fatal("original external work did not join within its fixed bound")
	}
	helperJoinedAt := time.Now().UTC()
	wait("fresh post-helper FIFO recovery", 10*time.Second, func() bool {
		h, e1 := m.Inspect(head.ID)
		v, e2 := m.Inspect(tail.ID)
		s := status()
		return e1 == nil && e2 == nil && h.State == "active" && v.State == "active" && s.CPU != nil && s.CPU.Known && s.CPU.Observation.At.After(helperJoinedAt)
	})
	events, err := m.Events(0)
	if err != nil {
		t.Fatal(err)
	}
	var headGrant, tailGrant int64
	for _, event := range events {
		if event.Type == "lease.granted" {
			switch event.Entity {
			case head.ID:
				headGrant = event.Sequence
			case tail.ID:
				tailGrant = event.Sequence
			}
		}
	}
	if headGrant == 0 || tailGrant <= headGrant {
		t.Fatal("durable native recovery grants did not respect FIFO", headGrant, tailGrant)
	}
	recovered := status()
	if math.Abs(recovered.Reserved.CPU-.35) > 1e-9 || recovered.CPU.Reserved != recovered.Reserved.CPU {
		t.Fatal("native recovery lost outstanding promises", recovered.Reserved, recovered.CPU)
	}
	if _, err := m.Renew(head.ID, 2*time.Minute); err != nil {
		t.Fatal("native renewal failed to count its own promise once", err)
	}
	for _, id := range []string{head.ID, tail.ID} {
		if _, err := m.End(id, "released"); err != nil {
			t.Fatal("ordinary native fixture lease release", err)
		}
	}
	if s := status(); s.Reserved.CPU != 0 {
		t.Fatal("ordinary releases retained CPU promises", s.Reserved)
	}
	t.Logf("actual native pressure %.3f→%.3f→%.3f cores; original helper Wait0; FIFO grant sequences %d<%d; promises0→.35→0, renew self once", initial.CPU.Capacity, pressured.CPU.Capacity, recovered.CPU.Capacity, headGrant, tailGrant)
}

// A real nested cgroup namespace cannot prove its hidden ancestor quota. This
// negative integration case has no load helper, injected reading or test cgroup.
func TestNativeManagerCPUUninspectableNamespaceRefusesAndQueues(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_MANAGER_CPU_NAMESPACE") != "1" {
		t.Skip("enable required native unsupported-namespace admission check")
	}
	f, err := os.Open("/proc/self/ns/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	var fs unix.Statfs_t
	statErr := unix.Fstat(int(f.Fd()), &st)
	fsErr := unix.Fstatfs(int(f.Fd()), &fs)
	kind, kindErr := unix.IoctlRetInt(int(f.Fd()), unix.NS_GET_NSTYPE)
	closeErr := f.Close()
	if statErr != nil || fsErr != nil || kindErr != nil || closeErr != nil || fs.Type != unix.NSFS_MAGIC || kind != unix.CLONE_NEWCGROUP {
		t.Fatal("native namespace handle proof failed", statErr, fsErr, kindErr, closeErr)
	}
	// CGROUP_NS_INIT_INO is reserved by Linux UAPI include/uapi/linux/nsfs.h.
	if st.Ino == 0xEFFFFFFB {
		t.Skip("this case requires a real noninitial cgroup namespace")
	}
	m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	m.Start()
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	s, err := m.Status()
	if err != nil || s.CPU == nil || s.CPU.Known || s.CPU.Observation.Available != nil || s.CPU.Available != 0 || s.Reserved.CPU != 0 || len(s.Leases) != 0 {
		t.Fatal("uninspectable namespace published CPU capacity", s.CPU, s.Reserved, s.Leases, err)
	}
	q := model.Request{Host: "localhost", Key: "native-hidden-ancestor-refusal", Reason: "unknown hidden CPU quota must prevent admission", Resources: model.Resources{CPU: .1}, TTL: time.Minute}
	if _, err := m.Acquire(q, nil); errorCode(err) != "insufficient_capacity" {
		t.Fatal("uninspectable quota admitted a direct CPU claim", err)
	}
	q.Key, q.Queue = "native-hidden-ancestor-queue", true
	waiting, err := m.Acquire(q, nil)
	if err != nil || waiting.State != "queued" {
		t.Fatal("uninspectable quota did not queue CPU work", waiting, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		l, err := m.Inspect(waiting.ID)
		if err != nil || l.State != "queued" {
			t.Fatal("unknown CPU evidence changed the queued promise", l, err)
		}
		if l.QueueInfo != nil {
			for _, b := range l.QueueInfo.Blockers {
				if b.Code == "observation_unavailable" && b.Resource == s.Host+"/cpu" && b.Available == nil {
					found = true
				}
			}
		}
		if found {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !found {
		t.Fatal("unknown native CPU queue lacked its exact observation_unavailable blocker")
	}
	if s, err := m.Status(); err != nil || s.Reserved.CPU != 0 || s.CPU == nil || s.CPU.Known || s.CPU.Available != 0 {
		t.Fatal("uninspectable CPU queue created a CPU promise", s.CPU, s.Reserved, err)
	}
	if _, err := m.End(waiting.ID, "cancelled"); err != nil {
		t.Fatal("ordinary queued fixture cancellation", err)
	}
	t.Logf("actual noninitial cgroup namespace inode=%d: direct CPU refused, queued observation_unavailable, promises0; no load process", st.Ino)
}
