//go:build arm_devtrace

package manager

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/model"
)

// This producer exercises real admission/renewal/queue and durable SQL. Only
// native pressure readings are controlled, independently of native collectors.
// No expected model states, grant flags or permission verdicts are emitted.
func TestCPUAdmissionActualTrace(t *testing.T) {
	for _, scenario := range []string{"shrink-renew", "fifo-recovery", "unknown-stale", "managed-credit", "credit-invalidation"} {
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
				o.append(cpuTraceRow{Cut: "sql", Host: m.host, Owner: m.owner, Budget: b, Operation: &op, Leases: leases, Events: events, Credits: cpuTraceCreditInputs(m)})
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
				m.mu.Lock()
				j := m.jobs[l.ID]
				m.mu.Unlock()
				_, e := m.End(l.ID, "released")
				op := cpuTraceOperation{Action: "end", Key: l.Request.Key, ID: l.ID, CPU: l.Request.Resources.CPU, Error: errorCode(e)}
				if j != nil {
					op.JobGeneration = fmt.Sprintf("%p", j)
					timer := time.NewTimer(5 * time.Second)
					select {
					case <-j.done:
						op.Joined = true
					case <-timer.C:
						t.Fatal("original CPU workload owner did not join")
					}
					timer.Stop()
				}
				capture(op)
			}
			run := func() (model.Lease, *job) {
				q := request("A")
				q.Resources.CPU = 2
				l, e := m.Acquire(q, &RunRequest{Request: q, Command: []string{"/bin/sleep", "30"}, CWD: filepath.Dir(m.home)})
				capture(cpuTraceOperation{Action: "run", Key: q.Key, ID: l.ID, CPU: 2, Error: errorCode(e)})
				if e != nil {
					t.Fatal(e)
				}
				m.mu.Lock()
				j := m.jobs[l.ID]
				m.mu.Unlock()
				if j == nil || j.identity == nil || l.Process == nil || *l.Process != *j.identity {
					t.Fatal("missing original owned job", l)
				}
				m.mu.Lock()
				if j.cpuAnchor == "" {
					m.mu.Unlock()
					t.Fatal("actual controlled-credit producer requires native original launch anchor (Darwin)")
				}
				m.mu.Unlock()
				return l, j
			}
			credit := func(l model.Lease, j *job, cores float64) {
				m.observationMu.Lock()
				m.cpuCredits = []cpuCreditSample{{id: l.ID, job: j, identity: *j.identity, cookie: j.cpuAnchor, cores: cores}}
				m.observationMu.Unlock()
				capture(cpuTraceOperation{Action: "observe"})
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
			case "managed-credit":
				observe(4, true, 0)
				a, j := run()
				observe(2, true, 0)
				credit(a, j, 1.75)
				m.observationMu.Lock()
				m.cpuCredits = append(m.cpuCredits, m.cpuCredits[0])
				m.observationMu.Unlock()
				capture(cpuTraceOperation{Action: "observe"})
				renew(a)
				b := acquire("B", 1, false)
				renew(a)
				observe(0, true, 0)
				credit(a, j, 5) // candidate cannot borrow B's idle promise
				renew(a)
				observe(2, true, 0)
				end(b)
				end(a)
			case "credit-invalidation":
				observe(4, true, 0)
				a, j := run()
				observe(1, true, 0)
				credit(a, j, 1.75)
				renew(a)
				m.mu.Lock()
				replacement := &job{cmd: j.cmd, done: make(chan struct{}), identity: j.identity, cpuAnchor: j.cpuAnchor}
				m.jobs[a.ID] = replacement
				m.mu.Unlock()
				defer func() {
					m.mu.Lock()
					defer m.mu.Unlock()
					if m.jobs[a.ID] == replacement {
						m.jobs[a.ID] = j
					}
				}()
				capture(cpuTraceOperation{Action: "invalidate", Key: "A", ID: a.ID, CPU: 2})
				renew(a)
				m.mu.Lock()
				m.jobs[a.ID] = j
				m.mu.Unlock()
				capture(cpuTraceOperation{Action: "observe"})
				observe(1, true, 7*time.Second)
				renew(a)
				observe(1, true, 0)
				b := acquire("B", 1, true)
				renew(a)
				end(a)
				m.tick()
				capture(cpuTraceOperation{Action: "tick"})
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
