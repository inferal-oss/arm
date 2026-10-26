package observe

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/model"
)

func TestMonitorCounterIntervalsResetsAndGaps(t *testing.T) {
	at := time.Now()
	prior := monitorRaw{at: at, cpu: []cpuCounter{{id: "cpu0", busy: 100, idle: 100}}, disk: []ioCounter{{id: "disk", read: 1 << 60, write: 100, at: at}}}
	next := monitorRaw{at: at.Add(2 * time.Second), cpu: []cpuCounter{{id: "cpu0", busy: 150, idle: 250}}, disk: []ioCounter{{id: "disk", read: (1 << 60) + 512, write: 300, at: at.Add(2 * time.Second)}}}
	v := monitorView(next, prior)
	if v.CPU.Value == nil || *v.CPU.Value != 25 || v.DiskIO[0].Read.Value == nil || *v.DiskIO[0].Read.Value != 256 || *v.DiskIO[0].Write.Value != 100 {
		t.Fatalf("incorrect interval or lossy cumulative-counter conversion: %+v", v)
	}
	prior.cpu[0].parts[0] = 100
	next.cpu[0].parts[0] = 99
	if got := monitorView(next, prior); got.CPU.Value != nil {
		t.Fatal("a component reset hidden by rising aggregate counters became CPU use")
	}
	next.cpu[0].parts[0] = 100
	next.disk[0].read = 0
	v = monitorView(next, prior)
	if v.DiskIO[0].Read.Value != nil || v.DiskIO[0].Write.Value == nil {
		t.Fatal("reset was zero traffic or destroyed independent write evidence")
	}
	for _, change := range []string{"first", "gap", "failure", "replaced", "zero-interval"} {
		p, n := prior, next
		switch change {
		case "first":
			p = monitorRaw{}
		case "gap":
			n.at = at.Add(20 * time.Second)
			n.disk = []ioCounter{{id: "disk", read: 200, write: 200, at: n.at}}
		case "failure":
			p.cpuError = errors.New("denied")
			p.diskError = errors.New("denied")
		case "replaced":
			n.cpu = []cpuCounter{{id: "cpu1", busy: 200, idle: 200}}
			n.disk = []ioCounter{{id: "new-device", read: 200, write: 200, at: n.at}}
		case "zero-interval":
			n.at = p.at
			n.disk = []ioCounter{{id: "disk", read: 200, write: 200, at: p.at}}
		}
		v = monitorView(n, p)
		if v.CPU.Value != nil || (len(v.DiskIO) > 0 && (v.DiskIO[0].Read.Value != nil || v.DiskIO[0].Write.Value != nil)) {
			t.Fatalf("%s inferred a valid rate", change)
		}
	}
}
func TestMonitorBoundedHistoryCursorEpochFreshnessAndCopies(t *testing.T) {
	m := NewMonitor("host-fixture", nil)
	p, changed, e := m.Read(model.MonitorQuery{})
	if e != nil || p.Latest != nil || !p.Stale || len(p.History) != 0 {
		t.Fatal("invented initial sample", p, e)
	}
	for i := 0; i < MonitorHistory+5; i++ {
		v := model.MonitorSnapshot{At: time.Now(), CPU: monitorValue(float64(i), "percent", "host", "fixture", time.Now())}
		m.publish(v, time.Now())
	}
	select {
	case <-changed:
	default:
		t.Fatal("publication did not wake atomic reader")
	}
	p, _, e = m.Read(model.MonitorQuery{})
	if e != nil || len(p.History) != MonitorHistory || p.Oldest != 6 || p.Cursor != MonitorHistory+5 || p.Stale || p.Gap {
		t.Fatal("unbounded/inconsistent retained window", p, e)
	}
	*p.Latest.CPU.Value = 999
	*p.History[0].CPU = 999
	page, _, e := m.Read(model.MonitorQuery{Epoch: p.Epoch, After: p.Cursor})
	if e != nil || len(page.History) != 0 || *page.Latest.CPU.Value == 999 {
		t.Fatal("reader mutated shared state", page, e)
	}
	for _, q := range []model.MonitorQuery{{Epoch: p.Epoch, After: 1}, {Epoch: "previous-collector", After: 99999}} {
		page, _, e = m.Read(q)
		if e != nil || !page.Gap || len(page.History) != MonitorHistory || *page.History[0].CPU == 999 {
			t.Fatal("lost telemetry was not signalled", page, e)
		}
	}
	for _, q := range []model.MonitorQuery{{Host: "other"}, {After: 1}, {Epoch: p.Epoch, After: -1}, {Epoch: p.Epoch, After: 99999}} {
		if _, _, e = m.Read(q); e == nil {
			t.Fatal("invalid cursor/host accepted", q)
		}
	}
	m.mu.Lock()
	m.sampled = time.Now().Add(-7 * time.Second)
	m.mu.Unlock()
	page, _, e = m.Read(model.MonitorQuery{})
	if e != nil || !page.Stale || page.Age < 7*time.Second {
		t.Fatal("old collection was live", page, e)
	}
}

func TestMonitorHistoryRetainsObservedAvailableMemory(t *testing.T) {
	m := NewMonitor("host-fixture", nil)
	at := time.Now()
	view := model.MonitorSnapshot{At: at, Memory: memoryValues(100, 37, "host", "fixture", at)}
	m.publish(view, at)
	page, _, err := m.Read(model.MonitorQuery{})
	if err != nil || len(page.History) != 1 || page.History[0].MemoryAvailable == nil || *page.History[0].MemoryAvailable != 37 || page.History[0].MemoryUsed == nil || *page.History[0].MemoryUsed != 63 {
		t.Fatalf("history lost observed availability: %+v, %v", page.History, err)
	}
	m.publish(model.MonitorSnapshot{At: at.Add(time.Second)}, at.Add(time.Second))
	page, _, err = m.Read(model.MonitorQuery{})
	if err != nil || len(page.History) != 2 || page.History[1].MemoryAvailable != nil {
		t.Fatalf("unknown availability became zero: %+v, %v", page.History, err)
	}
}
func TestMonitorDoesNotPollPerReaderAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := NewMonitor("host", func() model.ResourceMeasurement { return model.ResourceMeasurement{} })
	calls := make(chan struct{}, 1)
	m.collect = func(ctx context.Context) monitorRaw { calls <- struct{}{}; return monitorRaw{at: time.Now()} }
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("collector did not start")
	}
	for i := 0; i < 100; i++ {
		if _, _, e := m.Read(model.MonitorQuery{}); e != nil {
			t.Fatal(e)
		}
	}
	select {
	case <-calls:
		t.Fatal("readers caused additional host polling")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector did not stop")
	}
}
func TestMonitorLargestRetainedPageFitsTransportLimit(t *testing.T) {
	m := NewMonitor("host", nil)
	value := 1.0
	v := model.MonitorSnapshot{At: time.Now(), CPU: model.Measurement{Value: &value}}
	for i := 0; i < monitorCPUs; i++ {
		v.Cores = append(v.Cores, model.MonitorCPU{ID: strings.Repeat("c", 32), Busy: monitorValue(50, "percent", "host-cpu", "native", v.At)})
	}
	for i := 0; i < monitorRows; i++ {
		row := model.MonitorIO{ID: strings.Repeat("i", 128), Name: strings.Repeat("n", 128), Read: monitorValue(1, "bytes/second", "device", "native", v.At), Write: monitorValue(2, "bytes/second", "device", "native", v.At)}
		v.DiskIO = append(v.DiskIO, row)
		v.Network = append(v.Network, row)
	}
	for i := 0; i < MonitorHistory; i++ {
		m.publish(v, time.Now())
	}
	p, _, err := m.Read(model.MonitorQuery{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil || len(data) >= 4<<20 {
		t.Fatalf("bounded history exceeds client message limit: %d %v", len(data), err)
	}
	if p.Latest.FilesystemStale != true {
		t.Fatal("missing filesystem sample appeared fresh")
	}
}
func TestNativeMonitorCounters(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := collectMonitor(ctx)
	if first.cpuError != nil {
		if os.Getenv("ARM_TEST_NATIVE_MONITOR") == "1" {
			t.Fatal(first.cpuError)
		}
		t.Skipf("native CPU counters unavailable: %v", first.cpuError)
	}
	time.Sleep(60 * time.Millisecond)
	next := collectMonitor(ctx)
	v := monitorView(next, first)
	t.Logf("native scope: %d CPUs, disk %s (%d rows), network %s (%d rows)", len(v.Cores), v.DiskQuality, len(v.DiskIO), v.NetworkQuality, len(v.Network))
	if v.CPU.Value == nil || math.IsNaN(*v.CPU.Value) || *v.CPU.Value < 0 || *v.CPU.Value > 100 || len(v.Cores) == 0 {
		t.Fatalf("native CPU counters invalid: %+v errors %v", v, next.cpuError)
	}
	if v.Memory.Total.Value == nil || *v.Memory.Total.Value <= 0 || v.Memory.Available.Value == nil || v.Swap.Total.Value == nil {
		t.Fatalf("native memory/swap unavailable: %+v %+v", v.Memory, v.Swap)
	}
	if next.networkError != nil || len(v.Network) == 0 {
		t.Fatalf("native network counters unavailable: %v", next.networkError)
	}
	if os.Getenv("ARM_TEST_NATIVE_MONITOR") == "1" && (next.diskError != nil || len(v.DiskIO) == 0) {
		t.Fatalf("required native disk counters unavailable: %v", next.diskError)
	}
	for _, rows := range [][]model.MonitorIO{v.Network, v.DiskIO} {
		for _, row := range rows {
			if row.Read.Value != nil && *row.Read.Value < 0 {
				t.Fatal("negative native rate")
			}
		}
	}
}
