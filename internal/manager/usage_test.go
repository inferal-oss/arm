package manager

import (
	"os"
	"testing"
	"time"
)

func TestSupervisedUsageHasOwnedIdentityFreshnessAndFinalSummary(t *testing.T) {
	m := newManager(t)
	m.Start()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	req := request("usage-workload")
	req.TTL = 20 * time.Second
	run := RunRequest{Request: req, Command: []string{exe, "-test.run=^TestManagerUsageWorkloadChild$"}, CWD: t.TempDir()}
	lease, err := m.Acquire(req, &run)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Process == nil || lease.Process.PID != lease.PID || lease.Process.PGID != lease.PID {
		t.Fatalf("launch identity missing: %+v", lease)
	}
	eventually(t, func() bool {
		v, e := m.Usage(lease.ID)
		return e == nil && v.Usage != nil && v.Usage.CPUCores != nil && v.Usage.AccountedMemoryBytes != nil
	})
	v, err := m.Usage(lease.ID)
	if err != nil || !v.Usage.IdentityVerified || !v.Usage.Live || v.Usage.Stale || *v.Usage.CPUCores <= 0 {
		t.Fatalf("bad runtime observation: %+v %v", v, err)
	}
	m.mu.Lock()
	sample := m.usages[lease.ID]
	sample.observed = time.Now().Add(-10 * time.Second)
	m.usages[lease.ID] = sample
	m.mu.Unlock()
	v, _ = m.Usage(lease.ID)
	if !v.Usage.Stale {
		t.Fatal("old observation presented as fresh")
	}
	if _, err = m.End(lease.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { l, _ := m.Inspect(lease.ID); return l.State == "terminal" })
	l, err := m.Inspect(lease.ID)
	if err != nil || l.Usage == nil || l.Usage.Live || len(l.Usage.Members) != 0 || l.Usage.MemberCount == 0 {
		t.Fatalf("historical usage summary missing or live: %+v %v", l.Usage, err)
	}
	m.mu.Lock()
	_, retained := m.usages[lease.ID]
	m.mu.Unlock()
	if retained {
		t.Fatal("sampler retained a terminal workload")
	}
}
func TestManagerUsageWorkloadChild(t *testing.T) {
	// ARM launches this fixture through its normal supervised gate; only the
	// explicitly selected test reaches this body in the dedicated child.
	if len(os.Args) < 2 || os.Args[1] != "-test.run=^TestManagerUsageWorkloadChild$" {
		t.Skip("supervised subprocess fixture")
	}
	memory := make([]byte, 24<<20)
	for i := 0; i < len(memory); i += 4096 {
		memory[i] = 1
	}
	until := time.Now().Add(15 * time.Second)
	n := uint64(1)
	for time.Now().Before(until) {
		for range 100000 {
			n = n*1664525 + 1013904223
		}
		if n == 0 {
			memory[0]++
		}
	}
	t.Log(len(memory), n)
}
func TestUnsupervisedLeaseDoesNotInventProcessUsage(t *testing.T) {
	m := newManager(t)
	l, err := m.Acquire(request("no-process"), nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := m.Usage(l.ID)
	if err != nil || v.Usage != nil || v.Detail == "" {
		t.Fatalf("invented usage: %+v %v", v, err)
	}
}
