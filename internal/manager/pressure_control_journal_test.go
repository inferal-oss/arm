package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/pressure"
)

func newPressureJournalFixture(t *testing.T) (*Manager, pressure.Policy, pressureNativeCandidate, pressure.Action, pressure.State) {
	t.Helper()
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
	threshold.EnterBelow, threshold.ExitAbove, threshold.AllowPause = 750, 900, true
	p.Resources[pressure.CPU], p.MaxPauseJobs = threshold, 1
	now := time.Now().UTC()
	tick, err := pressureNativeTick()
	if err != nil {
		t.Fatal(err)
	}
	setHostPressureCPU(m, now, tick, 0.1)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := issueInitialPressurePermitForFixtureLocked(m, l.ID, p, "fixture operator pressure permit"); err != nil {
		t.Fatal(err)
	}
	candidate, err := bindPressureNativeCandidateForFixtureLocked(m, l.ID, p, m.jobs[l.ID])
	if err != nil {
		t.Fatal("fixture lacks current original candidate", err)
	}
	a := stagedPressureAction(pressure.RequestPause, candidate, m.pressureEpoch, 1)
	record := pressure.PauseRecord{Job: candidate.id, Reason: pressure.CPU, Incident: 1, Phase: pressure.PausePending,
		Revision: 1, PauseKey: a.Key, PolicyDigest: a.PolicyDigest}
	next := pressure.State{Epoch: m.pressureEpoch, LastTick: tick, LastNow: now, ControlRevision: 1,
		Resources: map[pressure.Resource]pressure.Episode{pressure.CPU: {Active: true, Pressuring: true, Warned: true, Incident: 1}},
		Pauses:    map[pressure.JobID]pressure.PauseRecord{candidate.id: record}}
	return m, p, candidate, a, next
}

func stagePressureJournalFixture(t *testing.T, m *Manager, candidate pressureNativeCandidate, a pressure.Action, next pressure.State) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := m.stagePressureControlDecision(tx, next, []pressure.Action{a}, map[pressure.JobID]pressureNativeCandidate{candidate.id: candidate}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	m.pressureState = next
}

func TestPressureControlJournalAtomicIntentAndExactResult(t *testing.T) {
	m, p, candidate, a, next := newPressureJournalFixture(t)
	m.mu.Lock()
	var beforeState, beforeControls int
	if err := m.db.QueryRow(`SELECT count(*) FROM host_pressure_state`).Scan(&beforeState); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	if err := m.db.QueryRow(`SELECT count(*) FROM host_pressure_controls`).Scan(&beforeControls); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`CREATE TRIGGER pressure_control_fail BEFORE INSERT ON host_pressure_controls
		BEGIN SELECT RAISE(ABORT,'forced control-intent failure'); END`); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	tx, err := m.db.Begin()
	if err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	stageErr := m.stagePressureControlDecision(tx, next, []pressure.Action{a}, map[pressure.JobID]pressureNativeCandidate{candidate.id: candidate})
	_ = tx.Rollback()
	if _, err := m.db.Exec(`DROP TRIGGER pressure_control_fail`); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	var afterState, afterControls int
	stateErr := m.db.QueryRow(`SELECT count(*) FROM host_pressure_state`).Scan(&afterState)
	controlErr := m.db.QueryRow(`SELECT count(*) FROM host_pressure_controls`).Scan(&afterControls)
	m.mu.Unlock()
	if stageErr == nil || stateErr != nil || controlErr != nil || beforeState != afterState || beforeControls != afterControls {
		t.Fatal("failed intent insert committed state or control row", stageErr, stateErr, controlErr, beforeState, afterState, beforeControls, afterControls)
	}
	stagePressureJournalFixture(t, m, candidate, a, next)
	m.mu.Lock()
	attempt, err := m.claimPressureControlIntent(a.Key, candidate, p)
	m.mu.Unlock()
	if err != nil {
		t.Fatal("durable exact intent could not enter attempting", err)
	}
	effect, err := m.applyPressureNativeControl(a, p)
	if err != nil || !effect.Applied {
		t.Fatal("test-owned original did not apply staged STOP", effect, err)
	}
	m.mu.Lock()
	result, recordErr := m.recordPressureControlResult(attempt, effect.Applied)
	row, readErr := readPressureControlRow(m.db, a.Key)
	m.mu.Unlock()
	if recordErr != nil || readErr != nil || !result.Applied || result.Key != a.Key || row.State != pressureControlApplied || row.Result == nil || *row.Result != result {
		t.Fatal("exact native result did not commit", result, recordErr, row, readErr)
	}
	m.mu.Lock()
	_, err = m.recordPressureControlResult(attempt, false)
	m.mu.Unlock()
	if !errors.Is(err, errPressureControlRefused) {
		t.Fatal("completed result accepted a contradictory replay", err)
	}
}

func TestPressureControlJournalRecoveryNeverReplaysNativeIntent(t *testing.T) {
	for _, attemptBeforeRecovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "queued", true: "attempting"}[attemptBeforeRecovery], func(t *testing.T) {
			m, p, candidate, a, next := newPressureJournalFixture(t)
			stagePressureJournalFixture(t, m, candidate, a, next)
			var attempt pressureControlAttempt
			if !attemptBeforeRecovery {
				m.mu.Lock()
				var exact []byte
				readErr := m.db.QueryRow(`SELECT data FROM host_pressure_controls WHERE key=?`, a.Key).Scan(&exact)
				if readErr == nil {
					_, readErr = m.db.Exec(`UPDATE host_pressure_controls SET data=? WHERE key=?`, exact, a.Key)
				}
				_, blobClaimErr := m.claimPressureControlIntent(a.Key, candidate, p)
				if readErr == nil {
					_, readErr = m.db.Exec(`UPDATE host_pressure_controls SET data=? WHERE key=?`, string(exact), a.Key)
				}
				m.mu.Unlock()
				if readErr != nil || !errors.Is(blobClaimErr, errPressureControlRefused) {
					t.Fatal("matching BLOB intent gained native claim authority", readErr, blobClaimErr)
				}
			}
			if attemptBeforeRecovery {
				m.mu.Lock()
				var err error
				attempt, err = m.claimPressureControlIntent(a.Key, candidate, p)
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			m.mu.Lock()
			err := m.recoverPressureControlIntents()
			row, readErr := readPressureControlRow(m.db, a.Key)
			_, replayErr := m.claimPressureControlIntent(a.Key, candidate, p)
			unknown, unknownErr := m.pressureUnknownControlRows()
			var startupErr error
			if attemptBeforeRecovery {
				startupErr = m.loadHostPressure()
			}
			m.mu.Unlock()
			want := pressureControlAbandoned
			if attemptBeforeRecovery {
				want = pressureControlUnknown
			}
			if err != nil || readErr != nil || row.State != want || !errors.Is(replayErr, errPressureControlRefused) || unknownErr != nil || len(unknown) != map[bool]int{false: 0, true: 1}[attemptBeforeRecovery] {
				t.Fatal("restart restored native authority or lost uncertain effect", err, readErr, row.State, replayErr, unknownErr, len(unknown))
			}
			if attemptBeforeRecovery && (startupErr == nil || !strings.Contains(startupErr.Error(), "unknown native effects")) {
				t.Fatal("warning-only startup accepted unknown original effect", startupErr)
			}
			if attemptBeforeRecovery {
				if _, err := m.recordPressureControlResult(attempt, true); !errors.Is(err, errPressureControlRefused) {
					t.Fatal("old process token completed a recovered unknown effect", err)
				}
			}
		})
	}
}

func TestPressureControlJournalPermitReplacementRefusesOldClaim(t *testing.T) {
	m, p, candidate, a, next := newPressureJournalFixture(t)
	stagePressureJournalFixture(t, m, candidate, a, next)
	m.mu.Lock()
	defer m.mu.Unlock()
	permit, err := m.readPressurePermit(candidate.id.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	// A previously staged intent cannot inherit a later physical permit row.
	// The private writer rejects pending pauses, so inject the adversarial row
	// directly to exercise the claim boundary after a durable transition.
	permit.Revision++
	permit.IssuedAt = time.Now().UTC()
	raw, err := json.Marshal(permit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`UPDATE host_pressure_permits SET revision=?,data=? WHERE lease=?`, permit.Revision, string(raw), candidate.id.LeaseID); err != nil {
		t.Fatal(err)
	}
	fresh, bindErr := bindPressureNativeCandidateForFixtureLocked(m, candidate.id.LeaseID, p, candidate.job)
	if bindErr != nil {
		t.Fatal("replacement permit did not bind original", bindErr)
	}
	if m.pressureCandidateStillCurrent(candidate, p) {
		t.Fatal("old candidate crossed a permit replacement")
	}
	if _, err := m.claimPressureControlIntent(a.Key, candidate, p); !errors.Is(err, errPressureControlRefused) {
		t.Fatal("old permit intent was claimed after replacement", err)
	}
	if fresh.permitRevision != candidate.permitRevision+1 {
		t.Fatal("fresh permit failed to create a separate eligible candidate")
	}
}

func TestPressureOriginalDeadlineBindingAndAckedRenewal(t *testing.T) {
	m, p, candidate, _, _ := newPressureJournalFixture(t)
	m.mu.Lock()
	id := candidate.id.LeaseID
	l, err := m.get(id)
	if err != nil || l.Clock == nil || l.Expires == nil {
		m.mu.Unlock()
		t.Fatal("fixture lacks original deadline", err)
	}
	prior, err := json.Marshal(l)
	if err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	for _, change := range []string{"clock", "expires"} {
		damaged := l
		if change == "clock" {
			clock := *l.Clock
			clock.End++
			damaged.Clock = &clock
		} else {
			expires := l.Expires.Add(time.Second)
			damaged.Expires = &expires
		}
		raw, err := json.Marshal(damaged)
		if err != nil {
			m.mu.Unlock()
			t.Fatal(err)
		}
		if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(raw), id); err != nil {
			m.mu.Unlock()
			t.Fatal(err)
		}
		if m.pressureCandidateStillCurrent(candidate, p) {
			m.mu.Unlock()
			t.Fatal("raw same-revision deadline replacement retained original eligibility", change)
		}
	}
	if _, err := m.db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(prior), id); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	m.mu.Unlock()
	if _, err := m.Renew(id, 2*time.Minute); err != nil {
		t.Fatal("acknowledged renewal failed", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pressureCandidateStillCurrent(candidate, p) {
		t.Fatal("old candidate survived acknowledged renewal")
	}
	if _, err := m.issuePressurePermitAsOperator(id, p, candidate.permitRevision, "renewed original permit"); err != nil {
		t.Fatal(err)
	}
	fresh, bindErr := bindPressureNativeCandidateForFixtureLocked(m, id, p, candidate.job)
	if bindErr != nil || fresh.leaseRevision <= candidate.leaseRevision {
		t.Fatal("renewed original lost explicit pressure eligibility")
	}
}

func TestPressureControlJournalBoundsPhysicalHeaders(t *testing.T) {
	m, _, candidate, a, next := newPressureJournalFixture(t)
	stagePressureJournalFixture(t, m, candidate, a, next)
	if _, err := m.db.Exec(`UPDATE host_pressure_controls SET state='unknown' WHERE key=?`, a.Key); err != nil {
		t.Fatal(err)
	}
	for _, damage := range []struct {
		column string
		value  any
	}{
		{"key", []byte(a.Key)}, {"key", strings.Repeat("k", pressureControlKeyMaxBytes+1)},
		{"epoch", []byte(a.Epoch)}, {"epoch", strings.Repeat("e", pressureControlEpochMaxBytes+1)},
		{"state", []byte("unknown")}, {"state", strings.Repeat("s", 17)},
	} {
		query := `UPDATE host_pressure_controls SET ` + damage.column + `=?`
		if _, err := m.db.Exec(query, damage.value); err != nil {
			t.Fatal(err)
		}
		if _, err := readPressureControlRow(m.db, a.Key); !errors.Is(err, errPressureControlRefused) && !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("damaged physical header was read", damage.column, err)
		}
		if err := m.validatePressureControlRowsAtStartup(); !errors.Is(err, errPressureControlRefused) {
			t.Fatal("startup accepted a damaged control header", damage.column, err)
		}
		if damage.column != "state" || !reflect.DeepEqual(damage.value, strings.Repeat("s", 17)) {
			if _, err := m.pressureUnknownControlRows(); !errors.Is(err, errPressureControlRefused) {
				t.Fatal("damaged unknown roster header was accepted", damage.column, err)
			}
		}
		if _, err := m.db.Exec(`UPDATE host_pressure_controls SET key=?,epoch=?,state='unknown'`, a.Key, a.Epoch); err != nil {
			t.Fatal(err)
		}
	}
	unknown, err := m.pressureUnknownControlRows()
	if err != nil || len(unknown) != 1 {
		t.Fatal("canonical unknown row became unreadable", err, len(unknown))
	}
	for i := 0; i < 256; i++ {
		if _, err := m.db.Exec(`INSERT INTO host_pressure_controls(key,epoch,state,data) VALUES(?,?,'unknown','{}')`, fmt.Sprintf("extra-%03d", i), a.Epoch); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.pressureUnknownControlRows(); err == nil || !strings.Contains(err.Error(), "exceeds bound") {
		t.Fatal("257-row unknown roster was truncated or accepted", err)
	}
}

func TestPressurePermitRevisionProjectionRefusesMalformed(t *testing.T) {
	m, _, candidate, _, _ := newPressureJournalFixture(t)
	m.mu.Lock()
	defer m.mu.Unlock()
	id := candidate.id.LeaseID
	for _, damaged := range []any{strings.Repeat("x", 1<<20), []byte(strings.Repeat("x", 1<<20))} {
		if _, err := m.db.Exec(`UPDATE host_pressure_permits SET revision=? WHERE lease=?`, damaged, id); err != nil {
			t.Fatal(err)
		}
		if _, err := m.readPressurePermit(id); !errors.Is(err, errPressureControlRefused) {
			t.Fatal("malformed physical permit revision gained authority", err)
		}
		if err := m.validatePressurePermitRowsAtStartup(); !errors.Is(err, errPressureControlRefused) {
			t.Fatal("startup accepted malformed permit revision", err)
		}
	}
	if _, err := m.db.Exec(`UPDATE host_pressure_permits SET revision=? WHERE lease=?`, candidate.permitRevision, id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readPressurePermit(id); err != nil {
		t.Fatal("canonical permit revision was not restored", err)
	}
}
