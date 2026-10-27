//go:build arm_devtrace

package manager

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"getarm.org/internal/leaseclock"
	"getarm.org/internal/model"
	"getarm.org/internal/observe"
	"getarm.org/internal/pressure"
)

// Installed only by tagged tests for one original Manager. No request, wire
// field or environment variable can install this read-only observer. Both
// method boundaries already hold Manager.mu; no native collection or Manager
// reentry occurs here. Every retained value is JSON-frozen before returning.
type cpuWarningTraceOwner struct {
	Kind        string                 `json:"kind"`
	Pointer     string                 `json:"pointer"`
	Manager     string                 `json:"manager"`
	Identity    *model.ProcessIdentity `json:"identity"`
	Cookie      string                 `json:"cookie"`
	Exited      bool                   `json:"exited"`
	Context     string                 `json:"context"`
	Cancelled   bool                   `json:"cancelled"`
	Runtime     string                 `json:"runtime"`
	Done        string                 `json:"done"`
	Joined      bool                   `json:"joined"`
	Started     bool                   `json:"started"`
	Settled     bool                   `json:"settled"`
	ID          string                 `json:"id"`
	Key         string                 `json:"key"`
	Record      string                 `json:"record"`
	Volume      string                 `json:"volume"`
	BindingKey  string                 `json:"binding_key"`
	BindingSide string                 `json:"binding_side"`
	Disk        int64                  `json:"disk"`
}
type cpuWarningTraceSaved struct {
	SourceDone   string                `json:"source_done"`
	SourceJoined bool                  `json:"source_joined"`
	SourceExited bool                  `json:"source_exited"`
	Kind         string                `json:"kind"`
	Pointer      string                `json:"pointer"`
	Identity     model.ProcessIdentity `json:"identity"`
	Cookie       string                `json:"cookie"`
	Context      string                `json:"context"`
	Runtime      string                `json:"runtime"`
	Done         string                `json:"done"`
	Record       string                `json:"record"`
	Digest       string                `json:"digest"`
}
type cpuWarningTraceCredit struct {
	ID       string                `json:"id"`
	Job      string                `json:"job"`
	Identity model.ProcessIdentity `json:"identity"`
	Cookie   string                `json:"cookie"`
	Cores    float64               `json:"cores"`
}
type cpuWarningTraceBinding struct {
	Key    string `json:"key"`
	Side   string `json:"side"`
	Volume string `json:"volume"`
	Disk   int64  `json:"disk"`
}
type cpuWarningTraceLease struct {
	Row  int64   `json:"row"`
	ID   string  `json:"id"`
	Key  string  `json:"key"`
	Data string  `json:"data"`
	Run  *string `json:"run"`
}
type cpuWarningTraceRow struct {
	Epoch       string                            `json:"epoch"`
	Schema      string                            `json:"schema"`
	Scenario    string                            `json:"scenario"`
	Ordinal     int                               `json:"ordinal"`
	Call        int                               `json:"call"`
	Cut         string                            `json:"cut"`
	Manager     string                            `json:"manager"`
	Host        string                            `json:"host"`
	Owner       string                            `json:"owner"`
	Now         time.Time                         `json:"now"`
	Tick        time.Duration                     `json:"tick"`
	LeaseClock  leaseclock.Sample                 `json:"lease_clock"`
	ClockReadOK bool                              `json:"clock_read_ok"`
	CPU         model.CPUAvailability             `json:"cpu"`
	CurrentCPU  model.CPUAvailability             `json:"current_cpu"`
	Observed    time.Time                         `json:"observed"`
	ObservedAge time.Duration                     `json:"observed_age"`
	NativeAge   time.Duration                     `json:"native_age"`
	SampleTick  time.Duration                     `json:"sample_tick"`
	Frame       observe.CPUCreditFrame            `json:"frame"`
	Saved       map[string]cpuWarningTraceSaved   `json:"saved"`
	Current     map[string][]cpuWarningTraceOwner `json:"current"`
	Credits     []cpuWarningTraceCredit           `json:"credits"`
	Listed      []model.Lease                     `json:"listed"`
	Leases      []cpuWarningTraceLease            `json:"leases"`
	Bindings    map[string]cpuWarningTraceBinding `json:"bindings"`
	Events      int64                             `json:"events"`
	Failure     string                            `json:"failure"`
	Sample      *pressure.Sample                  `json:"sample"`
	Evidence    *model.CPUWarningEvidence         `json:"evidence"`
}
type cpuWarningTraceObserver struct {
	scenario string
	rows     []cpuWarningTraceRow
	bytes    int
	call     int
	failure  error
}

var cpuWarningTraceObservers sync.Map

func cpuWarningTraceClosed(ch <-chan struct{}) bool {
	if ch == nil {
		return false
	}
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
func cpuWarningTracePointer(v any) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprintf("%p", v)
	if s == "0x0" {
		return ""
	}
	return s
}
func (o *cpuWarningTraceObserver) append(row cpuWarningTraceRow) {
	if o.failure != nil {
		return
	}
	if row.Cut == "enter" {
		o.call++
	}
	row.Schema, row.Scenario, row.Ordinal, row.Call = "arm.cpu-warning-actual/v2", o.scenario, len(o.rows)+1, o.call
	raw, err := json.Marshal(row)
	if err != nil || len(raw) > 256<<10 || o.bytes+len(raw) > 4<<20 || len(o.rows) >= 32 {
		o.failure = fmt.Errorf("bounded warning trace unavailable: %v", err)
		return
	}
	var frozen cpuWarningTraceRow
	if err = json.Unmarshal(raw, &frozen); err != nil {
		o.failure = err
		return
	}
	o.rows = append(o.rows, frozen)
	o.bytes += len(raw)
}
func traceCPUWarningEnter(m *Manager, s cpuWarningSnapshot, now time.Time, tick time.Duration, ls []model.Lease, elapsed pressureElapsedView) {
	traceCPUWarningBoundary(m, s, now, tick, ls, elapsed, "enter", nil, nil)
}
func traceCPUWarningReturn(m *Manager, s cpuWarningSnapshot, now time.Time, tick time.Duration, ls []model.Lease, elapsed pressureElapsedView, sample pressure.Sample, e model.CPUWarningEvidence) (pressure.Sample, model.CPUWarningEvidence) {
	traceCPUWarningBoundary(m, s, now, tick, ls, elapsed, "return", &sample, &e)
	return sample, e
}
func traceCPUWarningBoundary(m *Manager, s cpuWarningSnapshot, now time.Time, tick time.Duration, ls []model.Lease, elapsed pressureElapsedView, cut string, sample *pressure.Sample, e *model.CPUWarningEvidence) {
	value, ok := cpuWarningTraceObservers.Load(m)
	if !ok {
		return
	}
	o := value.(*cpuWarningTraceObserver)
	row := cpuWarningTraceRow{Epoch: m.pressureEpoch, Cut: cut, Manager: cpuWarningTracePointer(m), Host: m.host, Owner: m.owner, Now: now, Tick: tick, LeaseClock: elapsed.Sample, ClockReadOK: elapsed.ReadOK, CPU: s.cpu, Observed: s.observed, ObservedAge: now.Sub(s.observed), NativeAge: now.Sub(s.cpu.At), SampleTick: s.tick, Frame: s.frame, Saved: map[string]cpuWarningTraceSaved{}, Current: map[string][]cpuWarningTraceOwner{}, Credits: []cpuWarningTraceCredit{}, Listed: append([]model.Lease{}, ls...), Leases: []cpuWarningTraceLease{}, Bindings: map[string]cpuWarningTraceBinding{}, Sample: sample, Evidence: e}
	m.observationMu.RLock()
	row.CurrentCPU = m.cpuAvailability
	m.observationMu.RUnlock()
	for id, saved := range s.owners {
		r := cpuWarningTraceSaved{Identity: saved.identity, Cookie: saved.cookie, Context: cpuWarningTracePointer(saved.ctx), Runtime: cpuWarningTracePointer(saved.runtime), Done: cpuWarningTracePointer(saved.done), Record: saved.transitRecord, Digest: hex.EncodeToString(saved.leaseDigest[:])}
		if saved.job != nil {
			r.Kind = "process"
			r.Pointer = cpuWarningTracePointer(saved.job)
			r.SourceDone = cpuWarningTracePointer(saved.job.done)
			r.SourceJoined = cpuWarningTraceClosed(saved.job.done)
			r.SourceExited = saved.job.cpuExited.Load()
		}
		if saved.transit != nil {
			r.Kind = "transit"
			r.Pointer = cpuWarningTracePointer(saved.transit)
		}
		if saved.inline != nil {
			r.Kind = "inline"
			r.Pointer = cpuWarningTracePointer(saved.inline)
		}
		row.Saved[id] = r
	}
	for _, c := range s.credits {
		row.Credits = append(row.Credits, cpuWarningTraceCredit{ID: c.id, Job: cpuWarningTracePointer(c.job), Identity: c.identity, Cookie: c.cookie, Cores: c.cores})
	}
	for id, j := range m.jobs {
		if j == nil {
			continue
		}
		r := cpuWarningTraceOwner{Kind: "process", Pointer: cpuWarningTracePointer(j), Cookie: j.cpuAnchor, Exited: j.cpuExited.Load(), Done: cpuWarningTracePointer(j.done), Joined: cpuWarningTraceClosed(j.done)}
		if j.identity != nil {
			v := *j.identity
			r.Identity = &v
		}
		row.Current[id] = append(row.Current[id], r)
	}
	for id, h := range m.relayTransits {
		if h == nil {
			continue
		}
		raw, err := json.Marshal(h.result)
		if err != nil {
			row.Failure = err.Error()
		}
		row.Current[id] = append(row.Current[id], cpuWarningTraceOwner{Kind: "transit", Pointer: cpuWarningTracePointer(h), Manager: cpuWarningTracePointer(h.manager), Context: cpuWarningTracePointer(h.ctx), Cancelled: h.ctx == nil || h.ctx.Err() != nil, Done: cpuWarningTracePointer(h.joined), Joined: cpuWarningTraceClosed(h.joined), Settled: h.settled, ID: h.id, Key: h.key, Record: string(raw)})
	}
	for id, h := range m.syncWorkers {
		if h == nil {
			continue
		}
		r := cpuWarningTraceOwner{Kind: "inline", Pointer: cpuWarningTracePointer(h), Manager: cpuWarningTracePointer(h.manager), Runtime: cpuWarningTracePointer(h.runtime), Done: cpuWarningTracePointer(h.done), Joined: cpuWarningTraceClosed(h.done), ID: h.lease, Record: string(h.leaseRecord), Volume: h.volume, BindingKey: h.intent.Key, BindingSide: string(h.intent.Side), Disk: h.disk}
		h.mu.Lock()
		r.Started = h.started
		h.mu.Unlock()
		if h.runtime != nil {
			r.Context = cpuWarningTracePointer(h.runtime.ctx)
			r.Cancelled = h.runtime.ctx == nil || h.runtime.ctx.Err() != nil
		}
		row.Current[id] = append(row.Current[id], r)
	}
	rows, err := m.db.Query(`SELECT rowid,id,request_key,data,run FROM leases ORDER BY rowid LIMIT 33`)
	if err != nil {
		row.Failure = err.Error()
	} else {
		for rows.Next() {
			var r cpuWarningTraceLease
			var key, run sql.NullString
			if err = rows.Scan(&r.Row, &r.ID, &key, &r.Data, &run); err != nil {
				row.Failure = err.Error()
				break
			}
			r.Key = key.String
			if run.Valid {
				v := run.String
				r.Run = &v
			}
			row.Leases = append(row.Leases, r)
		}
		if err = rows.Err(); err != nil {
			row.Failure = err.Error()
		}
		_ = rows.Close()
	}
	if len(row.Leases) > 32 || len(row.Current) > 32 || len(row.Saved) > 32 || len(row.Credits) > 32 {
		row.Failure = "warning trace roster exceeded"
	}
	rows, err = m.db.Query(`SELECT lease,key,side,volume,disk_bytes FROM sync_lease_bindings ORDER BY lease LIMIT 33`)
	if err != nil {
		row.Failure = err.Error()
	} else {
		for rows.Next() {
			var id string
			var r cpuWarningTraceBinding
			if err = rows.Scan(&id, &r.Key, &r.Side, &r.Volume, &r.Disk); err != nil {
				row.Failure = err.Error()
				break
			}
			row.Bindings[id] = r
		}
		if err = rows.Err(); err != nil {
			row.Failure = err.Error()
		}
		_ = rows.Close()
	}
	if err = m.db.QueryRow(`SELECT coalesce(max(sequence),0) FROM events`).Scan(&row.Events); err != nil {
		row.Failure = err.Error()
	}
	o.append(row)
}
