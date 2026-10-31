package manager

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
	"unicode/utf8"

	"getarm.org/internal/model"
	"getarm.org/internal/pressure"
)

const pressurePermitMaxBytes = 4096
const pressurePermitClass = "generic-local-execution"
const pressurePermitIssuer = "local-operator"

// Startup validates each physical key before the private permit reader can
// use it. Historical permits remain reset blockers, even when well formed.
func (m *Manager) validatePressurePermitRowsAtStartup() error {
	rows, err := m.db.Query(`SELECT typeof(lease),length(CAST(lease AS BLOB)),
		CASE WHEN typeof(lease)='text' AND length(CAST(lease AS BLOB)) BETWEEN 1 AND 128 THEN CAST(lease AS BLOB) END
		FROM host_pressure_permits ORDER BY rowid LIMIT 257`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var kind string
		var size int64
		var raw []byte
		if err := rows.Scan(&kind, &size, &raw); err != nil {
			rows.Close()
			return err
		}
		if kind != "text" || size <= 0 || size > 128 || len(raw) != int(size) || !utf8.Valid(raw) {
			rows.Close()
			return errPressureControlRefused
		}
		ids = append(ids, string(raw))
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if len(ids) > 256 {
		return fmt.Errorf("pressure: permit history exceeds startup bound")
	}
	for _, id := range ids {
		if _, err := m.readPressurePermit(id); err != nil {
			return errPressureControlRefused
		}
	}
	return nil
}

// pressureNativePermit is operator policy evidence, never process authority.
// It is deliberately outside model.Request and cannot be supplied by Acquire.
// Restart may read this record for audit, but only a current process-local
// original owner can mint a pressureNativeGrant for native control.
type pressureNativePermit struct {
	LeaseID       string    `json:"lease_id"`
	LeaseRevision int       `json:"lease_revision"`
	Host          string    `json:"host"`
	Owner         string    `json:"owner"`
	RequestSHA256 string    `json:"request_sha256"`
	ProcessSHA256 string    `json:"process_sha256"`
	PolicySHA256  string    `json:"policy_sha256"`
	Class         string    `json:"class"`
	Issuer        string    `json:"issuer"`
	Reason        string    `json:"reason"`
	Revision      int64     `json:"revision"`
	IssuedAt      time.Time `json:"issued_at"`
}

func pressureDigest(value any) (string, bool) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), true
}

func pressurePermittablePolicy(p pressure.Policy) bool {
	cpu, ok := p.Resources[pressure.CPU]
	if !ok || !cpu.AllowPause || p.MaxPauseJobs != 1 || p.MaxJobs <= 0 || p.MaxJobs > 256 {
		return false
	}
	for resource, threshold := range p.Resources {
		if resource != pressure.CPU && threshold.AllowPause {
			return false
		}
	}
	return true
}

// issuePressurePermitAsOperator is an unused, private writer for a future
// authenticated local operator route. expected=0 creates a first permit;
// positive expected is a compare-and-swap replacement. No request, profile
// selector, recovered PID or merely supplied policy can call this writer.
// Caller holds Manager.mu; this method refuses a stopped or reserved owner.
func (m *Manager) issuePressurePermitAsOperator(id string, p pressure.Policy, expected int64, reason string) (pressureNativePermit, error) {
	var none pressureNativePermit
	if id == "" || expected < 0 || len(reason) == 0 || len(reason) > 1024 || !utf8.ValidString(reason) ||
		!pressurePermittablePolicy(p) || m.closing || m.draining || m.pressureEpoch == "" {
		return none, errPressureControlRefused
	}
	l, err := m.get(id)
	if err != nil || !m.pressureGenericExecution(l) {
		return none, errPressureControlRefused
	}
	j := m.jobs[id]
	if j == nil || j.pressureEffectActive || j.originalCancellationRequested || !m.pressureOriginalBinding(id, l, j) {
		return none, errPressureControlRefused
	}
	if paused, available := j.childOwner.TryPaused(); !available || paused {
		return none, errPressureControlRefused
	}
	for jid := range m.pressureState.Pauses {
		if jid.LeaseID == id {
			return none, errPressureControlRefused
		}
	}
	for _, unresolved := range m.pressureState.Unresolved {
		if unresolved.Record.Job.LeaseID == id {
			return none, errPressureControlRefused
		}
	}
	remaining, err := m.leaseRemaining(l)
	if err != nil || remaining <= 0 {
		return none, errPressureControlRefused
	}
	requestHash, ok := pressureDigest(l.Request)
	if !ok {
		return none, errPressureControlRefused
	}
	processHash, ok := pressureDigest(j.identity)
	if !ok {
		return none, errPressureControlRefused
	}
	policyHash, ok := pressureDigest(p)
	if !ok {
		return none, errPressureControlRefused
	}
	if expected == int64(^uint64(0)>>1) {
		return none, errPressureControlRefused
	}
	permit := pressureNativePermit{LeaseID: id, LeaseRevision: l.Revision, Host: l.Host, Owner: l.Owner,
		RequestSHA256: requestHash, ProcessSHA256: processHash, PolicySHA256: policyHash,
		Class: pressurePermitClass, Issuer: pressurePermitIssuer, Reason: reason, Revision: expected + 1, IssuedAt: time.Now().UTC()}
	raw, err := json.Marshal(permit)
	if err != nil || len(raw) > pressurePermitMaxBytes {
		return none, errPressureControlRefused
	}
	if expected == 0 {
		_, err = m.db.Exec(`INSERT INTO host_pressure_permits(lease,revision,data) VALUES(?,?,?)`, id, permit.Revision, string(raw))
	} else {
		prior, readErr := m.readPressurePermit(id)
		if readErr != nil || prior.Revision != expected || prior.Issuer != pressurePermitIssuer {
			return none, errPressureControlRefused
		}
		var result sql.Result
		result, err = m.db.Exec(`UPDATE host_pressure_permits SET revision=?,data=? WHERE lease=? AND revision=?`,
			permit.Revision, string(raw), id, expected)
		if err == nil {
			rows, rowsErr := result.RowsAffected()
			if rowsErr != nil || rows != 1 {
				return none, errPressureControlRefused
			}
		}
	}
	if err != nil {
		return none, err
	}
	return permit, nil
}

// readPressurePermit requires canonical bounded TEXT and its physical row key.
// A damaged or substituted row cannot be promoted into a process-local grant.
// Caller holds Manager.mu.
func (m *Manager) readPressurePermit(id string) (pressureNativePermit, error) {
	var permit pressureNativePermit
	var key, revisionType, dataType string
	var revision sql.NullInt64
	var size int64
	var raw []byte
	err := m.db.QueryRow(`SELECT lease,typeof(revision),CASE WHEN typeof(revision)='integer' THEN revision END,
		typeof(data),length(CAST(data AS BLOB)),
		CASE WHEN typeof(data)='text' AND length(CAST(data AS BLOB))<=? THEN substr(CAST(data AS BLOB),1,?) END
		FROM host_pressure_permits WHERE lease=?`, pressurePermitMaxBytes, pressurePermitMaxBytes+1, id).
		Scan(&key, &revisionType, &revision, &dataType, &size, &raw)
	if err != nil {
		return permit, err
	}
	if key != id || revisionType != "integer" || !revision.Valid || revision.Int64 <= 0 || dataType != "text" || size <= 0 || size > pressurePermitMaxBytes || len(raw) != int(size) {
		return permit, errPressureControlRefused
	}
	if err := json.Unmarshal(raw, &permit); err != nil {
		return pressureNativePermit{}, errPressureControlRefused
	}
	canonical, err := json.Marshal(permit)
	if err != nil || !bytes.Equal(canonical, raw) || permit.LeaseID != id || permit.Revision != revision.Int64 ||
		permit.Class != pressurePermitClass || permit.Issuer != pressurePermitIssuer || permit.Reason == "" || len(permit.Reason) > 1024 ||
		permit.IssuedAt.IsZero() || !utf8.ValidString(permit.Reason) {
		return pressureNativePermit{}, errPressureControlRefused
	}
	return permit, nil
}

// pressurePermitCurrent rechecks an explicit operator decision and current
// original binding. It does not mint a native handle from the durable record.
// Caller holds Manager.mu.
func (m *Manager) pressurePermitCurrent(id string, l model.Lease, j *job, p pressure.Policy) (pressureNativePermit, bool) {
	var none pressureNativePermit
	if !pressurePermittablePolicy(p) || !m.pressureOriginalBindingFields(id, l, j) {
		return none, false
	}
	permit, err := m.readPressurePermit(id)
	if err != nil || permit.LeaseRevision != l.Revision || permit.Host != l.Host || permit.Owner != l.Owner {
		return none, false
	}
	requestHash, requestOK := pressureDigest(l.Request)
	processHash, processOK := pressureDigest(j.identity)
	policyHash, policyOK := pressureDigest(p)
	if !requestOK || !processOK || !policyOK || permit.RequestSHA256 != requestHash ||
		permit.ProcessSHA256 != processHash || permit.PolicySHA256 != policyHash ||
		!reflect.DeepEqual(l.Process, j.identity) {
		return none, false
	}
	return permit, true
}
