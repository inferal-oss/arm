package observe

import (
	"errors"
	"testing"
	"time"

	"getarm.org/internal/model"
)

func TestPressureThresholds(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{{0, model.PressureNormal}, {1.25, model.PressureElevated}, {2.5, model.PressureHigh}, {16.7, model.PressureCritical}} {
		if got := loadThreshold.level(c.v); got != c.want {
			t.Fatalf("load %v: got %s want %s", c.v, got, c.want)
		}
	}
	if got := memoryAvailableLow.inverted(3); got != model.PressureCritical {
		t.Fatalf("inverted: %s", got)
	}
}

func TestParsePSI(t *testing.T) {
	psi := map[string]psiLine{}
	data := "some avg10=12.50 avg60=3.00 avg300=1.00 total=123456\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=0\n"
	if err := parsePSI("memory", []byte(data), psi); err != nil {
		t.Fatal(err)
	}
	if psi["memory.some"].avg10 != 12.5 || psi["memory.some"].total != 123456 || psi["memory.full"].avg10 != 0 {
		t.Fatalf("parsed %+v", psi)
	}
	for _, bad := range []string{"some avg10=x avg60=0 avg300=0 total=0", "some avg10=101 avg60=0 avg300=0 total=0", "other avg10=0 avg60=0 avg300=0 total=0", "some avg10=0"} {
		if err := parsePSI("cpu", []byte(bad), map[string]psiLine{}); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func pressureFixture(at time.Time) monitorRaw {
	return monitorRaw{at: at, cpu: make([]cpuCounter, 4), memory: memoryValues(100, 50, "host", "test", at), swap: memoryValues(0, 0, "host-swap", "test", at)}
}

func TestPressureViewCPUFromLoad(t *testing.T) {
	now := time.Now()
	raw := pressureFixture(now)
	raw.pressure = pressureRaw{load: [3]float64{67, 0, 0}, loadMethod: "test"}
	v := pressureView(raw, pressureFixture(now.Add(-2*time.Second)))
	if v.CPU.Level != model.PressureCritical || v.CPU.Reason == "" {
		t.Fatalf("cpu %+v", v.CPU)
	}
	// Linux without PSI falls back to RAM availability; no swap configured.
	if v.Memory.Level != model.PressureNormal {
		t.Fatalf("memory %+v", v.Memory)
	}
	if v.IO.Level != model.PressureUnknown {
		t.Fatalf("io %+v", v.IO)
	}
}

func TestPressureViewUnknownIsNotNormal(t *testing.T) {
	raw := monitorRaw{at: time.Now(), pressure: pressureRaw{loadErr: errors.New("no load"), loadMethod: "test"}}
	v := pressureView(raw, monitorRaw{})
	if v.CPU.Level != model.PressureUnknown || v.Memory.Level != model.PressureUnknown || v.IO.Level != model.PressureUnknown {
		t.Fatalf("unknown evidence produced levels %+v", v)
	}
	if pressurePoint(v.CPU.Level) != nil {
		t.Fatal("unknown level produced a history rank")
	}
}

func TestPressureViewPSIOverridesLoad(t *testing.T) {
	now := time.Now()
	raw := pressureFixture(now)
	raw.pressure = pressureRaw{load: [3]float64{40, 0, 0}, loadMethod: "test", psi: map[string]psiLine{"cpu.some": {avg10: 10}, "memory.some": {avg10: 20}, "memory.full": {avg10: 2}, "io.some": {}, "io.full": {avg10: 31}}}
	v := pressureView(raw, pressureFixture(now.Add(-2*time.Second)))
	if v.CPU.Level != model.PressureNormal {
		t.Fatalf("load overrode PSI: %+v", v.CPU)
	}
	if v.Memory.Level != model.PressureHigh || v.IO.Level != model.PressureCritical {
		t.Fatalf("memory %s io %s", v.Memory.Level, v.IO.Level)
	}
}

func TestPressureSwapRateAndDiskQueue(t *testing.T) {
	now := time.Now()
	prev, raw := pressureFixture(now.Add(-2*time.Second)), pressureFixture(now)
	prev.pressure = pressureRaw{vmMethod: "test", pageSize: 16384, vm: [4]uint64{0, 1000, 1000, 0}, memoryMethod: "test", memLevel: 1}
	raw.pressure = pressureRaw{vmMethod: "test", pageSize: 16384, vm: [4]uint64{0, 1000 + 4096, 1000 + 4096, 0}, memoryMethod: "test", memLevel: 1}
	prev.disk = []ioCounter{{id: "d", name: "disk", at: prev.at, busy: 0, timed: true}}
	raw.disk = []ioCounter{{id: "d", name: "disk", at: raw.at, busy: uint64(20 * time.Second), timed: true}}
	v := pressureView(raw, prev)
	// 8192 pages * 16 KiB / 2s = 64 MiB/s.
	if v.Memory.Level != model.PressureHigh {
		t.Fatalf("memory %+v", v.Memory)
	}
	if v.IO.Level != model.PressureHigh {
		t.Fatalf("io %+v", v.IO)
	}
	// A counter reset is unknown, not zero traffic.
	raw.pressure.vm[1] = 0
	if _, ok := pageRate(raw.pressure, prev.pressure, 2*time.Second); ok {
		t.Fatal("regressed counter produced a rate")
	}
}

func TestMemoryStatusLevels(t *testing.T) {
	for level, want := range map[int]string{1: model.PressureNormal, 2: model.PressureElevated, 4: model.PressureCritical} {
		raw := pressureFixture(time.Now())
		raw.pressure = pressureRaw{memoryMethod: "test", memLevel: level}
		if got := pressureView(raw, monitorRaw{}).Memory.Level; got != want {
			t.Fatalf("level %d: %s", level, got)
		}
	}
}

func TestPressureReasonsShowValuesAndBoundaries(t *testing.T) {
	now := time.Now()
	raw := pressureFixture(now)
	raw.pressure = pressureRaw{load: [3]float64{67, 0, 0}, loadMethod: "test", memoryMethod: "test", memLevel: 2}
	raw.swap = memoryValues(1000, 140, "host-swap", "test", now)
	v := pressureView(raw, pressureFixture(now.Add(-2*time.Second)))
	if v.CPU.Reason != "Run queue per CPU (1m load) 16.7 (critical at ≥4)" {
		t.Fatalf("cpu reason %q", v.CPU.Reason)
	}
	if got := pressureNumber(5.4, "percent") + " " + pressureNumber(1.256, "ratio") + " " + pressureNumber(4, "MiB/second"); got != "5.4% 1.26 4 MiB/s" {
		t.Fatal("numbers", got)
	}
	if v.Memory.Level != model.PressureElevated || v.Memory.Reason != "Kernel memory pressure warn; Swap remaining 14% (elevated at ≤20%)" {
		t.Fatalf("memory reason %q", v.Memory.Reason)
	}
	// Exhausted swap is capped at elevated and says so.
	raw.swap = memoryValues(1000, 30, "host-swap", "test", now)
	raw.pressure.memLevel = 1
	if v := pressureView(raw, pressureFixture(now.Add(-2*time.Second))); v.Memory.Reason != "Swap remaining 3% (elevated at ≤20%)" {
		t.Fatalf("capped swap reason %q", v.Memory.Reason)
	}
	// With PSI, load is forced normal: no threshold and not a reason.
	raw.pressure.psi = map[string]psiLine{"cpu.some": {avg10: 30}}
	v = pressureView(raw, pressureFixture(now.Add(-2*time.Second)))
	for _, s := range v.CPU.Signals {
		if s.ID == "load.per-cpu" && (s.Level != model.PressureNormal || s.Threshold != "") {
			t.Fatal("forced-normal load kept a threshold", s)
		}
	}
	if v.CPU.Reason != "CPU stall (some tasks, 10s) 30% (elevated at ≥25%)" {
		t.Fatalf("psi reason %q", v.CPU.Reason)
	}
	// Rounding never shows a value on the next level's boundary.
	if got := pressureReason(memoryAvailableLow.bound(pressureSignal("memory.available", "RAM available", 8.004, "percent", "test", model.PressureElevated, now), true)); got != "RAM available 8.01% (elevated at ≤15%)" {
		t.Fatal(got)
	}
	if got := pressureReason(loadThreshold.bound(pressureSignal("load.per-cpu", "Load", 1.996, "ratio", "test", model.PressureElevated, now), false)); got != "Load 1.99 (elevated at ≥1.25)" {
		t.Fatal(got)
	}
}
