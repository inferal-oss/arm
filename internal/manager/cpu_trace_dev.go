//go:build arm_devtrace

package manager

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/yrashk/arm/internal/model"
)

// Private tagged collectors are installed by a test for one original Manager.
// No environment, request or wire field enables tracing. Hooks freeze computed
// values and bounded selected SQL readbacks; they perform no native work or
// reentrant Manager call. All selected credit cuts hold Manager.mu.
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
	Credits   []cpuTraceCredit   `json:"credit_inputs"`
}

// Candidate evidence is private test input. Eligibility is recomputed by the
// reader from exact original/current job and SQL facts, never a verdict flag.
type cpuTraceCredit struct {
	ID                string                 `json:"id"`
	SampleGeneration  string                 `json:"sample_generation"`
	SampleIdentity    model.ProcessIdentity  `json:"sample_identity"`
	SampleCookie      string                 `json:"sample_cookie"`
	Cores             float64                `json:"cores"`
	CurrentGeneration string                 `json:"current_generation"`
	CurrentIdentity   *model.ProcessIdentity `json:"current_identity"`
	CurrentCookie     string                 `json:"current_cookie"`
	CurrentExited     bool                   `json:"current_exited"`
	CurrentDone       bool                   `json:"current_done"`
	Lease             *cpuTraceSQLLease      `json:"current_lease"`
	LeaseReadError    string                 `json:"lease_read_error"`
}

type cpuTraceOperation struct {
	Action        string  `json:"action"`
	Key           string  `json:"key"`
	ID            string  `json:"id"`
	CPU           float64 `json:"cpu"`
	Error         string  `json:"error"`
	JobGeneration string  `json:"job_generation"`
	Joined        bool    `json:"joined"`
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
		v.(*cpuTraceObserver).append(cpuTraceRow{Cut: cut, Host: m.host, Owner: m.owner, Budget: b, Requested: requested, Credits: cpuTraceCreditInputs(m)})
	}
}
func traceCPUBudget(m *Manager, b model.CPUBudget) { traceCPUValue(m, b, 0, "budget") }
func traceCPUAdmission(m *Manager, b model.CPUBudget, requested float64) {
	traceCPUValue(m, b, requested, "admission")
}
func traceCPURenewal(m *Manager, b model.CPUBudget) { traceCPUValue(m, b, 0, "renewal") }

// Only registered tagged tests enter here. Caller holds Manager.mu. These
// bounded SQL readbacks do not call Manager APIs or acquire its lock again.
func cpuTraceCreditInputs(m *Manager) []cpuTraceCredit {
	m.observationMu.RLock()
	samples := append([]cpuCreditSample(nil), m.cpuCredits...)
	m.observationMu.RUnlock()
	result := []cpuTraceCredit{}
	if len(samples) > 32 {
		return nil
	} // closed reader refuses missing evidence
	for _, sample := range samples {
		row := cpuTraceCredit{ID: sample.id, SampleGeneration: fmt.Sprintf("%p", sample.job), SampleIdentity: sample.identity, SampleCookie: sample.cookie, Cores: sample.cores}
		if j := m.jobs[sample.id]; j != nil {
			row.CurrentGeneration = fmt.Sprintf("%p", j)
			if j.identity != nil {
				copy := *j.identity
				row.CurrentIdentity = &copy
			}
			row.CurrentCookie = j.cpuAnchor
			row.CurrentExited = j.cpuExited.Load()
			select {
			case <-j.done:
				row.CurrentDone = true
			default:
			}
		}
		var l cpuTraceSQLLease
		var key sql.NullString
		var raw []byte
		e := m.db.QueryRow(`SELECT rowid,id,request_key,data FROM leases WHERE id=?`, sample.id).Scan(&l.Row, &l.ID, &key, &raw)
		if e != nil {
			row.LeaseReadError = e.Error()
		} else {
			l.Key = key.String
			l.Body = append(json.RawMessage(nil), raw...)
			row.Lease = &l
		}
		result = append(result, row)
	}
	return result
}
