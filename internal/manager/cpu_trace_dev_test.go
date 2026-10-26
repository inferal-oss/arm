//go:build arm_devtrace

package manager

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

// This producer exercises real admission/renewal/queue and durable SQL. Only
// native pressure readings are controlled, independently of native collectors.
// No expected model states, grant flags or permission verdicts are emitted.
func TestCPUAdmissionActualTrace(t *testing.T) {
	for _, scenario := range []string{"shrink-renew", "fifo-recovery", "unknown-stale"} {
		t.Run(scenario, func(t *testing.T) {
			m := newManager(t)
			m.capacity.CPU = 4
			o := &cpuTraceObserver{scenario: scenario}
			cpuTraceObservers.Store(m, o)
			defer cpuTraceObservers.Delete(m)
			capture := func(op cpuTraceOperation) {
				m.mu.Lock()
				defer m.mu.Unlock()
				var leases []cpuTraceSQLLease
				rows, e := m.db.Query(`SELECT rowid,id,request_key,data FROM leases ORDER BY rowid LIMIT 17`)
				if e != nil {
					t.Fatal(e)
				}
				defer rows.Close()
				reserved := 0.0
				for rows.Next() {
					var r cpuTraceSQLLease
					var key sql.NullString
					var raw []byte
					if e = rows.Scan(&r.Row, &r.ID, &key, &raw); e != nil {
						t.Fatal(e)
					}
					r.Body = append(json.RawMessage(nil), raw...)
					r.Key = key.String
					var l model.Lease
					if e = json.Unmarshal(r.Body, &l); e != nil {
						t.Fatal(e)
					}
					if consuming(l.State) {
						reserved += l.Request.Resources.CPU
					}
					leases = append(leases, r)
				}
				if e = rows.Err(); e != nil {
					t.Fatal(e)
				}
				if e = rows.Close(); e != nil {
					t.Fatal(e)
				}
				var events []cpuTraceSQLEvent
				rows, e = m.db.Query(`SELECT sequence,type,entity,data FROM events WHERE sequence>? AND (type GLOB 'lease.*' OR type='queue.changed') ORDER BY sequence LIMIT 65`, o.lastEvent)
				if e != nil {
					t.Fatal(e)
				}
				defer rows.Close()
				for rows.Next() {
					var r cpuTraceSQLEvent
					var raw []byte
					if e = rows.Scan(&r.Sequence, &r.Type, &r.ID, &raw); e != nil {
						t.Fatal(e)
					}
					r.Body = append(json.RawMessage(nil), raw...)
					o.lastEvent = r.Sequence
					events = append(events, r)
				}
				if e = rows.Err(); e != nil {
					t.Fatal(e)
				}
				if e = rows.Close(); e != nil {
					t.Fatal(e)
				}
				if len(leases) > 16 || len(events) > 64 {
					t.Fatal("CPU trace SQL roster exceeded bound")
				}
				b := m.cpuBudget(reserved)
				o.append(cpuTraceRow{Cut: "sql", Host: m.host, Owner: m.owner, Budget: b, Operation: &op, Leases: leases, Events: events})
			}
			observe := func(headroom float64, known bool, age time.Duration) {
				cpuReading(m, 4, 4, 4-headroom, headroom, age)
				m.observationMu.Lock()
				m.cpuAvailability.Known = known
				m.observationMu.Unlock()
				capture(cpuTraceOperation{Action: "observe"})
			}
			acquire := func(key string, cpu float64, wait bool) model.Lease {
				q := request(key)
				q.Resources.CPU = cpu
				q.Queue = wait
				l, e := m.Acquire(q, nil)
				capture(cpuTraceOperation{Action: "acquire", Key: key, ID: l.ID, CPU: cpu, Error: errorCode(e)})
				return l
			}
			renew := func(l model.Lease) {
				_, e := m.Renew(l.ID, 2*time.Minute)
				capture(cpuTraceOperation{Action: "renew", Key: l.Request.Key, ID: l.ID, CPU: l.Request.Resources.CPU, Error: errorCode(e)})
			}
			end := func(l model.Lease) {
				_, e := m.End(l.ID, "released")
				capture(cpuTraceOperation{Action: "end", Key: l.Request.Key, ID: l.ID, CPU: l.Request.Resources.CPU, Error: errorCode(e)})
			}
			// First actual SQL/budget snapshot matches the model's empty unknown
			// baseline. Subsequent sample changes are actual controlled observations.
			cpuReading(m, 4, 4, 4, 0, 7*time.Second)
			m.observationMu.Lock()
			m.cpuAvailability.Known = false
			m.observationMu.Unlock()
			capture(cpuTraceOperation{Action: "initial"})
			switch scenario {
			case "shrink-renew":
				observe(4, true, 0)
				a := acquire("A", 2, false)
				observe(1, true, 0)
				renew(a)
				observe(2, true, 0)
				renew(a)
				end(a)
			case "fifo-recovery":
				observe(1, true, 0)
				a := acquire("A", 2, true)
				b := acquire("B", 1, true)
				m.tick()
				capture(cpuTraceOperation{Action: "tick"})
				observe(4, true, 0)
				m.tick()
				capture(cpuTraceOperation{Action: "tick"})
				end(a)
				end(b)
			case "unknown-stale":
				observe(4, false, 0)
				acquire("A", 2, false)
				observe(4, true, 7*time.Second)
				acquire("A", 2, false)
				// Zero-CPU work is independent of CPU availability.
				z, e := m.Acquire(model.Request{Key: "Z", Reason: "unrelated memory", Resources: model.Resources{Memory: 1}, TTL: time.Minute}, nil)
				capture(cpuTraceOperation{Action: "zero", Key: "Z", ID: z.ID, Error: errorCode(e)})
				observe(4, true, 0)
				a := acquire("A", 2, false)
				end(a)
				end(z)
			}
			o.mu.Lock()
			defer o.mu.Unlock()
			if o.failure != nil {
				t.Fatal(o.failure)
			}
			for _, row := range o.rows {
				raw, e := json.Marshal(row)
				if e != nil {
					t.Fatal(e)
				}
				t.Log("CPU_ACTUAL " + string(raw))
			}
		})
	}
}
