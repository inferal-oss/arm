//go:build arm_devtrace

package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/observe"
	"golang.org/x/crypto/ssh"
)

// Every emitted row comes from the actual cpuWarningSampleFrom entry/return
// observer. Native readings/candidates are controlled inputs; original native
// jobs, in-process owners, SQLite rows and their lifetime operations are real.
// No model state, expected verdict or projected Go result is produced here.
func TestCPUWarningActualTrace(t *testing.T) {
	scenarios := []string{"bare", "unknown", "process-credit", "process-cap-bare", "process-fraction", "process-frame", "process-cookie", "process-pointer", "process-joined", "process-expired", "process-retained", "transit-live", "transit-context", "transit-join", "transit-row", "inline-live", "inline-full", "inline-context", "inline-volume", "inline-cancel", "inline-joined", "inline-pointer", "inline-unstarted", "inline-after-snapshot", "lost-inline-restart", "physical-idle", "stale", "snapshot", "fractional", "sum17", "unsupported-native"}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			var m *Manager
			var lease model.Lease
			var process *job
			var transit *RelayTransit
			var inline *SyncWorkerHold
			var release chan struct{}
			switch {
			case scenario == "process-credit" || scenario == "process-cap-bare" || scenario == "process-fraction" || scenario == "process-frame" || scenario == "process-cookie" || scenario == "process-pointer" || scenario == "process-joined" || scenario == "process-expired" || scenario == "process-retained" || scenario == "physical-idle" || scenario == "unsupported-native":
				m, lease, process = cpuCreditFixture(t)
			case scenario == "transit-live" || scenario == "transit-context" || scenario == "transit-join" || scenario == "transit-row" || scenario == "sum17":
				m = newManager(t)
				memoryReading(m, 1024, 1024, 0)
				count, cores := 1, .25
				if scenario == "sum17" {
					count, cores = 17, .75/17
				}
				for n := range count {
					joined := make(chan struct{})
					h, err := m.AcquireRelayTransit(t.Context(), model.Request{Key: fmt.Sprintf("warning-trace-transit-%d", n), Reason: "controlled warning confidence trace", TTL: time.Minute, Resources: model.Resources{CPU: cores, Memory: 1}}, joined)
					if err != nil {
						t.Fatal(err)
					}
					transit = h
					t.Cleanup(func() {
						close(joined)
						if _, err := h.FinishAfterJoin(); err != nil {
							t.Error(err)
						}
					})
				}
			case scenario == "inline-live" || scenario == "inline-full" || scenario == "inline-context" || scenario == "inline-volume" || scenario == "inline-cancel" || scenario == "inline-joined" || scenario == "inline-pointer" || scenario == "inline-unstarted" || scenario == "inline-after-snapshot" || scenario == "lost-inline-restart":
				m, inline = reviewSyncHold(t)
				if scenario != "inline-unstarted" && scenario != "inline-after-snapshot" && scenario != "lost-inline-restart" {
					release = make(chan struct{})
					entered := make(chan struct{})
					ownedRelease := release
					if err := inline.Start(func(context.Context, ssh.Channel) error { close(entered); <-ownedRelease; return nil }); err != nil {
						t.Fatal(err)
					}
					<-entered
				}
			default:
				m = newManager(t)
			}
			if scenario == "process-cap-bare" {
				q := request("warning-unused-bare-budget")
				q.Resources.CPU = 2
				if _, err := m.Acquire(q, nil); err != nil {
					t.Fatal(err)
				}
			}
			o := &cpuWarningTraceObserver{scenario: scenario}
			cpuWarningTraceObservers.Store(m, o)
			defer cpuWarningTraceObservers.Delete(m)
			if release != nil {
				defer func() {
					if release != nil {
						close(release)
					}
				}()
			}
			now, tick := time.Now().UTC(), 100*time.Second
			capture := func(idle float64) cpuWarningSnapshot {
				setHostPressureCPU(m, now, tick, idle)
				if process != nil {
					amount := 1.
					if scenario == "process-cap-bare" {
						amount = 3
					}
					if scenario == "process-fraction" {
						amount = .25
					}
					setHostPressureCredit(m, lease, process, amount)
				}
				if scenario == "unknown" {
					m.observationMu.Lock()
					m.cpuAvailability.Known = false
					m.observationMu.Unlock()
				}
				if scenario == "unsupported-native" {
					m.observationMu.Lock()
					m.cpuCreditFrame.Eligible = false
					m.observationMu.Unlock()
				}
				m.observationMu.RLock()
				s := m.cpuWarningSnapshotLocked()
				m.observationMu.RUnlock()
				return s
			}
			call := func(s cpuWarningSnapshot) {
				m.mu.Lock()
				ls, err := m.list()
				if err == nil {
					_, _ = m.cpuWarningSampleFrom(s, now, tick, ls)
				}
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			idle := 0.
			if scenario == "transit-live" || scenario == "transit-context" || scenario == "transit-join" || scenario == "transit-row" || inline != nil {
				idle = .75
			}
			if scenario == "snapshot" {
				idle = 1
			}
			if scenario == "process-fraction" {
				idle = .75
			}
			s := capture(idle)
			if scenario == "fractional" {
				m.observationMu.Lock()
				capacity, busy, headroom := .0026, .0006, .002
				m.cpuAvailability.Capacity = &capacity
				m.cpuAvailability.Busy = &busy
				m.cpuAvailability.Available = &headroom
				s = m.cpuWarningSnapshotLocked()
				m.observationMu.Unlock()
			}
			if scenario == "inline-full" {
				m.observationMu.Lock()
				capacity, busy, headroom := .25, .25, 0.
				m.cpuAvailability.Capacity = &capacity
				m.cpuAvailability.Busy = &busy
				m.cpuAvailability.Available = &headroom
				s = m.cpuWarningSnapshotLocked()
				m.observationMu.Unlock()
			}
			if scenario == "inline-live" || scenario == "inline-full" {
				at := s.cpu.At
				m.observationMu.Lock()
				m.cpuCredits = []cpuCreditSample{{id: inline.lease, cores: 1}}
				m.cpuCreditFrame = observe.CPUCreditFrame{Eligible: true, Evidence: observe.CPUCreditEvidence{
					PreviousHostStarted: at.Add(-time.Second - 2*time.Millisecond), PreviousHostFinished: at.Add(-time.Second + time.Millisecond),
					PreviousPostStarted: at.Add(-time.Second + 2*time.Millisecond), PreviousPostFinished: at.Add(-time.Second + 3*time.Millisecond),
					CurrentPreStarted: at.Add(-4 * time.Millisecond), CurrentPreFinished: at.Add(-3 * time.Millisecond), HostStarted: at.Add(-2 * time.Millisecond), HostFinished: at.Add(time.Millisecond)}}
				s = m.cpuWarningSnapshotLocked()
				m.observationMu.Unlock()
			}
			call(s)
			switch scenario {
			case "bare":
				cpuReading(m, 4, 4, 0, 4, 0)
				q := request("warning-trace-bare")
				q.Resources.CPU = 1
				if _, err := m.Acquire(q, nil); err != nil {
					t.Fatal(err)
				}
				m.observationMu.Lock()
				m.cpuAvailability = s.cpu
				m.observationMu.Unlock()
				call(s)
			case "process-frame":
				s.frame.Eligible = false
				call(s)
			case "process-cookie":
				m.mu.Lock()
				old := process.cpuAnchor
				process.cpuAnchor = "changed-original-cookie"
				m.mu.Unlock()
				defer func() { m.mu.Lock(); process.cpuAnchor = old; m.mu.Unlock() }()
				call(s)
			case "process-pointer":
				m.mu.Lock()
				m.jobs[lease.ID] = &job{cmd: process.cmd, done: process.done, identity: process.identity, cpuAnchor: process.cpuAnchor}
				m.mu.Unlock()
				defer func() { m.mu.Lock(); m.jobs[lease.ID] = process; m.mu.Unlock() }()
				call(s)
			case "process-joined":
				if _, err := m.End(lease.ID, "released"); err != nil {
					t.Fatal(err)
				}
				select {
				case <-process.done:
				case <-time.After(5 * time.Second):
					t.Fatal("original process Wait did not join")
				}
				call(s)
			case "process-expired":
				m.mu.Lock()
				changed := lease
				expired := now.Add(-time.Second)
				changed.Expires = &expired
				err := m.put(changed, "trace.expired")
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { m.mu.Lock(); _ = m.put(lease, "trace.restore"); m.mu.Unlock() }()
				call(s)
			case "process-retained", "physical-idle":
				m.mu.Lock()
				delete(m.jobs, lease.ID)
				m.mu.Unlock()
				defer func() { m.mu.Lock(); m.jobs[lease.ID] = process; m.mu.Unlock() }()
				call(s)
				if scenario == "physical-idle" {
					now = now.Add(time.Second)
					tick += time.Second
					s = capture(1)
					call(s)
				}
			case "transit-context":
				m.mu.Lock()
				old := transit.ctx
				replacement, cancel := context.WithCancel(context.Background())
				transit.ctx = replacement
				m.mu.Unlock()
				defer func() { m.mu.Lock(); transit.ctx = old; m.mu.Unlock(); cancel() }()
				call(s)
			case "transit-join":
				m.mu.Lock()
				old := transit.joined
				transit.joined = make(chan struct{})
				m.mu.Unlock()
				defer func() { m.mu.Lock(); transit.joined = old; m.mu.Unlock() }()
				call(s)
			case "transit-row":
				m.mu.Lock()
				original, err := m.get(transit.id)
				if err == nil {
					changed := original
					changed.Request.Reason = "same revision changed transit row"
					err = m.put(changed, "trace.transit-replaced")
				}
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
				defer func() { m.mu.Lock(); _ = m.put(original, "trace.restore"); m.mu.Unlock() }()
				call(s)
			case "inline-context":
				m.mu.Lock()
				old := inline.runtime.ctx
				replacement, cancel := context.WithCancel(context.Background())
				inline.runtime.ctx = replacement
				m.mu.Unlock()
				defer func() { m.mu.Lock(); inline.runtime.ctx = old; m.mu.Unlock(); cancel() }()
				call(s)
			case "inline-volume":
				if _, err := m.db.Exec(`UPDATE sync_lease_bindings SET volume='changed-original-volume' WHERE lease=?`, inline.lease); err != nil {
					t.Fatal(err)
				}
				defer func() {
					_, _ = m.db.Exec(`UPDATE sync_lease_bindings SET volume=? WHERE lease=?`, inline.volume, inline.lease)
				}()
				call(s)
			case "inline-cancel":
				inline.cancel()
				call(s)
			case "inline-joined":
				close(release)
				release = nil
				<-inline.done
				call(s)
			case "inline-pointer":
				m.mu.Lock()
				m.syncWorkers[inline.lease] = &SyncWorkerHold{manager: m, lease: inline.lease, runtime: inline.runtime, done: inline.done, started: true, leaseRecord: inline.leaseRecord}
				m.mu.Unlock()
				defer func() { m.mu.Lock(); m.syncWorkers[inline.lease] = inline; m.mu.Unlock() }()
				call(s)
			case "inline-after-snapshot":
				release = make(chan struct{})
				entered := make(chan struct{})
				ownedRelease := release
				if err := inline.Start(func(context.Context, ssh.Channel) error { close(entered); <-ownedRelease; return nil }); err != nil {
					t.Fatal(err)
				}
				<-entered
				defer close(release)
				call(s)
			case "lost-inline-restart":
				cpuWarningTraceObservers.Delete(m)
				if err := m.Close(); err != nil {
					t.Fatal(err)
				}
				reopened, err := Open(m.home, m.capacity)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				m = reopened
				cpuWarningTraceObservers.Store(m, o)
				defer cpuWarningTraceObservers.Delete(m)
				now = now.Add(time.Second)
				tick += time.Second
				s = capture(0)
				call(s)
				now = now.Add(time.Second)
				tick += time.Second
				s = capture(1)
				call(s)
			case "stale":
				now = now.Add(5 * time.Second)
				tick += 5 * time.Second
				call(s)
			case "snapshot":
				setHostPressureCPU(m, now.Add(time.Second), tick+time.Second, 0)
				call(s)
			}
			if o.failure != nil {
				t.Fatal(o.failure)
			}
			if len(o.rows) < 2 || len(o.rows)%2 != 0 {
				t.Fatal("actual warning boundaries incomplete")
			}
			for _, r := range o.rows {
				raw, err := json.Marshal(r)
				if err != nil {
					t.Fatal(err)
				}
				t.Log("WARNING_ACTUAL " + string(raw))
			}
		})
	}
}
