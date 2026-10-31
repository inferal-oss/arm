package manager

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"getarm.org/internal/pressure"
)

const pressureControlIntentMaxBytes = 16384
const pressureControlResultMaxBytes = 1024
const pressureControlKeyMaxBytes = 8192
const pressureControlEpochMaxBytes = 256

// Project physical header types and sizes before returning any bytes to Go.
// CAST comparison in the callers also finds a matching damaged BLOB key/state.
const pressureControlHeaderColumns = `typeof(key),length(CAST(key AS BLOB)),
	CASE WHEN typeof(key)='text' AND length(CAST(key AS BLOB)) BETWEEN 1 AND 8192 THEN CAST(key AS BLOB) END,
	typeof(epoch),length(CAST(epoch AS BLOB)),
	CASE WHEN typeof(epoch)='text' AND length(CAST(epoch AS BLOB)) BETWEEN 1 AND 256 THEN CAST(epoch AS BLOB) END,
	typeof(state),length(CAST(state AS BLOB)),
	CASE WHEN typeof(state)='text' AND length(CAST(state AS BLOB)) BETWEEN 1 AND 16 THEN CAST(state AS BLOB) END`

type pressureControlHeader struct {
	keyType, epochType, stateType string
	keySize, epochSize, stateSize int64
	keyRaw, epochRaw, stateRaw    []byte
}

func (h *pressureControlHeader) fields() []any {
	return []any{&h.keyType, &h.keySize, &h.keyRaw, &h.epochType, &h.epochSize, &h.epochRaw,
		&h.stateType, &h.stateSize, &h.stateRaw}
}

func (h pressureControlHeader) valid() bool {
	return h.keyType == "text" && h.keySize > 0 && h.keySize <= pressureControlKeyMaxBytes && len(h.keyRaw) == int(h.keySize) && utf8.Valid(h.keyRaw) &&
		h.epochType == "text" && h.epochSize > 0 && h.epochSize <= pressureControlEpochMaxBytes && len(h.epochRaw) == int(h.epochSize) && utf8.Valid(h.epochRaw) &&
		h.stateType == "text" && h.stateSize > 0 && h.stateSize <= 16 && len(h.stateRaw) == int(h.stateSize) && utf8.Valid(h.stateRaw)
}

const (
	pressureControlQueued     = "queued"
	pressureControlAttempting = "attempting"
	pressureControlApplied    = "applied"
	pressureControlRefused    = "refused"
	pressureControlUnknown    = "unknown"
	pressureControlAbandoned  = "abandoned"
)

// pressureControlIntent is immutable durable intent, not native signal
// authority. An original-owner pointer is never serialized or reconstructed.
type pressureControlIntent struct {
	Action         pressure.Action `json:"action"`
	LeaseRevision  int             `json:"lease_revision"`
	PermitRevision int64           `json:"permit_revision"`
}

type pressureControlRow struct {
	Intent pressureControlIntent
	State  string
	Result *pressure.ControlResult
}

type pressureControlAttempt struct {
	intent    pressureControlIntent
	candidate pressureNativeCandidate // process-local original, lost on restart
}

func pressureIntentMatchesState(intent pressureControlIntent, next pressure.State) bool {
	a := intent.Action
	if (a.Kind != pressure.RequestPause && a.Kind != pressure.RequestResume) || a.Key == "" ||
		len(a.Key) > pressureControlKeyMaxBytes || !utf8.ValidString(a.Key) || a.Epoch == "" ||
		len(a.Epoch) > pressureControlEpochMaxBytes || !utf8.ValidString(a.Epoch) || a.Epoch != next.Epoch ||
		intent.LeaseRevision <= 0 || intent.PermitRevision <= 0 || a.Revision == 0 || a.Job.LeaseID == "" ||
		a.Reason != pressure.CPU || len(a.PolicyDigest) != 64 {
		return false
	}
	if _, err := hex.DecodeString(a.PolicyDigest); err != nil {
		return false
	}
	record, ok := next.Pauses[a.Job]
	if !ok || record.Job != a.Job || record.Revision != a.Revision || record.Reason != a.Reason ||
		record.Incident != a.Incident || record.PolicyDigest != a.PolicyDigest {
		return false
	}
	if a.Kind == pressure.RequestPause {
		return record.Phase == pressure.PausePending && record.PauseKey == a.Key
	}
	return record.Phase == pressure.ResumePending && record.ResumeKey == a.Key
}

// stagePressureControlDecision writes state and each native intent in the
// caller's transaction, alongside its warning messages/events. The caller
// holds Manager.mu and commits only after every action has a matching exact
// candidate. It is unused by the warning-only runtime.
func (m *Manager) stagePressureControlDecision(tx *sql.Tx, next pressure.State, actions []pressure.Action, candidates map[pressure.JobID]pressureNativeCandidate) error {
	if tx == nil || len(actions) == 0 || len(actions) > 256 {
		return errPressureControlRefused
	}
	stateRaw, err := json.Marshal(next)
	if err != nil || len(stateRaw) > maxDurablePressureStateBytes {
		return errPressureControlRefused
	}
	intents := make([]pressureControlIntent, 0, len(actions))
	seen := make(map[string]bool, len(actions))
	for _, a := range actions {
		candidate, ok := candidates[a.Job]
		intent := pressureControlIntent{Action: a, LeaseRevision: candidate.leaseRevision, PermitRevision: candidate.permitRevision}
		if !ok || seen[a.Key] || candidate.job == nil || candidate.owner == nil || candidate.cmd == nil ||
			m.jobs[a.Job.LeaseID] != candidate.job || candidate.job.childOwner != candidate.owner ||
			candidate.job.cmd != candidate.cmd || candidate.id != a.Job ||
			a.PolicyDigest != hex.EncodeToString(candidate.policyDigest[:]) || !pressureIntentMatchesState(intent, next) {
			return errPressureControlRefused
		}
		seen[a.Key] = true
		intents = append(intents, intent)
	}
	var active int
	if err := tx.QueryRow(`SELECT count(*) FROM host_pressure_controls WHERE state IN ('queued','attempting','unknown')`).Scan(&active); err != nil {
		return err
	}
	if active < 0 || active > 256-len(intents) {
		return errPressureControlRefused
	}
	var priorType string
	var priorSize int64
	var priorRaw []byte
	priorErr := tx.QueryRow(`SELECT typeof(data),length(CAST(data AS BLOB)),
		CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB))<=? THEN substr(CAST(data AS BLOB),1,?) END
		FROM host_pressure_state WHERE id='current'`, maxDurablePressureStateBytes, maxDurablePressureStateBytes+1).
		Scan(&priorType, &priorSize, &priorRaw)
	if priorErr != nil && !errors.Is(priorErr, sql.ErrNoRows) {
		return priorErr
	}
	if errors.Is(priorErr, sql.ErrNoRows) {
		if m.pressureState.Epoch != "" || m.pressureState.ControlRevision != 0 || len(m.pressureState.Pauses) != 0 ||
			len(m.pressureState.Unresolved) != 0 || len(m.pressureState.Resources) != 0 || len(m.pressureState.JobWarnings) != 0 {
			return errPressureControlRefused
		}
	} else {
		expectedRaw, err := json.Marshal(m.pressureState)
		if err != nil || priorType != "text" || priorSize < 0 || priorSize > maxDurablePressureStateBytes ||
			!bytes.Equal(priorRaw, expectedRaw) {
			return errPressureControlRefused
		}
	}
	if _, err := tx.Exec(`INSERT INTO host_pressure_state(id,data) VALUES('current',?)
		ON CONFLICT(id) DO UPDATE SET data=excluded.data`, string(stateRaw)); err != nil {
		return err
	}
	for _, intent := range intents {
		raw, err := json.Marshal(intent)
		if err != nil || len(raw) > pressureControlIntentMaxBytes {
			return errPressureControlRefused
		}
		a := intent.Action
		if _, err := tx.Exec(`INSERT INTO host_pressure_controls(key,epoch,state,data) VALUES(?,?,'queued',?)`,
			a.Key, a.Epoch, string(raw)); err != nil {
			return err
		}
		stored, err := readPressureControlRow(tx, a.Key)
		if err != nil || stored.Intent != intent {
			return errPressureControlRefused
		}
	}
	return nil
}

type pressureControlQueryRow interface {
	QueryRow(string, ...any) *sql.Row
}

func readPressureControlRow(query pressureControlQueryRow, key string) (pressureControlRow, error) {
	var row pressureControlRow
	var header pressureControlHeader
	var dataType, resultType string
	var dataSize, resultSize int64
	var raw, resultRaw []byte
	columns := pressureControlHeaderColumns + `,typeof(data),length(CAST(data AS BLOB)),
		CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB))<=? THEN substr(CAST(data AS BLOB),1,?) END,
		typeof(result),coalesce(length(CAST(result AS BLOB)),0),
		CASE WHEN typeof(result)='text' AND length(CAST(result AS BLOB))<=? THEN substr(CAST(result AS BLOB),1,?) END
		FROM host_pressure_controls WHERE CAST(key AS BLOB)=CAST(? AS BLOB)
		AND (SELECT count(*) FROM host_pressure_controls WHERE CAST(key AS BLOB)=CAST(? AS BLOB))=1`
	err := query.QueryRow(`SELECT `+columns, pressureControlIntentMaxBytes, pressureControlIntentMaxBytes+1,
		pressureControlResultMaxBytes, pressureControlResultMaxBytes+1, key, key).
		Scan(append(header.fields(), &dataType, &dataSize, &raw, &resultType, &resultSize, &resultRaw)...)
	if err != nil {
		return pressureControlRow{}, err
	}
	if !header.valid() || string(header.keyRaw) != key || dataType != "text" || dataSize <= 0 || dataSize > pressureControlIntentMaxBytes ||
		len(raw) != int(dataSize) || !utf8.Valid(raw) {
		return pressureControlRow{}, errPressureControlRefused
	}
	row.State = string(header.stateRaw)
	if err := json.Unmarshal(raw, &row.Intent); err != nil {
		return pressureControlRow{}, errPressureControlRefused
	}
	canonical, err := json.Marshal(row.Intent)
	if err != nil || !bytes.Equal(canonical, raw) || row.Intent.Action.Key != key || row.Intent.Action.Epoch != string(header.epochRaw) {
		return pressureControlRow{}, errPressureControlRefused
	}
	switch row.State {
	case pressureControlQueued, pressureControlAttempting, pressureControlUnknown, pressureControlAbandoned:
		if resultType != "null" || resultSize != 0 || len(resultRaw) != 0 {
			return pressureControlRow{}, errPressureControlRefused
		}
	case pressureControlApplied, pressureControlRefused:
		if resultType != "text" || resultSize <= 0 || resultSize > pressureControlResultMaxBytes || len(resultRaw) != int(resultSize) {
			return pressureControlRow{}, errPressureControlRefused
		}
		var result pressure.ControlResult
		if err := json.Unmarshal(resultRaw, &result); err != nil {
			return pressureControlRow{}, errPressureControlRefused
		}
		encoded, err := json.Marshal(result)
		if err != nil || !bytes.Equal(encoded, resultRaw) || result.Key != key || result.Applied != (row.State == pressureControlApplied) {
			return pressureControlRow{}, errPressureControlRefused
		}
		row.Result = &result
	default:
		return pressureControlRow{}, errPressureControlRefused
	}
	return row, nil
}

// claimPressureControlIntent records an attempt before a future native call.
// It checks the current process-local original and operator permit again, but
// the later native effect still needs applyPressureNativeControl's final fence.
// Caller holds Manager.mu. No caller exists while native control is disabled.
func (m *Manager) claimPressureControlIntent(key string, candidate pressureNativeCandidate, policy pressure.Policy) (pressureControlAttempt, error) {
	var none pressureControlAttempt
	row, err := readPressureControlRow(m.db, key)
	if err != nil || row.State != pressureControlQueued || !pressureIntentMatchesState(row.Intent, m.pressureState) ||
		candidate.id != row.Intent.Action.Job || row.Intent.Action.PolicyDigest != hex.EncodeToString(candidate.policyDigest[:]) ||
		candidate.leaseRevision != row.Intent.LeaseRevision || candidate.permitRevision != row.Intent.PermitRevision ||
		!m.pressureCandidateStillCurrent(candidate, policy) {
		return none, errPressureControlRefused
	}
	result, err := m.db.Exec(`UPDATE host_pressure_controls SET state='attempting' WHERE key=? AND state='queued' AND epoch=?`,
		key, row.Intent.Action.Epoch)
	if err != nil {
		return none, err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return none, errPressureControlRefused
	}
	return pressureControlAttempt{intent: row.Intent, candidate: candidate}, nil
}

// recordPressureControlResult accepts only an in-process attempt token. The
// caller must establish an actual applied effect or a definite pre-effect
// refusal; uncertain signal outcomes use markPressureControlUnknown instead.
// The returned result may enter Advance only after this durable write commits.
// Caller holds Manager.mu.
func (m *Manager) recordPressureControlResult(attempt pressureControlAttempt, applied bool) (pressure.ControlResult, error) {
	var none pressure.ControlResult
	intent := attempt.intent
	if attempt.candidate.job == nil || attempt.candidate.owner == nil || attempt.candidate.cmd == nil ||
		intent.Action.Job != attempt.candidate.id || intent.LeaseRevision != attempt.candidate.leaseRevision ||
		intent.PermitRevision != attempt.candidate.permitRevision {
		return none, errPressureControlRefused
	}
	row, err := readPressureControlRow(m.db, intent.Action.Key)
	if err != nil || row.State != pressureControlAttempting || row.Intent != intent {
		return none, errPressureControlRefused
	}
	state := pressureControlRefused
	if applied {
		state = pressureControlApplied
	}
	result := pressure.ControlResult{Key: intent.Action.Key, Applied: applied}
	raw, err := json.Marshal(result)
	if err != nil || len(raw) > pressureControlResultMaxBytes {
		return none, errPressureControlRefused
	}
	updated, err := m.db.Exec(`UPDATE host_pressure_controls SET state=?,result=? WHERE key=? AND state='attempting'`, state, string(raw), result.Key)
	if err != nil {
		return none, err
	}
	count, err := updated.RowsAffected()
	if err != nil || count != 1 {
		return none, errPressureControlRefused
	}
	return result, nil
}

// Caller holds Manager.mu.
func (m *Manager) markPressureControlUnknown(attempt pressureControlAttempt) error {
	if attempt.candidate.job == nil || attempt.intent.Action.Job != attempt.candidate.id {
		return errPressureControlRefused
	}
	row, err := readPressureControlRow(m.db, attempt.intent.Action.Key)
	if err != nil || row.State != pressureControlAttempting || row.Intent != attempt.intent {
		return errPressureControlRefused
	}
	updated, err := m.db.Exec(`UPDATE host_pressure_controls SET state='unknown' WHERE key=? AND state='attempting'`, attempt.intent.Action.Key)
	if err != nil {
		return err
	}
	count, err := updated.RowsAffected()
	if err != nil || count != 1 {
		return errPressureControlRefused
	}
	return nil
}

// recoverPressureControlIntents never turns an old durable row into process
// authority. A queued old-epoch action is abandoned; an interrupted native
// attempt is unknown and requires independent original-owner reconciliation.
// Called before accepting the warning-only startup state.
func (m *Manager) validatePressureControlRowsAtStartup() error {
	rows, err := m.db.Query(`SELECT ` + pressureControlHeaderColumns + ` FROM host_pressure_controls ORDER BY rowid LIMIT 257`)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var header pressureControlHeader
		if err := rows.Scan(header.fields()...); err != nil {
			rows.Close()
			return err
		}
		if !header.valid() {
			rows.Close()
			return errPressureControlRefused
		}
		keys = append(keys, string(header.keyRaw))
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(keys) > 256 {
		return fmt.Errorf("pressure: control history exceeds startup bound")
	}
	for _, key := range keys {
		if _, err := readPressureControlRow(m.db, key); err != nil {
			return errPressureControlRefused
		}
	}
	return nil
}

func (m *Manager) recoverPressureControlIntents() error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, update := range []string{
		`UPDATE host_pressure_controls SET state='abandoned' WHERE state='queued'`,
		`UPDATE host_pressure_controls SET state='unknown' WHERE state='attempting'`,
	} {
		if _, err := tx.Exec(update); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// pressureUnknownControlRows is a bounded inspection prerequisite for a
// future original-owner reconciler. Reading an old row never authorizes a
// retry, PID adoption, result injection or release of retained charges.
func (m *Manager) pressureUnknownControlRows() ([]pressureControlRow, error) {
	rows, err := m.db.Query(`SELECT ` + pressureControlHeaderColumns +
		` FROM host_pressure_controls WHERE CAST(state AS BLOB)=CAST('unknown' AS BLOB) ORDER BY rowid LIMIT 257`)
	if err != nil {
		return nil, err
	}
	var keys []string
	for rows.Next() {
		var header pressureControlHeader
		if err := rows.Scan(header.fields()...); err != nil {
			rows.Close()
			return nil, err
		}
		if !header.valid() || string(header.stateRaw) != pressureControlUnknown {
			rows.Close()
			return nil, errPressureControlRefused
		}
		keys = append(keys, string(header.keyRaw))
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	if len(keys) > 256 {
		return nil, fmt.Errorf("pressure: unknown control roster exceeds bound")
	}
	unknown := make([]pressureControlRow, 0, len(keys))
	for _, key := range keys {
		row, err := readPressureControlRow(m.db, key)
		if err != nil || row.State != pressureControlUnknown {
			return nil, errPressureControlRefused
		}
		unknown = append(unknown, row)
	}
	return unknown, nil
}
