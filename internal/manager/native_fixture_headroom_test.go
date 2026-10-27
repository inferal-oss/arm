package manager

import (
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

// Sample real headroom without starting scheduling: these fixtures explicitly
// drive expiry and must not acquire a concurrent tick or synthetic CPU reading.
func awaitNativeFixtureHeadroom(t *testing.T, m *Manager, requested model.Resources) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if requested.CPU > 0 {
			m.sampleCPU()
		}
		if requested.Memory > 0 {
			m.sampleMemory()
		}
		status, err := m.Status()
		if err != nil {
			t.Fatal(err)
		}
		cpuReady := requested.CPU == 0 || status.CPU != nil && status.CPU.Known && !status.CPU.Stale && status.CPU.Available >= requested.CPU
		memoryReady := requested.Memory == 0 || status.Memory != nil && !status.Memory.Stale && status.Memory.Available >= requested.Memory
		if cpuReady && memoryReady {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("native fixture headroom unavailable for %+v: CPU=%+v memory=%+v", requested, status.CPU, status.Memory)
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-t.Context().Done():
			timer.Stop()
			t.Fatal(t.Context().Err())
		case <-timer.C:
		}
	}
}
