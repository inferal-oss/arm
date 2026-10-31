package manager

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
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

// These helpers are only for positive fixture setup. Owner.Wait can briefly
// hold the original owner's mutex while observing the child. The production
// pressure gate intentionally refuses that window without waiting under m.mu.
// Each helper returns with m.mu held, as it was on entry.
func pressureFixtureYieldLocked(m *Manager) {
	m.mu.Unlock()
	runtime.Gosched()
	m.mu.Lock()
}

type pressureFixtureLedger struct {
	permits, controls, outbox, stateRows int
	stateRaw, memoryState                string
}

func pressureFixtureLedgerLocked(m *Manager, id string) (pressureFixtureLedger, error) {
	var ledger pressureFixtureLedger
	if err := m.db.QueryRow(`SELECT count(*) FROM host_pressure_permits WHERE lease=?`, id).Scan(&ledger.permits); err != nil {
		return ledger, err
	}
	if err := m.db.QueryRow(`SELECT count(*) FROM host_pressure_controls`).Scan(&ledger.controls); err != nil {
		return ledger, err
	}
	if err := m.db.QueryRow(`SELECT count(*) FROM host_pressure_outbox`).Scan(&ledger.outbox); err != nil {
		return ledger, err
	}
	if err := m.db.QueryRow(`SELECT count(*),coalesce(max(data),'') FROM host_pressure_state`).Scan(&ledger.stateRows, &ledger.stateRaw); err != nil {
		return ledger, err
	}
	state, err := json.Marshal(m.pressureState)
	if err != nil {
		return ledger, err
	}
	ledger.memoryState = string(state)
	return ledger, nil
}

// Only expected=0 has a typed refusal entirely before its SQL INSERT. A SQL
// error or any changed permit, grant, original, or control ledger is terminal.
// CAS replacements use the one-shot production writer directly.
func issueInitialPressurePermitForFixtureLocked(m *Manager, id string, p pressure.Policy, reason string) error {
	l, err := m.get(id)
	if err != nil {
		return err
	}
	j := m.jobs[id]
	if j == nil || j.childOwner == nil || j.identity == nil || j.pressureGrant != nil {
		return fmt.Errorf("initial permit fixture lacks an ungranted original")
	}
	owner, cmd, epoch := j.childOwner, j.cmd, m.pressureEpoch
	identity, err := json.Marshal(j.identity)
	if err != nil {
		return err
	}
	baseline, err := pressureFixtureLedgerLocked(m, id)
	if err != nil {
		return err
	}
	if baseline.permits != 0 || baseline.controls != 0 || baseline.outbox != 0 {
		return fmt.Errorf("initial permit fixture has a permit or control ledger row: %+v", baseline)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if !time.Now().Before(deadline) {
			return fmt.Errorf("initial permit fixture deadline passed before issue")
		}
		current, getErr := m.get(id)
		remaining, remainingErr := m.leaseRemaining(l)
		ledger, ledgerErr := pressureFixtureLedgerLocked(m, id)
		currentIdentity, identityErr := json.Marshal(j.identity)
		if getErr != nil || remainingErr != nil || remaining <= 0 || ledgerErr != nil || ledger != baseline ||
			!reflect.DeepEqual(current, l) || m.jobs[id] != j || j.childOwner != owner || j.cmd != cmd ||
			identityErr != nil || !bytes.Equal(currentIdentity, identity) || m.pressureEpoch != epoch || j.pressureGrant != nil ||
			j.pressureEffectActive || j.originalCancellationRequested {
			return fmt.Errorf("initial permit fixture changed before issue: get=%v remaining=%v ledger=%v", getErr, remainingErr, ledgerErr)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("initial permit fixture deadline passed during preflight")
		}
		_, err = m.issuePressurePermitAsOperator(id, p, 0, reason)
		if err == nil {
			if !time.Now().Before(deadline) {
				return fmt.Errorf("initial permit was inserted after fixture deadline")
			}
			return nil
		}
		if err != errPressureControlRefused {
			return err
		}
		ledger, ledgerErr = pressureFixtureLedgerLocked(m, id)
		if ledgerErr != nil || ledger != baseline || m.jobs[id] != j || j.childOwner != owner ||
			j.cmd != cmd || j.pressureGrant != nil || j.pressureEffectActive || j.originalCancellationRequested {
			return fmt.Errorf("initial permit refusal left an uncertain write: ledger=%v grant=%v", ledgerErr, j.pressureGrant)
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("initial permit fixture never observed the original ready: %w", err)
		}
		pressureFixtureYieldLocked(m)
	}
}

func bindPressureNativeCandidateForFixtureLocked(m *Manager, id string, p pressure.Policy, original *job) (pressureNativeCandidate, error) {
	var none pressureNativeCandidate
	l, err := m.get(id)
	if err != nil {
		return none, err
	}
	permit, err := m.readPressurePermit(id)
	if err != nil {
		return none, err
	}
	if original == nil || original.childOwner == nil || m.jobs[id] != original {
		return none, fmt.Errorf("grant fixture lost its captured original")
	}
	owner, cmd := original.childOwner, original.cmd
	deadline := time.Now().Add(time.Second)
	for {
		if !time.Now().Before(deadline) {
			return none, fmt.Errorf("grant fixture deadline passed before attempt")
		}
		current, getErr := m.get(id)
		currentPermit, permitErr := m.readPressurePermit(id)
		remaining, remainingErr := m.leaseRemaining(l)
		if getErr != nil || permitErr != nil || remainingErr != nil || remaining <= 0 ||
			!reflect.DeepEqual(current, l) || !reflect.DeepEqual(currentPermit, permit) ||
			m.jobs[id] != original || original.childOwner != owner || original.cmd != cmd ||
			original.pressureEffectActive || original.originalCancellationRequested {
			return none, fmt.Errorf("grant fixture changed original or permit: get=%v permit=%v remaining=%v", getErr, permitErr, remainingErr)
		}
		if !time.Now().Before(deadline) {
			return none, fmt.Errorf("grant fixture deadline passed during preflight")
		}
		if m.bindPressureNativeGrant(id, p) {
			candidate, ok := m.pressureOriginalNativeCandidate(id, p)
			if ok && candidate.job == original && candidate.owner == owner && candidate.cmd == cmd &&
				candidate.permitRevision == permit.Revision && m.pressureCandidateStillCurrent(candidate, p) {
				if !time.Now().Before(deadline) {
					return none, fmt.Errorf("grant fixture succeeded after deadline")
				}
				return candidate, nil
			}
		}
		if !time.Now().Before(deadline) {
			return none, fmt.Errorf("grant fixture never observed the captured original ready")
		}
		pressureFixtureYieldLocked(m)
	}
}

func pressureCandidateCurrentForFixtureLocked(m *Manager, candidate pressureNativeCandidate, p pressure.Policy) bool {
	deadline := time.Now().Add(time.Second)
	for {
		if !time.Now().Before(deadline) {
			return false
		}
		if m.pressureCandidateStillCurrent(candidate, p) {
			return time.Now().Before(deadline)
		}
		if m.jobs[candidate.id.LeaseID] != candidate.job || candidate.job == nil ||
			candidate.job.childOwner != candidate.owner || candidate.job.pressureEffectActive ||
			candidate.job.originalCancellationRequested || !time.Now().Before(deadline) {
			return false
		}
		pressureFixtureYieldLocked(m)
	}
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
	if m.bindPressureNativeGrant(l.ID, p) {
		t.Fatal("policy alone minted an original-bound grant without an operator permit")
	}
	if err := issueInitialPressurePermitForFixtureLocked(m, l.ID, p, "fixture explicit operator permit"); err != nil {
		t.Fatal("could not issue exact operator permit", err)
	}
	candidate, err := bindPressureNativeCandidateForFixtureLocked(m, l.ID, p, m.jobs[l.ID])
	if err != nil || candidate.id.LeaseID != l.ID || candidate.id.Generation == "" || candidate.remaining <= 0 {
		t.Fatal("live original owner was not selected by explicit policy", err)
	}
	var permitRaw []byte
	if err := m.db.QueryRow(`SELECT data FROM host_pressure_permits WHERE lease=?`, l.ID).Scan(&permitRaw); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`UPDATE host_pressure_permits SET data=? WHERE lease=?`, permitRaw, l.ID); err != nil {
		t.Fatal(err)
	}
	if m.pressureCandidateStillCurrent(candidate, p) {
		t.Fatal("matching BLOB operator row restored native grant")
	}
	if _, err := m.db.Exec(`UPDATE host_pressure_permits SET data=? WHERE lease=?`, string(permitRaw), l.ID); err != nil {
		t.Fatal(err)
	}
	if !pressureCandidateCurrentForFixtureLocked(m, candidate, p) {
		t.Fatal("canonical operator permit did not restore same original candidate")
	}
	if _, err := m.issuePressurePermitAsOperator(l.ID, p, 1, "fixture replacement permit"); err != nil {
		t.Fatal(err)
	}
	fresh, bindErr := bindPressureNativeCandidateForFixtureLocked(m, l.ID, p, m.jobs[l.ID])
	if bindErr != nil || m.pressureCandidateStillCurrent(candidate, p) {
		t.Fatal("new same-policy permit revived an old candidate")
	}
	candidate = fresh
	if candidate.permitRevision != 2 {
		t.Fatal("fresh replacement permit did not select a fresh candidate")
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
	if m.bindPressureNativeGrant(l.ID, p) {
		t.Fatal("stale operator permit minted grant for renewed lease revision")
	}
	if _, err := m.issuePressurePermitAsOperator(l.ID, p, 2, "fixture revised lease permit"); err != nil {
		t.Fatal("could not reissue exact revised operator permit", err)
	}
	current, bindErr := bindPressureNativeCandidateForFixtureLocked(m, l.ID, p, original)
	if bindErr != nil || current.leaseRevision != l.Revision || current.id != candidate.id {
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
	// Force the same busy-owner gate seen by the original Wait. A typed
	// initial-permit refusal must leave no SQL row, grant, or control effect.
	j := m.jobs[l.ID]
	if j == nil || j.childOwner == nil {
		m.mu.Unlock()
		t.Fatal("fixture lacks captured original owner")
	}
	before, err := pressureFixtureLedgerLocked(m, l.ID)
	if err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	ownerMu := (*sync.Mutex)(unsafe.Pointer(reflect.ValueOf(j.childOwner).Elem().FieldByName("mu").UnsafeAddr()))
	var refused error
	func() {
		ownerMu.Lock()
		defer ownerMu.Unlock()
		_, refused = m.issuePressurePermitAsOperator(l.ID, p, 0, "fixture busy-owner permit")
	}()
	after, ledgerErr := pressureFixtureLedgerLocked(m, l.ID)
	if refused != errPressureControlRefused || ledgerErr != nil || after != before ||
		j.pressureGrant != nil || j.pressureEffectActive {
		m.mu.Unlock()
		t.Fatal("busy original owner issued or reserved pressure authority", refused, ledgerErr)
	}
	if err := issueInitialPressurePermitForFixtureLocked(m, l.ID, p, "fixture unjournaled-action permit"); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	candidate, err := bindPressureNativeCandidateForFixtureLocked(m, l.ID, p, m.jobs[l.ID])
	m.mu.Unlock()
	if err != nil {
		t.Fatal("fixture lacked current native candidate", err)
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
	if err := issueInitialPressurePermitForFixtureLocked(m, l.ID, p, "fixture revoked-lease permit"); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	candidate, bindErr := bindPressureNativeCandidateForFixtureLocked(m, l.ID, p, m.jobs[l.ID])
	if bindErr != nil {
		m.mu.Unlock()
		t.Fatal("fixture lacked current original owner", bindErr)
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
	if err := issueInitialPressurePermitForFixtureLocked(m, l.ID, p, "fixture staged-control permit"); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	candidate, bindErr := bindPressureNativeCandidateForFixtureLocked(m, l.ID, p, m.jobs[l.ID])
	if bindErr != nil {
		m.mu.Unlock()
		t.Fatal("fixture lacked current original owner", bindErr)
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
	t.Run("busy-owner-refuses-before-reservation", func(t *testing.T) {
		// Use the existing test-only mutex seam to make the nonblocking
		// original-owner refusal deterministic; it must precede any effect.
		ownerMu := (*sync.Mutex)(unsafe.Pointer(reflect.ValueOf(candidate.job.childOwner).Elem().FieldByName("mu").UnsafeAddr()))
		var blocked pressureNativeCandidate
		var blockedErr error
		func() {
			ownerMu.Lock()
			defer ownerMu.Unlock()
			blocked, blockedErr = m.reservePressureNativeControl(pause, p)
		}()
		m.mu.Lock()
		active := candidate.job.pressureEffectActive
		m.mu.Unlock()
		if !errors.Is(blockedErr, errPressureControlRefused) || blocked.job != nil || active || candidate.job.childOwner.Paused() {
			t.Fatal("busy original owner acquired a reservation or effect", blockedErr)
		}
	})
	// The sole original Wait polls under Owner.mu. Its legitimate transient
	// contention can refuse this pre-effect reservation. Retry only that typed
	// refusal: every attempt repeats the unchanged durable/current/elapsed
	// guards, and none has reserved a slot or sent STOP/CONT. Native apply
	// calls and errors below are never retried.
	var reserved pressureNativeCandidate
	reserveUntil := time.Now().Add(time.Second)
	for {
		reserved, err = m.reservePressureNativeControl(pause, p)
		if !errors.Is(err, errPressureControlRefused) || !time.Now().Before(reserveUntil) {
			break
		}
		time.Sleep(time.Millisecond)
	}
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
	if err := issueInitialPressurePermitForFixtureLocked(m, l.ID, p, "fixture degraded-expiry permit"); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	candidate, bindErr := bindPressureNativeCandidateForFixtureLocked(m, l.ID, p, m.jobs[l.ID])
	if bindErr != nil {
		m.mu.Unlock()
		t.Fatal("fixture lacked exact original candidate", bindErr)
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
