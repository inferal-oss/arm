//go:build arm_devtrace

package manager

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/yrashk/arm/internal/model"
)

// Private tagged collectors are installed by a test for one original Manager.
// No environment, request or wire field enables tracing. Hooks freeze computed
// values only; they perform no SQL/native work or reentrant Manager call.
type cpuTraceRow struct {
	Schema    string             `json:"schema"`
	Scenario  string             `json:"scenario"`
	Ordinal   int                `json:"ordinal"`
	Cut       string             `json:"cut"`
	Host      string             `json:"host"`
	Owner     string             `json:"owner"`
	Budget    model.CPUBudget    `json:"budget"`
	Requested float64            `json:"requested"`
	Operation *cpuTraceOperation `json:"operation,omitempty"`
	Leases    []cpuTraceSQLLease `json:"leases,omitempty"`
	Events    []cpuTraceSQLEvent `json:"events,omitempty"`
}
type cpuTraceOperation struct {
	Action string  `json:"action"`
	Key    string  `json:"key"`
	ID     string  `json:"id"`
	CPU    float64 `json:"cpu"`
	Error  string  `json:"error"`
}
type cpuTraceSQLLease struct {
	Row  int64           `json:"row"`
	ID   string          `json:"id"`
	Key  string          `json:"key"`
	Body json.RawMessage `json:"body"`
}
type cpuTraceSQLEvent struct {
	Sequence int64           `json:"sequence"`
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	Body     json.RawMessage `json:"body"`
}
type cpuTraceObserver struct {
	mu        sync.Mutex
	scenario  string
	rows      []cpuTraceRow
	bytes     int
	failure   error
	lastEvent int64
}

var cpuTraceObservers sync.Map // *Manager -> *cpuTraceObserver

func (o *cpuTraceObserver) append(row cpuTraceRow) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.failure != nil {
		return
	}
	row.Schema, row.Scenario, row.Ordinal = "arm.cpu-admission-actual/v1", o.scenario, len(o.rows)+1
	raw, e := json.Marshal(row)
	if e != nil || len(raw) > 128<<10 || o.bytes+len(raw) > 2<<20 || len(o.rows) >= 256 {
		o.failure = fmt.Errorf("bounded immutable CPU trace unavailable: %v", e)
		return
	}
	var frozen cpuTraceRow
	if e = json.Unmarshal(raw, &frozen); e != nil {
		o.failure = e
		return
	}
	o.rows = append(o.rows, frozen)
	o.bytes += len(raw)
}
func traceCPUValue(m *Manager, b model.CPUBudget, requested float64, cut string) {
	if v, ok := cpuTraceObservers.Load(m); ok {
		v.(*cpuTraceObserver).append(cpuTraceRow{Cut: cut, Host: m.host, Owner: m.owner, Budget: b, Requested: requested})
	}
}
func traceCPUBudget(m *Manager, b model.CPUBudget) { traceCPUValue(m, b, 0, "budget") }
func traceCPUAdmission(m *Manager, b model.CPUBudget, requested float64) {
	traceCPUValue(m, b, requested, "admission")
}
func traceCPURenewal(m *Manager, b model.CPUBudget) { traceCPUValue(m, b, 0, "renewal") }
