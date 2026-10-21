package manager

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"

	"github.com/yrashk/arm/internal/model"
)

type gradleRecord struct {
	Lease            string                       `json:"lease"`
	Storage          string                       `json:"storage"`
	Name             string                       `json:"name"`
	Identity         *gradleDirIdentity           `json:"identity"`
	Children         map[string]gradleDirIdentity `json:"children"`
	Nonce            string                       `json:"nonce"`
	Phase            string                       `json:"phase"`
	Binding          model.WorkloadBinding        `json:"binding"`
	Probe            *model.ProcessIdentity       `json:"probe,omitempty"`
	Uncertain        bool                         `json:"uncertain"`
	BlockedBy        string                       `json:"blocked_by,omitempty"`
	FinalOutcome     string                       `json:"final_outcome,omitempty"`
	ExpectedPID      int                          `json:"expected_pid,omitempty"`
	ServiceHeap      int64                        `json:"service_heap"`
	ServiceMetaspace int64                        `json:"service_metaspace"`
}

func (m *Manager) initGradleRecords() error {
	_, err := m.db.Exec(`CREATE TABLE IF NOT EXISTS gradle_workloads (lease TEXT PRIMARY KEY, data TEXT NOT NULL)`)
	return err
}

func (m *Manager) gradleRecord(id string) (gradleRecord, error) {
	var r gradleRecord
	var raw []byte
	if err := m.db.QueryRow(`SELECT data FROM gradle_workloads WHERE lease=?`, id).Scan(&raw); err != nil {
		return r, err
	}
	if len(raw) > 64<<10 {
		return r, model.Err("invalid_evidence", "Gradle workload record exceeds bound")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&r); err != nil {
		return r, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return r, model.Err("invalid_evidence", "Gradle workload record contains trailing data")
	}
	if r.Lease != id || r.Storage == "" || filepath.Base(r.Name) != r.Name || r.Name == "." || r.Name == ".." || r.Nonce == "" {
		return r, model.Err("invalid_evidence", "Gradle workload record identity is invalid")
	}
	return r, nil
}

func (m *Manager) saveGradleRecord(l *model.Lease, r gradleRecord, kind string) error {
	if l.ID != r.Lease {
		return fmt.Errorf("Gradle workload/lease identity differs")
	}
	binding := r.Binding
	l.Workload = &binding
	l.Revision++
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	record, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(record) > 64<<10 {
		return model.Err("invalid_evidence", "Gradle workload record exceeds bound")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE leases SET data=? WHERE id=?`, string(data), l.ID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return model.Err("invalid_evidence", "Gradle lease disappeared during record update")
	}
	if _, err = tx.Exec(`INSERT INTO gradle_workloads(lease,data) VALUES(?,?) ON CONFLICT(lease) DO UPDATE SET data=excluded.data`, l.ID, string(record)); err != nil {
		return err
	}
	if err := event(tx, kind, l.ID, l); err != nil {
		return err
	}
	return tx.Commit()
}

func isGradleProfile(profile string) bool {
	return profile == "gradle-service" || profile == "gradle-build"
}

func (m *Manager) gradleObligation(l model.Lease) bool {
	return isGradleProfile(l.Request.Profile) || l.Request.ServiceLease != ""
}

func (m *Manager) gradleRecordOptional(id string) (gradleRecord, bool, error) {
	r, err := m.gradleRecord(id)
	if err == sql.ErrNoRows {
		return r, false, nil
	}
	return r, err == nil, err
}
