package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/pressure"
)

func setHostPressureCPU(m *Manager, now time.Time, tick time.Duration, idle float64) {
	eligible, capacity, busy := 1, 1.0, 1-idle
	owners := m.captureCPUWarningOwners()
	m.observationMu.Lock()
	m.cpuObserved = now
	m.cpuWarningOwners = owners
	m.cpuAvailability = model.CPUAvailability{Known: true, At: now, Interval: time.Second, EligibleCPUs: &eligible,
		Capacity: &capacity, Busy: &busy, Available: &idle, Scope: "controlled-test-host", Method: "controlled-counter-interval"}
	m.cpuPressureTick = tick
	m.memoryAvailability = model.MemoryAvailability{}
	m.diskAvailability = model.DiskAvailability{}
	m.observationMu.Unlock()
}

func claimedHostPressureWarning(t *testing.T, m *Manager) (string, hostPressureDelivery, string, model.Lease) {
	t.Helper()
	c, err := m.RegisterContact(agents.Contact{ID: "pressure-claim-agent", Adapter: "codex", Session: local.ID(), CWD: t.TempDir(), Delivery: "native"})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 4, 4, 0, 4, 0)
	r := request(local.ID())
	r.Agent, r.Resources.CPU = c.ID, 0.5
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	base, tick := time.Now().UTC(), time.Duration(100)*time.Second
	for _, sec := range []int{0, 6} {
		now, observed := base.Add(time.Duration(sec)*time.Second), tick+time.Duration(sec)*time.Second
		setHostPressureCPU(m, now, observed, 0.1)
		if err = m.advanceHostPressureAt(now, observed); err != nil {
			t.Fatal(err)
		}
	}
	id, item, body, ok := m.claimHostPressureDelivery()
	if !ok || item.Lease != l.ID {
		t.Fatalf("fixture failed to claim exact warning: %t %+v", ok, item)
	}
	return id, item, body, l
}

func TestHostPressureStartRechecksClaimedAuthority(t *testing.T) {
	for _, scenario := range []string{"current", "withdrawn", "replaced", "released", "recovered", "incident-ended"} {
		t.Run(scenario, func(t *testing.T) {
			m := newManager(t)
			id, item, body, l := claimedHostPressureWarning(t, m)
			switch scenario {
			case "withdrawn":
				if _, err := m.db.Exec(`DELETE FROM agents WHERE id=?`, item.Target.ID); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				replacement := item.Target
				replacement.Session, replacement.Registration = local.ID(), local.ID()
				raw, _ := json.Marshal(replacement)
				if _, err := m.db.Exec(`UPDATE agents SET data=? WHERE id=?`, string(raw), item.Target.ID); err != nil {
					t.Fatal(err)
				}
			case "released":
				if _, err := m.End(l.ID, "released"); err != nil {
					t.Fatal(err)
				}
			case "recovered":
				if err := m.recoverDeliveries(); err != nil {
					t.Fatal(err)
				}
				if err := m.loadHostPressure(); err != nil {
					t.Fatal(err)
				}
			case "incident-ended":
				m.mu.Lock()
				e := m.pressureState.Resources[item.Reason]
				e.Active = false
				m.pressureState.Resources[item.Reason] = e
				m.mu.Unlock()
			}
			dir := t.TempDir()
			marker := filepath.Join(dir, "launched")
			script := []byte("#!/bin/sh\nprintf launched > " + strconv.Quote(marker) + "\n")
			if err := os.WriteFile(filepath.Join(dir, "codex"), script, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			d := agents.DeliverWithStartFence(context.Background(), item.Target, body, func(start func() error) (bool, error) {
				return m.hostPressureStartFence(id, item, start)
			})
			_, statErr := os.Stat(marker)
			if scenario == "current" {
				if d.State != "queued" || statErr != nil {
					t.Fatalf("current exact claim did not launch: %+v %v", d, statErr)
				}
			} else if d.State != "superseded" || !os.IsNotExist(statErr) {
				t.Fatalf("stale %s claim launched or misreported: %+v %v", scenario, d, statErr)
			}
		})
	}
}

func TestHostPressureContactReplacementDuringCommandPreparationDoesNotLaunch(t *testing.T) {
	m := newManager(t)
	id, item, body, _ := claimedHostPressureWarning(t, m)
	dir := t.TempDir()
	marker := filepath.Join(dir, "launched")
	script := []byte("#!/bin/sh\nprintf launched > " + strconv.Quote(marker) + "\n")
	if err := os.WriteFile(filepath.Join(dir, "codex"), script, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	prepared, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	result := make(chan agents.Delivery, 1)
	go func() {
		result <- agents.DeliverWithStartFence(context.Background(), item.Target, body, func(start func() error) (bool, error) {
			close(prepared) // Agent command was prepared; native Start has not run.
			<-release
			return m.hostPressureStartFence(id, item, start)
		})
	}()
	<-prepared
	replacement := item.Target
	replacement.Session, replacement.Registration = local.ID(), local.ID()
	raw, _ := json.Marshal(replacement)
	m.mu.Lock()
	_, err := m.db.Exec(`UPDATE agents SET data=? WHERE id=?`, string(raw), item.Target.ID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	unblock()
	d := <-result
	if d.State != "superseded" {
		t.Fatalf("replaced contact received native invocation: %+v", d)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("replaced contact launched native command: %v", err)
	}
}

func TestHostPressureCoverageAndDurableWarning(t *testing.T) {
	m := newManager(t)
	contact, err := m.RegisterContact(agents.Contact{ID: "pressure-agent", Adapter: "codex", Session: local.ID(), CWD: t.TempDir(), Delivery: "native"})
	if err != nil {
		t.Fatal(err)
	}
	r := request("host-pressure-covered")
	r.Agent = contact.ID
	r.Resources.CPU = 0.5
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sleep", "30"}, CWD: filepath.Dir(m.home)})
	if err != nil {
		t.Fatal(err)
	}
	j := m.jobs[l.ID]
	if j.cpuAnchor == "" {
		j.cpuAnchor = "controlled-warning-owner"
	}
	base, tick := time.Now().UTC(), time.Duration(100)*time.Second
	setHostPressureCPU(m, base, tick, 0.5)
	setHostPressureCredit(m, l, j, 0.5)
	m.mu.Lock()
	in, p, err := m.hostPressureInput(base, tick)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if got := in.Samples[pressure.CPU].Available; !in.Samples[pressure.CPU].Known || got < 900 || got > 1000 {
		t.Fatalf("compliant managed use exposed as pressure: %d", got)
	}
	var state pressure.State
	state, actions, err := pressure.Advance(state, in, p)
	if err != nil || len(actions) != 0 {
		t.Fatalf("healthy managed work produced incident: %v %+v", err, actions)
	}
	setHostPressureCPU(m, base.Add(6*time.Second), tick+6*time.Second, 0.5)
	setHostPressureCredit(m, l, j, 0.5)
	m.mu.Lock()
	in, p, err = m.hostPressureInput(base.Add(6*time.Second), tick+6*time.Second)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	state, actions, err = pressure.Advance(state, in, p)
	if err != nil || len(actions) != 0 || state.Resources[pressure.CPU].Active {
		t.Fatalf("compliant busy ARM work warned: %v %+v %+v", err, actions, state.Resources[pressure.CPU])
	}

	// A fresh external busy fraction beyond the promised half-core produces a
	// sustained coverage shortage, without asserting a per-lease violation.
	setHostPressureCPU(m, base.Add(10*time.Second), tick+10*time.Second, 0.1)
	if err = m.advanceHostPressureAt(base.Add(10*time.Second), tick+10*time.Second); err != nil {
		t.Fatal(err)
	}
	setHostPressureCPU(m, base.Add(16*time.Second), tick+16*time.Second, 0.1)
	if err = m.advanceHostPressureAt(base.Add(16*time.Second), tick+16*time.Second); err != nil {
		t.Fatal(err)
	}
	var stateRaw []byte
	if err = m.db.QueryRow(`SELECT data FROM host_pressure_state WHERE id='current'`).Scan(&stateRaw); err != nil {
		t.Fatal(err)
	}
	var stored pressure.State
	if err = json.Unmarshal(stateRaw, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Resources[pressure.CPU].Active || stored.Resources[pressure.CPU].WarningCount != 1 || len(stored.Pauses) != 0 {
		t.Fatalf("wrong durable warning state: %+v", stored)
	}
	for table, want := range map[string]int{"messages": 2, "agent_inbox": 1, "host_pressure_outbox": 1, "usage_incidents": 0} {
		var got int
		if err = m.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
	var msgRaw []byte
	if err = m.db.QueryRow(`SELECT data FROM messages WHERE json_extract(data,'$.audience')=?`, contact.ID).Scan(&msgRaw); err != nil {
		t.Fatal(err)
	}
	var msg Message
	if err = json.Unmarshal(msgRaw, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Lease != l.ID || msg.Deliveries[contact.ID].State != "pending" || msg.Deliveries[contact.ID].Registration != contact.Registration {
		t.Fatalf("warning delivery is not exact: %+v", msg)
	}
	if _, _, _, ok := m.claimHostPressureDelivery(); !ok {
		t.Fatal("current exact native warning was not claimable")
	}
	// An interrupted native attempt is unknown and cannot be replayed.
	if err = m.recoverDeliveries(); err != nil {
		t.Fatal(err)
	}
	if err = m.loadHostPressure(); err != nil {
		t.Fatal(err)
	}
	if err = m.db.QueryRow(`SELECT data FROM messages WHERE id=?`, msg.ID).Scan(&msgRaw); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(msgRaw, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Deliveries[contact.ID].State != "unknown" {
		t.Fatalf("interrupted native delivery was reported as %+v", msg.Deliveries[contact.ID])
	}
	if _, _, _, ok := m.claimHostPressureDelivery(); ok {
		t.Fatal("uncertain native warning was replayed")
	}
	var outboxState string
	if err = m.db.QueryRow(`SELECT state FROM host_pressure_outbox`).Scan(&outboxState); err != nil {
		t.Fatal(err)
	}
	if outboxState != "unknown" {
		t.Fatalf("interrupted native attempt is %q", outboxState)
	}
}

func TestHostPressureFullyAllocatedManagedCPUIsHealthy(t *testing.T) {
	m, l, j := cpuCreditFixture(t)
	base, tick := time.Now().UTC(), 100*time.Second
	var state pressure.State
	for _, sec := range []int{0, 6, 12, 18} {
		now, observed := base.Add(time.Duration(sec)*time.Second), tick+time.Duration(sec)*time.Second
		setHostPressureCPU(m, now, observed, 0)
		setHostPressureCredit(m, l, j, 1)
		m.mu.Lock()
		in, p, err := m.hostPressureInput(now, observed)
		m.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		if !in.Samples[pressure.CPU].Known || in.Samples[pressure.CPU].Available < 900 {
			t.Fatal("verified use did not establish recovery", in.Samples[pressure.CPU])
		}
		var actions []pressure.Action
		state, actions, err = pressure.Advance(state, in, p)
		if err != nil || len(actions) != 0 || state.Resources[pressure.CPU].Active {
			t.Fatal("verified compliant work warned", err, actions, state)
		}
	}
}

func TestHostPressureUnusedBarePromiseDoesNotMaskHostBusy(t *testing.T) {
	m := newManager(t)
	r := request("unused-bare-promise")
	r.Resources.CPU = 0.5
	if _, err := m.Acquire(r, nil); err != nil {
		t.Fatal(err)
	}
	base, tick := time.Now().UTC(), 100*time.Second
	var state pressure.State
	warned := false
	for _, sec := range []int{0, 6, 12} {
		now, observed := base.Add(time.Duration(sec)*time.Second), tick+time.Duration(sec)*time.Second
		setHostPressureCPU(m, now, observed, 0.5)
		m.mu.Lock()
		in, p, err := m.hostPressureInput(now, observed)
		ls, listErr := m.list()
		_, e := m.cpuWarningSample(now, observed, ls)
		m.mu.Unlock()
		if listErr != nil {
			t.Fatal(listErr)
		}
		if err != nil {
			t.Fatal(err)
		}
		if e.VerifiedManaged != 0 || e.PossibleCompliant != 0 || !in.Samples[pressure.CPU].Known || in.Samples[pressure.CPU].Available != 500 {
			t.Fatal("bare promise manufactured coverage", e, in.Samples[pressure.CPU])
		}
		var actions []pressure.Action
		state, actions, err = pressure.Advance(state, in, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range actions {
			warned = warned || a.Kind == pressure.WarnOperator
		}
	}
	if !warned || !state.Resources[pressure.CPU].Active {
		t.Fatal("bare promise masked sustained busy host", state)
	}
}

func TestHostPressureWarningTransactionRollsBack(t *testing.T) {
	m := newManager(t)
	c, err := m.RegisterContact(agents.Contact{ID: "rollback-agent", Adapter: "codex", Session: local.ID(), CWD: t.TempDir(), Delivery: "native"})
	if err != nil {
		t.Fatal(err)
	}
	r := request("host-pressure-rollback")
	r.Agent, r.Resources.CPU = c.ID, 0.5
	if _, err = m.Acquire(r, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`CREATE TRIGGER pressure_fail BEFORE INSERT ON host_pressure_outbox BEGIN SELECT RAISE(ABORT,'forced outbox failure'); END`); err != nil {
		t.Fatal(err)
	}
	base, tick := time.Now().UTC(), time.Duration(100)*time.Second
	setHostPressureCPU(m, base, tick, 0.1)
	if err = m.advanceHostPressureAt(base, tick); err != nil {
		t.Fatal(err)
	}
	setHostPressureCPU(m, base.Add(6*time.Second), tick+6*time.Second, 0.1)
	if err = m.advanceHostPressureAt(base.Add(6*time.Second), tick+6*time.Second); err == nil {
		t.Fatal("forced outbox failure did not abort warning transition")
	}
	for table, want := range map[string]int{"host_pressure_state": 1, "messages": 0, "agent_inbox": 0, "host_pressure_outbox": 0} {
		var got int
		if err = m.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows after rollback = %d, want %d", table, got, want)
		}
	}
	if m.pressureState.Resources[pressure.CPU].WarningCount != 0 {
		t.Fatal("in-memory pressure state advanced after transaction rollback")
	}
}

func TestHostPressureDrainPreservesWarningAndStopsNewDispatch(t *testing.T) {
	m := newManager(t)
	c, err := m.RegisterContact(agents.Contact{ID: "draining-agent", Adapter: "codex", Session: local.ID(), CWD: t.TempDir(), Delivery: "native"})
	if err != nil {
		t.Fatal(err)
	}
	r := request("host-pressure-drain")
	r.Agent, r.Resources.CPU = c.ID, 0.5
	if _, err = m.Acquire(r, nil); err != nil {
		t.Fatal(err)
	}
	base, tick := time.Now().UTC(), time.Duration(100)*time.Second
	for _, sec := range []int{0, 6} {
		now, observed := base.Add(time.Duration(sec)*time.Second), tick+time.Duration(sec)*time.Second
		setHostPressureCPU(m, now, observed, 0.1)
		if err = m.advanceHostPressureAt(now, observed); err != nil {
			t.Fatal(err)
		}
	}
	before := m.pressureState.Resources[pressure.CPU]
	if before.WarningCount != 1 {
		t.Fatalf("fixture did not queue a warning: %+v", before)
	}
	var messages, inbox, queued int
	for query, out := range map[string]*int{
		`SELECT COUNT(*) FROM messages`:                                  &messages,
		`SELECT COUNT(*) FROM agent_inbox`:                               &inbox,
		`SELECT COUNT(*) FROM host_pressure_outbox WHERE state='queued'`: &queued,
	} {
		if err = m.db.QueryRow(query).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	if messages != 2 || inbox != 1 || queued != 1 {
		t.Fatalf("warning evidence missing before drain: messages=%d inbox=%d queued=%d", messages, inbox, queued)
	}
	report, err := m.BeginShutdown(model.ShutdownRequest{Wait: true, Timeout: time.Hour})
	if err != nil || report.Phase != "draining" {
		t.Fatalf("wait shutdown did not begin: %+v %v", report, err)
	}
	setHostPressureCPU(m, base.Add(12*time.Second), tick+12*time.Second, 0.1)
	if err = m.advanceHostPressureAt(base.Add(12*time.Second), tick+12*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := m.pressureState.Resources[pressure.CPU]; got != before {
		t.Fatalf("drain advanced warning episode: before=%+v after=%+v", before, got)
	}
	if _, _, _, ok := m.claimHostPressureDelivery(); ok {
		t.Fatal("draining backend claimed new native delivery")
	}
	started := false
	allowed, startErr := m.hostPressureStartFence("", hostPressureDelivery{}, func() error { started = true; return nil })
	if startErr != nil || allowed || started {
		t.Fatalf("native command start escaped drain fence: allowed=%t started=%t err=%v", allowed, started, startErr)
	}
	for query, want := range map[string]int{
		`SELECT COUNT(*) FROM messages`:                                  messages,
		`SELECT COUNT(*) FROM agent_inbox`:                               inbox,
		`SELECT COUNT(*) FROM host_pressure_outbox WHERE state='queued'`: queued,
	} {
		var got int
		if err = m.db.QueryRow(query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("drain changed durable warning evidence for %q: %d, want %d", query, got, want)
		}
	}
}

func TestHostPressureRemoteNodeStopPreservesWarningAndFencesLaunch(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	c, err := m.RegisterContact(agents.Contact{ID: "remote-stop-agent", Adapter: "codex", Session: local.ID(), CWD: t.TempDir(), Delivery: "native"})
	if err != nil {
		t.Fatal(err)
	}
	cpuReading(m, 4, 4, 0, 4, 0)
	r := request("host-pressure-remote-stop")
	r.Agent, r.Resources.CPU = c.ID, 0.5
	if _, err = m.Acquire(r, nil); err != nil {
		t.Fatal(err)
	}
	base, tick := time.Now().UTC(), time.Duration(100)*time.Second
	for _, sec := range []int{0, 6} {
		now, observed := base.Add(time.Duration(sec)*time.Second), tick+time.Duration(sec)*time.Second
		setHostPressureCPU(m, now, observed, 0.1)
		if err = m.advanceHostPressureAt(now, observed); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.pressureState.Resources[pressure.CPU].WarningCount; got != 1 {
		t.Fatalf("fixture did not produce warning: %d", got)
	}
	q := stopApplication(t, m, actor, external, model.ShutdownRequest{Wait: true})
	if _, err = m.BeginNodeStop(q, actor, true, external); err != nil {
		t.Fatal(err)
	}
	if !m.draining {
		t.Fatal("accepted remote stop did not drain")
	}
	setHostPressureCPU(m, base.Add(12*time.Second), tick+12*time.Second, 0.1)
	if err = m.advanceHostPressureAt(base.Add(12*time.Second), tick+12*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := m.pressureState.Resources[pressure.CPU].WarningCount; got != 1 {
		t.Fatalf("remote stop emitted another warning: %d", got)
	}
	if _, _, _, ok := m.claimHostPressureDelivery(); ok {
		t.Fatal("accepted remote stop claimed native warning delivery")
	}
	started := false
	allowed, startErr := m.hostPressureStartFence("", hostPressureDelivery{}, func() error { started = true; return nil })
	if startErr != nil || allowed || started {
		t.Fatalf("remote stop permitted native command start: allowed=%t started=%t err=%v", allowed, started, startErr)
	}
	var messages, inbox, queued int
	for query, out := range map[string]*int{
		`SELECT COUNT(*) FROM messages`:                                  &messages,
		`SELECT COUNT(*) FROM agent_inbox`:                               &inbox,
		`SELECT COUNT(*) FROM host_pressure_outbox WHERE state='queued'`: &queued,
	} {
		if err = m.db.QueryRow(query).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	if messages != 2 || inbox != 1 || queued != 1 {
		t.Fatalf("remote stop changed historical warning evidence: messages=%d inbox=%d queued=%d", messages, inbox, queued)
	}
}

func TestHostPressureRemoteNodeStopWaitsForNativeStartFenceOnly(t *testing.T) {
	m, actor, external := stopManagerFixture(t)
	id, item, _, _ := claimedHostPressureWarning(t, m)
	q := stopApplication(t, m, actor, external, model.ShutdownRequest{Wait: true})
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	startResult := make(chan error, 1)
	go func() {
		allowed, err := m.hostPressureStartFence(id, item, func() error {
			close(entered)
			<-release
			return nil
		})
		if !allowed && err == nil {
			err = fmt.Errorf("native start was unexpectedly denied")
		}
		startResult <- err
	}()
	<-entered
	stopResult := make(chan error, 1)
	go func() {
		_, err := m.BeginNodeStop(q, actor, true, external)
		stopResult <- err
	}()
	select {
	case err := <-stopResult:
		unblock()
		<-startResult
		t.Fatalf("remote stop crossed launch fence before command start returned: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	if err := <-startResult; err != nil {
		t.Fatal(err)
	}
	if err := <-stopResult; err != nil {
		t.Fatal(err)
	}
	if !m.draining {
		t.Fatal("remote stop did not drain after native start returned")
	}
}

func TestHostPressureUnknownSampleCannotWarn(t *testing.T) {
	m := newManager(t)
	base, tick := time.Now().UTC(), time.Duration(100)*time.Second
	setHostPressureCPU(m, base, tick, 0)
	m.observationMu.Lock()
	m.cpuPressureTick = -1 // failed suspend-inclusive clock collection
	m.observationMu.Unlock()
	m.mu.Lock()
	in, p, err := m.hostPressureInput(base.Add(10*time.Second), tick+10*time.Second)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if in.Samples[pressure.CPU].Known {
		t.Fatal("unknown native tick became fresh pressure")
	}
	_, actions, err := pressure.Advance(pressure.State{}, in, p)
	if err != nil || len(actions) != 0 {
		t.Fatalf("unknown sample triggered action: %v %+v", err, actions)
	}
}
