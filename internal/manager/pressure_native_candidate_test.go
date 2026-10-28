package manager

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"getarm.org/internal/childowner"
	"getarm.org/internal/leaseclock"
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
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`UPDATE host_pressure_state SET data=? WHERE id='current'`, raw); err != nil {
		t.Fatal(err)
	}
	result, err := m.applyPressureNativeControl(pause, p)
	if err == nil || result.Applied || candidate.job.childOwner.Paused() {
		t.Fatal("matching BLOB bypassed the durable TEXT fence", result, err)
	}
	if _, err := m.db.Exec(`UPDATE host_pressure_state SET data=? WHERE id='current'`, strings.Repeat(" ", maxDurablePressureStateBytes+1)); err != nil {
		t.Fatal(err)
	}
	result, err = m.applyPressureNativeControl(pause, p)
	if err == nil || result.Applied || candidate.job.childOwner.Paused() {
		t.Fatal("oversize durable TEXT bypassed the bounded fence", result, err)
	}
	m.mu.Lock()
	persistStagedPressureState(t, m, state)
	m.mu.Unlock()
	reserved, err := m.reservePressureNativeControl(pause, p)
	if err != nil || reserved.job != candidate.job {
		t.Fatal("fixture could not reserve exact current pressure effect", err)
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			m.mu.Lock()
			reserved.job.pressureEffectActive = false
			reserved.job.pressureEffectCandidate = pressureNativeCandidate{}
			m.mu.Unlock()
			m.wg.Done()
		})
	}
	defer release()
	m.mu.Lock()
	beforeRenew, err := m.get(l.ID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Renew(l.ID, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "native pressure effect is reserved") {
		t.Fatal("renewal revised a lease during its reserved native effect", err)
	}
	m.mu.Lock()
	afterRenew, getErr := m.get(l.ID)
	m.mu.Unlock()
	if getErr != nil || !reflect.DeepEqual(beforeRenew, afterRenew) {
		t.Fatal("refused concurrent renewal changed lease or elapsed entitlement", getErr)
	}
	release()
	result, err = m.applyPressureNativeControl(pause, p)
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

func TestPressureReservedEffectLetsEndRetireWithoutWaitingForNativeOwner(t *testing.T) {
	m := newManager(t)
	r := request(local.ID())
	r.Resources.CPU = 0.01
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sleep", "30"}, CWD: filepath.Dir(m.home)})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	j := m.jobs[l.ID]
	m.mu.Unlock()
	if j == nil || j.childOwner == nil {
		t.Fatal("fixture lacks original owner")
	}
	if err := j.childOwner.Pause(); err != nil {
		t.Fatal("fixture could not stop its original child", err)
	}
	m.mu.Lock()
	j.pressureEffectActive = true // Deterministic reservation before End; native effect is already owned.
	m.mu.Unlock()
	var finishOnce sync.Once
	finish := func() {
		finishOnce.Do(func() {
			m.mu.Lock()
			j.pressureEffectActive = false
			deferred := j.pressureCancelDeferred
			j.pressureCancelDeferred = false
			m.mu.Unlock()
			if deferred {
				m.startDeferredPressureCancellation(l.ID, j)
			}
		})
	}
	t.Cleanup(func() {
		finish()
		_ = j.childOwner.Signal(syscall.SIGKILL) // Only this captured test-owned original.
	})
	ended := make(chan error, 1)
	go func() {
		_, endErr := m.End(l.ID, "revoked")
		ended <- endErr
	}()
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal("End could not record revocation while effect was reserved", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("End waited on the original native observer under Manager.mu")
	}
	m.mu.Lock()
	deferred := j.originalCancellationRequested && j.pressureCancelDeferred
	m.mu.Unlock()
	if !deferred || !j.childOwner.Paused() || j.cmd.ProcessState != nil {
		t.Fatal("End lost its exact stopped original or deferred cancellation")
	}
	finish()
	select {
	case <-j.nativeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("deferred original cancellation did not reach its sole native Wait")
	}
	if err := j.childOwner.Resume(); !errors.Is(err, childowner.ErrRetired) {
		t.Fatal("original owner resumed after deferred End/Wait", err)
	}
}

func TestPressureDegradedExpiryUsesCapturedOriginalDuringReservedEffect(t *testing.T) {
	m := newManager(t)
	r := request(local.ID())
	r.Resources.CPU = 0.01
	l, err := m.Acquire(r, &RunRequest{Request: r, Command: []string{"/bin/sleep", "30"}, CWD: filepath.Dir(m.home)})
	if err != nil {
		t.Fatal(err)
	}
	p := hostPressurePolicy(model.CPUAvailability{}, model.MemoryAvailability{}, model.DiskAvailability{})
	threshold := p.Resources[pressure.CPU]
	threshold.AllowPause = true
	p.Resources[pressure.CPU], p.MaxPauseJobs = threshold, 1
	m.mu.Lock()
	if !m.bindPressureNativeGrant(l.ID, p) {
		m.mu.Unlock()
		t.Fatal("fixture lacked original-bound grant")
	}
	candidate, ok := m.pressureOriginalNativeCandidate(l.ID, p)
	if !ok {
		m.mu.Unlock()
		t.Fatal("fixture lacked exact original candidate")
	}
	j := candidate.job
	j.pressureEffectActive = true
	j.pressureEffectCandidate = candidate // Same fields captured by the real reservation.
	m.wg.Add(1)
	m.mu.Unlock()
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			m.mu.Lock()
			j.pressureEffectActive = false
			j.pressureEffectCandidate = pressureNativeCandidate{}
			deferred := j.pressureCancelDeferred
			j.pressureCancelDeferred = false
			m.mu.Unlock()
			if deferred {
				m.startDeferredPressureCancellation(l.ID, j)
			}
			m.wg.Done()
		})
	}
	defer release()
	m.mu.Lock()
	current, err := m.get(l.ID)
	m.mu.Unlock()
	if err != nil || current.Clock == nil || current.Process == nil {
		t.Fatal("fixture lost durable original row", err)
	}
	expired := leaseclock.Sample{Boot: current.Clock.Boot, Tick: current.Clock.End, Wall: time.Now().UTC()}
	bad := current
	altered := *current.Process
	altered.Start += "-replaced"
	bad.Process = &altered
	m.mu.Lock()
	badErr := m.cancelCheckpointExpiredJobs(expired, []checkpointExpiryLease{{id: l.ID, lease: bad}})
	badCancelled := j.originalCancellationRequested
	m.mu.Unlock()
	if badErr == nil || badCancelled {
		t.Fatal("replaced process identity inherited reserved original authority", badErr)
	}
	// The real observer holds Owner.mu during a native preflight. Fault-inject
	// that private gate here without expanding the production Owner API: the
	// degraded expiry must use its exact captured reservation while it is busy.
	ownerMu := (*sync.Mutex)(unsafe.Pointer(reflect.ValueOf(j.childOwner).Elem().FieldByName("mu").UnsafeAddr()))
	ownerMu.Lock()
	ownerLocked := true
	defer func() {
		if ownerLocked {
			ownerMu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() {
		m.mu.Lock()
		err := m.cancelCheckpointExpiredJobs(expired, []checkpointExpiryLease{{id: l.ID, lease: current}})
		m.mu.Unlock()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("degraded expiry could not retire its captured original", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("degraded expiry held Manager.mu behind a reserved owner effect")
	}
	m.mu.Lock()
	deferred := j.originalCancellationRequested && j.pressureCancelDeferred
	m.mu.Unlock()
	if !deferred {
		t.Fatal("degraded expiry did not defer exact original cancellation")
	}
	ownerMu.Unlock()
	ownerLocked = false
	release()
	select {
	case <-j.nativeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("degraded expiry did not join its captured original")
	}
}
