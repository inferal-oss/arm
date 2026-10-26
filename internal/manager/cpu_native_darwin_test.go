package manager

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/usage"
)

// Opt-in actual native integration, bounded to one CPU for eight seconds.
// The parent uses ordinary Manager admission/sampling/renewal/settlement; it
// never seeds a native observation, credit, process identity or grant result.
func TestNativeManagerAlignedCPUUsageCredit(t *testing.T) {
	if os.Getenv("ARM_TEST_NATIVE_MANAGER_CREDIT") != "1" {
		t.Skip("requires explicitly enabled bounded native CPU credit test")
	}
	if os.Getenv("ARM_NATIVE_MANAGER_CREDIT_HELPER") == "1" {
		runtime.GOMAXPROCS(1)
		deadline := time.Now().Add(8 * time.Second)
		var n uint64
		for time.Now().Before(deadline) {
			for range 10000 {
				n = n*1664525 + 1013904223
			}
		}
		runtime.KeepAlive(n)
		return
	}
	if _, _, err := usage.CPUClockQuantum(); err != nil {
		t.Fatal("native accounting scope unsupported", err)
	}
	m, err := Open(filepath.Join(t.TempDir(), "arm"), model.Resources{CPU: float64(runtime.NumCPU())})
	if err != nil {
		t.Fatal(err)
	}
	m.Start()
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error("original Manager close", err)
		}
	})
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := model.Request{Host: "localhost", Key: "native-aligned-manager-credit", Reason: "bounded native managed CPU credit acceptance", Resources: model.Resources{CPU: 1}, TTL: 30 * time.Second}
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/usr/bin/env", "ARM_NATIVE_MANAGER_CREDIT_HELPER=1", "ARM_TEST_NATIVE_MANAGER_CREDIT=1", path, "-test.run=^TestNativeManagerAlignedCPUUsageCredit$", "-test.timeout=12s"}, CWD: filepath.Dir(m.home)})
	if err != nil {
		t.Fatal("actual idle headroom did not admit the one-core fixture", err)
	}
	m.mu.Lock()
	original := m.jobs[l.ID]
	m.mu.Unlock()
	if original == nil || original.cpuAnchor == "" {
		t.Fatal("original launch anchor unavailable")
	}
	deadline := time.Now().Add(6500 * time.Millisecond)
	var observed *model.CPUBudget
	for time.Now().Before(deadline) {
		s, err := m.Status()
		if err != nil {
			t.Fatal(err)
		}
		if s.CPU != nil && s.CPU.Known && s.CPU.ManagedCredit > .2 {
			observed = s.CPU
			break
		}
		select {
		case <-original.done:
			t.Fatal("original helper ended before credit was observed")
		default:
		}
		time.Sleep(100 * time.Millisecond)
	}
	if observed == nil {
		s, _ := m.Status()
		t.Fatal("normal native sampling never credited owned work", s.CPU)
	}
	if observed.Reserved != 1 || observed.ManagedCredit > 1 || observed.Observation.Busy == nil || observed.ManagedCredit > *observed.Observation.Busy {
		t.Fatal("native credit escaped its promise/host bounds", observed)
	}
	t.Logf("native headroom=%.3f credited=%.3f pool=%.3f promised=%.3f available=%.3f", *observed.Observation.Available, observed.ManagedCredit, observed.Capacity, observed.Reserved, observed.Available)
	if _, err := m.Renew(l.ID, time.Minute); err != nil {
		t.Fatal("native renewal failed", err)
	}
	select {
	case <-original.done:
	case <-time.After(7 * time.Second):
		t.Fatal("original bounded helper did not join")
	}
	settled, err := m.Inspect(l.ID)
	if err != nil || settled.State != "terminal" || settled.ExitCode == nil || *settled.ExitCode != 0 {
		t.Fatal("original helper did not settle successfully", settled, err)
	}
	s, err := m.Status()
	if err != nil || s.CPU.Reserved != 0 || s.CPU.ManagedCredit != 0 {
		t.Fatal("retired native job left promises or credit", s.CPU, err)
	}
}
