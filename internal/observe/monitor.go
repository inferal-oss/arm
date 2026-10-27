package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

const MonitorCadence = 2 * time.Second
const MonitorHistory = 60
const monitorRows = 64
const monitorCPUs = 256

type cpuCounter struct {
	id         string
	busy, idle uint64
	parts      [8]uint64
}
type ioCounter struct {
	id, name    string
	read, write uint64
	at          time.Time
}
type monitorRaw struct {
	at                                   time.Time
	cpuMethod, diskMethod, networkMethod string
	cpu                                  []cpuCounter
	cpuError                             error
	memory, swap                         model.ResourceMeasurement
	disk, network                        []ioCounter
	diskError, networkError              error
}

// Monitor has one collector independent of client count, bounded history, and
// an atomic snapshot/change notification boundary. Returned pages are copies.
type Monitor struct {
	mu          sync.Mutex
	host, epoch string
	sequence    int64
	latest      *model.MonitorSnapshot
	points      []model.MonitorPoint
	changed     chan struct{}
	sampled     time.Time
	collect     func(context.Context) monitorRaw
	filesystem  func() model.ResourceMeasurement
}

func NewMonitor(host string, filesystem func() model.ResourceMeasurement) *Monitor {
	return &Monitor{host: host, epoch: local.ID(), changed: make(chan struct{}), collect: collectMonitor, filesystem: filesystem}
}
func (m *Monitor) Run(ctx context.Context) {
	var previous monitorRaw
	ticker := time.NewTicker(MonitorCadence)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		bounded, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		raw := m.collect(bounded)
		cancel()
		view := monitorView(raw, previous)
		view.Collection = time.Since(started)
		view.Filesystem = m.filesystem()
		m.publish(view, started)
		previous = raw
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (m *Monitor) publish(view model.MonitorSnapshot, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sequence++
	m.latest = &view
	m.sampled = now
	point := model.MonitorPoint{Sequence: m.sequence, At: view.At, CPU: view.CPU.Value, MemoryUsed: view.Memory.Used.Value, MemoryAvailable: view.Memory.Available.Value, SwapUsed: view.Swap.Used.Value, FilesystemAvailable: view.Filesystem.Available.Value, FilesystemAt: view.Filesystem.Available.At, DiskIO: []model.MonitorHistoryIO{}, Network: []model.MonitorHistoryIO{}}
	for _, v := range view.DiskIO {
		point.DiskIO = append(point.DiskIO, model.MonitorHistoryIO{ID: v.ID, Read: v.Read.Value, Write: v.Write.Value})
	}
	for _, v := range view.Network {
		point.Network = append(point.Network, model.MonitorHistoryIO{ID: v.ID, Read: v.Read.Value, Write: v.Write.Value})
	}
	if len(m.points) == MonitorHistory {
		copy(m.points, m.points[1:])
		m.points = m.points[:MonitorHistory-1]
	}
	m.points = append(m.points, point)
	close(m.changed)
	m.changed = make(chan struct{})
}
func (m *Monitor) Read(q model.MonitorQuery) (model.MonitorPage, <-chan struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if q.Host != "" && q.Host != "localhost" && q.Host != m.host {
		return model.MonitorPage{}, nil, model.Err("host_unavailable", "only the local monitoring host is registered")
	}
	if q.After < 0 || (q.After > 0 && q.Epoch == "") {
		return model.MonitorPage{}, nil, model.Err("invalid_request", "monitor after requires an epoch and must be nonnegative")
	}
	if q.Epoch == m.epoch && q.After > m.sequence {
		return model.MonitorPage{}, nil, model.Err("invalid_request", "monitor cursor is in the future")
	}
	p := model.MonitorPage{Host: m.host, Epoch: m.epoch, Cursor: m.sequence, Cadence: MonitorCadence, StaleAfter: 3 * MonitorCadence, FilesystemCadence: 5 * time.Second, Latest: m.latest, History: []model.MonitorPoint{}, Stale: m.latest == nil}
	if m.latest != nil {
		p.Age = max(0, time.Since(m.sampled))
		p.Stale = p.Age > p.StaleAfter
	}
	if len(m.points) > 0 {
		p.Oldest = m.points[0].Sequence
	}
	p.Gap = (q.Epoch != "" && q.Epoch != m.epoch) || (q.After > 0 && q.After < p.Oldest-1)
	for _, point := range m.points {
		if p.Gap || point.Sequence > q.After {
			p.History = append(p.History, point)
		}
	}
	// JSON copying preserves nil metrics and exact cursor integers, and prevents
	// callers or renderers from mutating shared sample/history pointers.
	data, err := json.Marshal(p)
	if err != nil {
		return model.MonitorPage{}, nil, err
	}
	var result model.MonitorPage
	err = json.Unmarshal(data, &result)
	if result.Latest != nil {
		age := time.Since(result.Latest.Filesystem.Available.At)
		result.Latest.FilesystemStale = result.Latest.Filesystem.Available.At.IsZero() || age > 3*p.FilesystemCadence || age < -time.Second
	}
	return result, m.changed, err
}
func monitorValue(value float64, unit, scope, method string, at time.Time) model.Measurement {
	return model.Measurement{Value: &value, Unit: unit, Scope: scope, Method: method, Quality: "reported", At: at, Complete: true}
}
func monitorUnknown(unit, scope, method, detail string, at time.Time) model.Measurement {
	return model.Measurement{Unit: unit, Scope: scope, Method: method, Quality: "unavailable", Detail: detail, At: at}
}
func memoryValues(total, available uint64, scope, method string, at time.Time) model.ResourceMeasurement {
	if available > total {
		return unavailable(scope, method, "inconsistent memory counters")
	}
	return model.ResourceMeasurement{Total: monitorValue(float64(total), "bytes", scope, method, at), Available: monitorValue(float64(available), "bytes", scope, method, at), Used: monitorValue(float64(total-available), "bytes", scope, method, at)}
}
func monitorView(raw, previous monitorRaw) model.MonitorSnapshot {
	v := model.MonitorSnapshot{At: raw.at, Memory: raw.memory, Swap: raw.swap, Cores: []model.MonitorCPU{}, CPU: monitorUnknown("percent", "host-cpu", "native-cpu-counters", "two consecutive valid samples are required", raw.at), GPU: monitorUnknown("percent", "host-gpu", "none", "GPU telemetry provider is not implemented; this is not zero utilization", raw.at)}
	prior := map[string]cpuCounter{}
	for _, c := range previous.cpu {
		prior[c.id] = c
	}
	complete := raw.cpuError == nil && previous.cpuError == nil && len(raw.cpu) > 0 && len(raw.cpu) == len(previous.cpu) && raw.at.Sub(previous.at) > 0 && raw.at.Sub(previous.at) <= 3*MonitorCadence
	var busy, total float64
	for _, c := range raw.cpu {
		metric := v.CPU
		p, ok := prior[c.id]
		if ok && cpuMonotone(c, p) && raw.cpuError == nil && previous.cpuError == nil && c.busy >= p.busy && c.idle >= p.idle && raw.at.Sub(previous.at) > 0 && raw.at.Sub(previous.at) <= 3*MonitorCadence {
			b, i := float64(c.busy-p.busy), float64(c.idle-p.idle)
			if b+i > 0 {
				metric = monitorValue(100*b/(b+i), "percent", "host-cpu:"+c.id, "native-cpu-counter-delta", raw.at)
				busy += b
				total += b + i
			} else {
				complete = false
			}
		} else {
			complete = false
		}
		v.Cores = append(v.Cores, model.MonitorCPU{ID: c.id, Busy: metric})
	}
	if complete && total > 0 {
		v.CPU = monitorValue(100*busy/total, "percent", "all-reported-host-cpus", "native-cpu-counter-delta", raw.at)
		v.CPUInterval = raw.at.Sub(previous.at)
	}
	if raw.cpuError != nil {
		v.CPU.Detail = raw.cpuError.Error()
	}
	if raw.cpuMethod != "" {
		v.CPU.Method = raw.cpuMethod + ":counter-delta"
		for i := range v.Cores {
			v.Cores[i].Busy.Method = v.CPU.Method
		}
	}
	if v.CPU.Detail != "" {
		v.CPU.Detail += "; "
	}
	v.CPU.Detail += "Interval non-idle CPU time excluding I/O wait; Linux steal is included; not a reservation or performance guarantee"
	v.DiskIO, v.DiskQuality, v.DiskDetail = ioView(raw.disk, previous.disk, raw.diskError, previous.diskError, "block-device")
	v.Network, v.NetworkQuality, v.NetworkDetail = ioView(raw.network, previous.network, raw.networkError, previous.networkError, "network-interface")
	for _, group := range []struct {
		rows   []model.MonitorIO
		method string
	}{{v.DiskIO, raw.diskMethod}, {v.Network, raw.networkMethod}} {
		if group.method != "" {
			for i := range group.rows {
				group.rows[i].Read.Method = group.method + ":counter-delta"
				group.rows[i].Write.Method = group.method + ":counter-delta"
			}
		}
	}
	return v
}
func cpuMonotone(current, previous cpuCounter) bool {
	for i, n := range current.parts {
		if n < previous.parts[i] {
			return false
		}
	}
	return true
}
func ioView(current, previous []ioCounter, err, priorErr error, scope string) ([]model.MonitorIO, string, string) {
	rows := []model.MonitorIO{}
	quality, detail := "reported", "Per-device/interface counters; layered devices and virtual interfaces must not be summed as exclusive physical traffic"
	if err != nil {
		quality = "incomplete"
		detail = err.Error()
	}
	if len(current) == 0 {
		quality = "unavailable"
		if err == nil {
			detail = "No counters were reported in this observation scope"
		}
	}
	prior := map[string]ioCounter{}
	for _, c := range previous {
		prior[c.id] = c
	}
	sort.Slice(current, func(i, j int) bool { return current[i].id < current[j].id })
	for _, c := range current {
		r := model.MonitorIO{ID: c.id, Name: c.name, Read: monitorUnknown("bytes/second", scope+":"+c.id, "native-counter-delta", "two consecutive valid samples are required; resets and gaps are not zero traffic", c.at)}
		r.Write = r.Read
		p, ok := prior[c.id]
		dt := c.at.Sub(p.at)
		if ok && err == nil && priorErr == nil && dt > 0 && dt <= 3*MonitorCadence {
			r.Interval = dt
			if c.read >= p.read {
				r.Read = monitorValue(float64(c.read-p.read)/dt.Seconds(), "bytes/second", scope+":"+c.id, "native-counter-delta", c.at)
			}
			if c.write >= p.write {
				r.Write = monitorValue(float64(c.write-p.write)/dt.Seconds(), "bytes/second", scope+":"+c.id, "native-counter-delta", c.at)
			}
		}
		rows = append(rows, r)
	}
	return rows, quality, detail
}
func checkedAdd(a, b uint64) (uint64, error) {
	if a > math.MaxUint64-b {
		return 0, fmt.Errorf("native counter overflow")
	}
	return a + b, nil
}
