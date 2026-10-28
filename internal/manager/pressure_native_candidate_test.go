package manager

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/childowner"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/pressure"
)

func stagedPressureAction(kind pressure.ActionKind, candidate pressureNativeCandidate, epoch string, revision uint64) pressure.Action {
	id := candidate.id
	return pressure.Action{Kind: kind, Reason: pressure.CPU, Incident: 1, Job: id, Epoch: epoch, Revision: revision,
		PolicyDigest: hex.EncodeToString(candidate.policyDigest[:]),
		Key: fmt.Sprintf("%q/%q/%q/%d/%d/%d/%q/%q/%q/%q", epoch, kind, pressure.CPU, 1, 0, revision,
			id.LeaseID, id.ExecutionID, id.Generation, id.OwnershipID)}
}

func persistStagedPressureState(t *testing.T, m *Manager, s pressure.State) {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`INSERT INTO host_pressure_state(id,data) VALUES('current',?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, string(raw)); err != nil {
		t.Fatal(err)
	}
	m.pressureState = s
}

func TestPressureOriginalNativeCandidateRequiresCurrentOwnerAndExplicitPolicy(t *testing.T) {
	m := newManager(t)
	r := request(local.ID())
	r.Resources.CPU = 0.01
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sleep", "30"}, CWD: filepath.Dir(m.home)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.End(l.ID, "released") })

	m.mu.Lock()
	defer m.mu.Unlock()
	p := hostPressurePolicy(model.CPUAvailability{}, model.MemoryAvailability{}, model.DiskAvailability{})
	if _, ok := m.pressureOriginalNativeCandidate(l.ID, p); ok {
		t.Fatal("disabled native pause policy selected a process")
	}
	threshold := p.Resources[pressure.CPU]
	threshold.AllowPause = true
	p.Resources[pressure.CPU] = threshold
	p.MaxPauseJobs = 1
	if _, ok := m.pressureOriginalNativeCandidate(l.ID, p); ok {
		t.Fatal("policy alone granted native pause without an original-bound capability")
	}
	if !m.bindPressureNativeGrant(l.ID, p) {
		t.Fatal("resolved CPU policy did not bind the current original owner")
	}
	candidate, ok := m.pressureOriginalNativeCandidate(l.ID, p)
	if !ok || candidate.job != m.jobs[l.ID] || candidate.id.LeaseID != l.ID || candidate.id.Generation == "" || candidate.remaining <= 0 ||
		!m.pressureCandidateStillCurrent(candidate, p) {
		t.Fatal("live original owner was not selected by explicit policy")
	}
	changedPolicy := p
	changedPolicy.Grace++
	if m.pressureCandidateStillCurrent(candidate, changedPolicy) {
		t.Fatal("changed resolved policy retained stale control eligibility")
	}

	original := m.jobs[l.ID]
	m.jobs[l.ID] = &job{}
	if m.pressureCandidateStillCurrent(candidate, p) {
		t.Fatal("replaced original owner retained stale control eligibility")
	}
	m.jobs[l.ID] = original

	l.Revision++
	if err := m.put(l, "fixture.pressure-revision"); err != nil {
		t.Fatal(err)
	}
	if m.pressureCandidateStillCurrent(candidate, p) {
		t.Fatal("changed lease revision retained stale control eligibility")
	}
	if !m.bindPressureNativeGrant(l.ID, p) {
		t.Fatal("fresh lease revision did not rebind its exact original owner")
	}
	current, ok := m.pressureOriginalNativeCandidate(l.ID, p)
	if !ok || current.leaseRevision != l.Revision || current.id != candidate.id {
		t.Fatal("same original owner lost identity after a fresh lease revision")
	}
	if original.childOwner == nil || !original.childOwner.Active() {
		t.Fatal("candidate test did not retain its original unreaped child owner")
	}
}

func TestPressureNativeControlRefusesUnjournaledAction(t *testing.T) {
	m := newManager(t)
	r := request(local.ID())
	r.Resources.CPU = 0.01
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sleep", "30"}, CWD: filepath.Dir(m.home)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.End(l.ID, "released") })
	p := hostPressurePolicy(model.CPUAvailability{}, model.MemoryAvailability{}, model.DiskAvailability{})
	threshold := p.Resources[pressure.CPU]
	threshold.AllowPause = true
	p.Resources[pressure.CPU], p.MaxPauseJobs = threshold, 1
	m.mu.Lock()
	if !m.bindPressureNativeGrant(l.ID, p) {
		m.mu.Unlock()
		t.Fatal("fixture lacked original-bound policy grant")
	}
	candidate, ok := m.pressureOriginalNativeCandidate(l.ID, p)
	m.mu.Unlock()
	if !ok {
		t.Fatal("fixture lacked current native candidate")
	}
	a := pressure.Action{Kind: pressure.RequestPause, Key: "uncommitted-control", Job: candidate.id}
	result, err := m.applyPressureNativeControl(a, p)
	if err == nil || result.Applied || candidate.job.childOwner.Paused() {
		t.Fatal("unjournaled action sent native STOP", result, err)
	}
}

func TestPressurePausedOriginalRevocationPrecedesResumeAndRetainsCharges(t *testing.T) {
	m := newManager(t)
	r := request(local.ID())
	r.Resources.CPU, r.Resources.Memory, r.Resources.Disk = 0.01, 64, 128
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sleep", "30"}, CWD: filepath.Dir(m.home)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.End(l.ID, "released") })
	p := hostPressurePolicy(model.CPUAvailability{}, model.MemoryAvailability{}, model.DiskAvailability{})
	threshold := p.Resources[pressure.CPU]
	threshold.AllowPause = true
	p.Resources[pressure.CPU], p.MaxPauseJobs = threshold, 1
	m.mu.Lock()
	if !m.bindPressureNativeGrant(l.ID, p) {
		m.mu.Unlock()
		t.Fatal("fixture lacked resolved original-bound grant")
	}
	candidate, ok := m.pressureOriginalNativeCandidate(l.ID, p)
	if !ok {
		m.mu.Unlock()
		t.Fatal("fixture lacked current original owner")
	}
	err = candidate.job.childOwner.Pause() // Test-owned effect; runtime controller stays disconnected.
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before, err := m.Status()
	if err != nil || before.Reserved.CPU != r.Resources.CPU || before.Reserved.Memory != r.Resources.Memory || before.Reserved.Disk != r.Resources.Disk {
		t.Fatal("pause released a lease obligation", before.Reserved, err)
	}
	ended, err := m.End(l.ID, "revoked")
	if err != nil || ended.State != "ending" {
		t.Fatal("revocation did not request original cancellation", ended.State, err)
	}
	m.mu.Lock()
	stillCurrent := m.pressureCandidateStillCurrent(candidate, p)
	m.mu.Unlock()
	if stillCurrent {
		t.Fatal("revoked lease retained native resume eligibility")
	}
	a := pressure.Action{Kind: pressure.RequestResume, Key: "revoked-resume", Job: candidate.id}
	result, err := m.applyPressureNativeControl(a, p)
	if err == nil || result.Applied {
		t.Fatal("revoked original received a pressure resume", result, err)
	}
	select {
	case <-candidate.job.nativeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked stopped original did not reach its native Wait")
	}
	if err := candidate.job.childOwner.Resume(); !errors.Is(err, childowner.ErrRetired) {
		t.Fatal("retired original owner accepted a late CONT", err)
	}
	if err := candidate.job.childOwner.Signal(syscall.SIGKILL); !errors.Is(err, childowner.ErrRetired) {
		t.Fatal("retired original owner accepted another signal", err)
	}
}

func TestPressureNativeControlRequiresDurableCurrentFenceForOwnedStopContinue(t *testing.T) {
	m := newManager(t)
	r := request(local.ID())
	r.Resources.CPU = 0.01
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sleep", "30"}, CWD: filepath.Dir(m.home)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.End(l.ID, "released") })
	p := hostPressurePolicy(model.CPUAvailability{}, model.MemoryAvailability{}, model.DiskAvailability{})
	delete(p.Resources, pressure.Memory)
	delete(p.Resources, pressure.Disk)
	threshold := p.Resources[pressure.CPU]
	threshold.EnterBelow, threshold.ExitAbove = 750, 900
	threshold.AllowPause = true
	p.Resources[pressure.CPU], p.MaxPauseJobs = threshold, 1
	now := time.Now().UTC()
	tick, err := pressureNativeTick()
	if err != nil {
		t.Fatal(err)
	}
	setHostPressureCPU(m, now, tick, 0.1)
	m.mu.Lock()
	if !m.bindPressureNativeGrant(l.ID, p) {
		m.mu.Unlock()
		t.Fatal("fixture lacked resolved original-bound grant")
	}
	candidate, ok := m.pressureOriginalNativeCandidate(l.ID, p)
	if !ok {
		m.mu.Unlock()
		t.Fatal("fixture lacked current original owner")
	}
	pause := stagedPressureAction(pressure.RequestPause, candidate, m.pressureEpoch, 1)
	record := pressure.PauseRecord{Job: candidate.id, Reason: pressure.CPU, Incident: 1, Phase: pressure.PausePending,
		Revision: 1, PauseKey: pause.Key, PolicyDigest: pause.PolicyDigest}
	state := pressure.State{Epoch: m.pressureEpoch, LastTick: tick, LastNow: now, ControlRevision: 1,
		Resources: map[pressure.Resource]pressure.Episode{pressure.CPU: {Active: true, Pressuring: true, Warned: true, Incident: 1}},
		Pauses:    map[pressure.JobID]pressure.PauseRecord{candidate.id: record}}
	persistStagedPressureState(t, m, state)
	m.mu.Unlock()
	result, err := m.applyPressureNativeControl(pause, p)
	if err != nil || !result.Applied || !candidate.job.childOwner.Paused() {
		t.Fatal("durably staged current original did not STOP", err)
	}
	m.mu.Lock()
	pausedTick, tickErr := pressureNativeTick()
	pausedInput, inputErr := m.pressureNativeControlInput(time.Now().UTC(), pausedTick, candidate, p)
	m.mu.Unlock()
	if tickErr != nil || inputErr != nil || !pausedInput.Samples[pressure.CPU].Known || pausedInput.Samples[pressure.CPU].Available > 100 {
		t.Fatal("paused owner supplied possible-use recovery credit", tickErr, inputErr, pausedInput.Samples[pressure.CPU])
	}

	now = time.Now().UTC()
	tick, err = pressureNativeTick()
	if err != nil {
		t.Fatal(err)
	}
	setHostPressureCPU(m, now, tick, 0.95)
	m.mu.Lock()
	resume := stagedPressureAction(pressure.RequestResume, candidate, m.pressureEpoch, 2)
	record.Phase, record.Revision, record.ResumeKey, record.AppliedAt = pressure.ResumePending, 2, resume.Key, tick
	state.LastTick, state.LastNow, state.ControlRevision = tick, now, 2
	state.Resources[pressure.CPU] = pressure.Episode{}
	state.Pauses[candidate.id] = record
	persistStagedPressureState(t, m, state)
	m.mu.Unlock()
	result, err = m.applyPressureNativeControl(resume, p)
	if err != nil || !result.Applied || candidate.job.childOwner.Paused() {
		t.Fatal("durably staged recovered original did not CONT", result, err)
	}
}
