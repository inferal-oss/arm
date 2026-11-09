package observe

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/model"
)

// psiLine is one Linux pressure stall information row: avg10/avg60 percent
// and the cumulative stall total in microseconds.
type psiLine struct {
	avg10, avg60 float64
	total        uint64
}

// pressureRaw carries native pressure counters. Fields a platform cannot
// observe keep their errors; unknown never becomes normal.
type pressureRaw struct {
	load     [3]float64
	loadErr  error
	psi      map[string]psiLine
	psiErr   error
	memLevel int
	memErr   error
	// Darwin vm_statistics64 cumulative page counters.
	vm           [4]uint64 // pageouts, swapins, swapouts, compressions
	compressor   uint64    // pages held by the compressor
	pageSize     uint64
	vmErr        error
	vmMethod     string
	loadMethod   string
	memoryMethod string
}

// Thresholds are documented heuristics, not kernel semantics. Each row lists
// the elevated, high and critical boundaries of one signal.
type pressureThreshold struct{ elevated, high, critical float64 }

var (
	loadThreshold        = pressureThreshold{1.25, 2, 4} // 1-minute load per logical CPU
	psiCPUSome           = pressureThreshold{25, 50, 80} // percent of time some tasks stalled
	psiMemorySome        = pressureThreshold{5, 15, 30}  //
	psiMemoryFull        = pressureThreshold{1, 5, 15}   // percent of time all tasks stalled
	psiIOSome            = pressureThreshold{20, 40, 60} //
	psiIOFull            = pressureThreshold{5, 15, 30}  //
	swapRateThreshold    = pressureThreshold{4, 32, 128} // MiB/s swapped in or out
	diskQueueThreshold   = pressureThreshold{2, 8, 32}   // mean requests in service per device
	memoryAvailableLow   = pressureThreshold{15, 8, 4}   // percent of RAM available (inverted)
	swapRemainingLow     = pressureThreshold{20, 10, 5}  // percent of swap remaining (inverted)
	pressureLevelsByRank = []string{model.PressureNormal, model.PressureElevated, model.PressureHigh, model.PressureCritical}
)

func (t pressureThreshold) level(v float64) string {
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return model.PressureUnknown
	case v >= t.critical:
		return model.PressureCritical
	case v >= t.high:
		return model.PressureHigh
	case v >= t.elevated:
		return model.PressureElevated
	}
	return model.PressureNormal
}

// inverted levels a signal where lower values mean more pressure.
func (t pressureThreshold) inverted(v float64) string {
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return model.PressureUnknown
	case v <= t.critical:
		return model.PressureCritical
	case v <= t.high:
		return model.PressureHigh
	case v <= t.elevated:
		return model.PressureElevated
	}
	return model.PressureNormal
}

// bound records the boundary of the signal's own level, so a reason can
// say how far a value went, not only that it crossed a line.
func (t pressureThreshold) bound(s model.PressureSignal, inverted bool) model.PressureSignal {
	edge, ok := map[string]float64{model.PressureElevated: t.elevated, model.PressureHigh: t.high, model.PressureCritical: t.critical}[s.Level]
	if !ok {
		return s
	}
	op := "≥"
	if inverted {
		op = "≤"
	}
	s.Threshold = op + pressureNumber(edge, s.Value.Unit)
	return s
}

// pressureNumber formats a signal value with its unit.
func pressureNumber(v float64, unit string) string {
	return pressureNumberToward(v, unit, 0)
}

// pressureNumberToward rounds down (toward < 0), up (> 0) or to nearest, so
// a shown value never appears to reach a boundary it has not reached.
func pressureNumberToward(v float64, unit string, toward int) string {
	text := strconvFloat(v, toward)
	switch unit {
	case "percent":
		return text + "%"
	case "MiB/second":
		return text + " MiB/s"
	case "requests":
		return text + " requests"
	}
	return text
}

func strconvFloat(v float64, toward int) string {
	scale := 100.0
	if math.Abs(v) >= 10 {
		scale = 10
	}
	round := math.Round
	if toward < 0 {
		round = math.Floor
	} else if toward > 0 {
		round = math.Ceil
	}
	return strconv.FormatFloat(round(v*scale)/scale, 'f', -1, 64)
}

// pressureReason is one signal as evidence: its label, value and the
// boundary it reached.
func pressureReason(s model.PressureSignal) string {
	text := s.Label
	if v := s.Value.Value; v != nil {
		if s.Value.Unit == "level" {
			if name := darwinMemoryLevels[int(*v)]; name != "" {
				text += " " + name
			}
		} else {
			// A rising signal rounds down and a falling one up, so the
			// value stays on its own side of the next level's boundary.
			toward := -1
			if strings.HasPrefix(s.Threshold, "≤") {
				toward = 1
			}
			text += " " + pressureNumberToward(*v, s.Value.Unit, toward)
		}
	}
	if s.Threshold != "" {
		text += " (" + s.Level + " at " + s.Threshold + ")"
	}
	return text
}

// darwinMemoryLevels names kernel memory status levels.
var darwinMemoryLevels = map[int]string{1: "normal", 2: "warn", 4: "critical"}

func pressureSignal(id, label string, value float64, unit, method, level string, at time.Time) model.PressureSignal {
	return model.PressureSignal{ID: id, Label: label, Value: monitorValue(value, unit, "host", method, at), Level: level}
}
func pressureUnknownSignal(id, label, unit, method, detail string, at time.Time) model.PressureSignal {
	return model.PressureSignal{ID: id, Label: label, Value: monitorUnknown(unit, "host", method, detail, at), Level: model.PressureUnknown}
}

// combine reports the highest known signal level. A reading is unknown only
// when no signal is known; partial evidence still reports what it shows.
func combine(signals []model.PressureSignal) model.PressureReading {
	r := model.PressureReading{Level: model.PressureUnknown, Signals: signals}
	best, reasons := -1, []string{}
	for _, s := range signals {
		rank, ok := model.PressureRank(s.Level)
		if !ok {
			continue
		}
		if rank > best {
			best, reasons = rank, nil
		}
		if rank == best && rank > 0 {
			reasons = append(reasons, pressureReason(s))
		}
	}
	if best >= 0 {
		r.Level = pressureLevelsByRank[best]
		r.Reason = strings.Join(reasons, "; ")
	}
	if r.Signals == nil {
		r.Signals = []model.PressureSignal{}
	}
	return r
}

func pressureView(raw, previous monitorRaw) model.MonitorPressure {
	at, p := raw.at, raw.pressure
	cpus := len(raw.cpu)
	var cpu, memory, io []model.PressureSignal
	// CPU
	if p.psiErr == nil && p.psi != nil {
		if l, ok := p.psi["cpu.some"]; ok {
			cpu = append(cpu, psiCPUSome.bound(pressureSignal("psi.cpu.some", "CPU stall (some tasks, 10s)", l.avg10, "percent", "proc:/proc/pressure/cpu", psiCPUSome.level(l.avg10), at), false))
		}
	}
	if p.loadErr == nil && cpus > 0 {
		ratio := p.load[0] / float64(cpus)
		level := loadThreshold.level(ratio)
		if len(cpu) > 0 {
			// PSI is authoritative where present; load also counts
			// uninterruptible I/O waits on Linux.
			level = model.PressureNormal
		}
		cpu = append(cpu, loadThreshold.bound(pressureSignal("load.per-cpu", "Run queue per CPU (1m load)", ratio, "ratio", p.loadMethod, level, at), false))
	} else {
		detail := "load average unavailable"
		if p.loadErr != nil {
			detail = p.loadErr.Error()
		}
		cpu = append(cpu, pressureUnknownSignal("load.per-cpu", "Run queue per CPU (1m load)", "ratio", p.loadMethod, detail, at))
	}
	// Memory
	if p.psiErr == nil && p.psi != nil {
		if l, ok := p.psi["memory.some"]; ok {
			memory = append(memory, psiMemorySome.bound(pressureSignal("psi.memory.some", "Memory stall (some tasks, 10s)", l.avg10, "percent", "proc:/proc/pressure/memory", psiMemorySome.level(l.avg10), at), false))
		}
		if l, ok := p.psi["memory.full"]; ok {
			memory = append(memory, psiMemoryFull.bound(pressureSignal("psi.memory.full", "Memory stall (all tasks, 10s)", l.avg10, "percent", "proc:/proc/pressure/memory", psiMemoryFull.level(l.avg10), at), false))
		}
	}
	if p.memoryMethod != "" {
		if p.memErr != nil {
			memory = append(memory, pressureUnknownSignal("memorystatus.level", "Kernel memory pressure", "level", p.memoryMethod, p.memErr.Error(), at))
		} else {
			level := model.PressureNormal
			switch {
			case p.memLevel >= 4:
				level = model.PressureCritical
			case p.memLevel >= 2:
				// Warn is common in ordinary use on smaller Macs; swap
				// traffic corroborates real stalls.
				level = model.PressureElevated
			}
			memory = append(memory, pressureSignal("memorystatus.level", "Kernel memory pressure", float64(p.memLevel), "level", p.memoryMethod, level, at))
		}
	}
	if rate, ok := pageRate(p, previous.pressure, raw.at.Sub(previous.at)); ok {
		memory = append(memory, swapRateThreshold.bound(pressureSignal("swap.rate", "Swap traffic", rate, "MiB/second", p.vmMethod, swapRateThreshold.level(rate), at), false))
	} else if p.vmMethod != "" {
		memory = append(memory, pressureUnknownSignal("swap.rate", "Swap traffic", "MiB/second", p.vmMethod, "two consecutive valid samples are required", at))
	}
	if t, a := raw.memory.Total.Value, raw.memory.Available.Value; t != nil && a != nil && *t > 0 && len(memory) == 0 {
		// Without kernel pressure signals, low estimated availability is
		// the remaining evidence. Darwin excludes compressible memory, so
		// it is not used there.
		pct := 100 * *a / *t
		memory = append(memory, memoryAvailableLow.bound(pressureSignal("memory.available", "RAM available", pct, "percent", raw.memory.Available.Method, memoryAvailableLow.inverted(pct), at), true))
	}
	if t, a := raw.swap.Total.Value, raw.swap.Available.Value; t != nil && a != nil && *t > 0 && *t-*a > 0 {
		pct := 100 * *a / *t
		level := swapRemainingLow.inverted(pct)
		if rank, _ := model.PressureRank(level); rank > 1 {
			// Exhausted swap alone is not stalling; it limits headroom.
			level = model.PressureElevated
		}
		memory = append(memory, swapRemainingLow.bound(pressureSignal("swap.remaining", "Swap remaining", pct, "percent", raw.swap.Available.Method, level, at), true))
	}
	// I/O
	if p.psiErr == nil && p.psi != nil {
		if l, ok := p.psi["io.some"]; ok {
			io = append(io, psiIOSome.bound(pressureSignal("psi.io.some", "I/O stall (some tasks, 10s)", l.avg10, "percent", "proc:/proc/pressure/io", psiIOSome.level(l.avg10), at), false))
		}
		if l, ok := p.psi["io.full"]; ok {
			io = append(io, psiIOFull.bound(pressureSignal("psi.io.full", "I/O stall (all tasks, 10s)", l.avg10, "percent", "proc:/proc/pressure/io", psiIOFull.level(l.avg10), at), false))
		}
	}
	if depth, name, ok := diskQueue(raw, previous); ok {
		level := diskQueueThreshold.level(depth)
		if len(io) > 0 {
			level = model.PressureNormal
		}
		io = append(io, diskQueueThreshold.bound(pressureSignal("disk.queue", "Busiest disk queue ("+name+")", depth, "requests", raw.diskMethod, level, at), false))
	} else if len(io) == 0 {
		io = append(io, pressureUnknownSignal("disk.queue", "Busiest disk queue", "requests", raw.diskMethod, "disk service-time counters require two consecutive valid samples", at))
	}
	return model.MonitorPressure{CPU: combine(cpu), Memory: combine(memory), IO: combine(io)}
}

// pageRate reports swap-in plus swap-out traffic in MiB/s from cumulative
// page counters. Darwin compressor pageouts are excluded: compression is
// ordinary memory management, not disk traffic.
func pageRate(current, previous pressureRaw, dt time.Duration) (float64, bool) {
	if current.vmErr != nil || previous.vmErr != nil || current.vmMethod == "" || previous.vmMethod == "" || current.pageSize == 0 || dt <= 0 || dt > 3*MonitorCadence {
		return 0, false
	}
	in, out := current.vm[1], current.vm[2]
	if in < previous.vm[1] || out < previous.vm[2] {
		return 0, false
	}
	pages := float64(in-previous.vm[1]) + float64(out-previous.vm[2])
	return pages * float64(current.pageSize) / (1 << 20) / dt.Seconds(), true
}

// diskQueue reports the largest mean number of requests in service across
// devices: the service-time counter delta divided by elapsed time.
func diskQueue(raw, previous monitorRaw) (float64, string, bool) {
	if raw.diskError != nil || previous.diskError != nil {
		return 0, "", false
	}
	prior := map[string]ioCounter{}
	for _, c := range previous.disk {
		prior[c.id] = c
	}
	best, name, found := 0.0, "", false
	for _, c := range raw.disk {
		p, ok := prior[c.id]
		dt := c.at.Sub(p.at)
		if !ok || !c.timed || !p.timed || c.busy < p.busy || dt <= 0 || dt > 3*MonitorCadence {
			continue
		}
		depth := float64(c.busy-p.busy) / float64(dt.Nanoseconds())
		if !found || depth > best {
			best, name, found = depth, c.name, true
		}
	}
	return best, name, found
}

func parsePSI(kind string, data []byte, into map[string]psiLine) error {
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(line)
		if len(f) != 5 || (f[0] != "some" && f[0] != "full") {
			return fmt.Errorf("invalid %s pressure row", kind)
		}
		var l psiLine
		for _, field := range f[1:] {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				return fmt.Errorf("invalid %s pressure field", kind)
			}
			var err error
			switch key {
			case "avg10":
				_, err = fmt.Sscanf(value, "%g", &l.avg10)
			case "avg60":
				_, err = fmt.Sscanf(value, "%g", &l.avg60)
			case "avg300":
			case "total":
				_, err = fmt.Sscanf(value, "%d", &l.total)
			default:
				err = fmt.Errorf("unknown field")
			}
			if err != nil || l.avg10 < 0 || l.avg10 > 100 || l.avg60 < 0 || l.avg60 > 100 {
				return fmt.Errorf("invalid %s pressure value", kind)
			}
		}
		into[kind+"."+f[0]] = l
	}
	return nil
}

func pressurePoint(level string) *int {
	if rank, ok := model.PressureRank(level); ok {
		return &rank
	}
	return nil
}
