package manager

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/nodeavailability"
)

const nodeAvailabilityReadSQL = `SELECT typeof(value),coalesce(length(CAST(value AS BLOB)),-1),CASE WHEN typeof(value)='text' AND length(CAST(value AS BLOB))<=? THEN value ELSE '' END FROM metadata WHERE key=?`

func (m *Manager) nodeAvailabilityDocument() (nodeavailability.Document, error) {
	var raw string
	var kind string
	var size int64
	err := m.db.QueryRow(nodeAvailabilityReadSQL, nodeavailability.MaxDocumentBytes, nodeavailability.MetadataKey).Scan(&kind, &size, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nodeavailability.Document{}, nil
	}
	if err != nil {
		return nodeavailability.Document{}, err
	}
	if kind != "text" || size < 0 || size > nodeavailability.MaxDocumentBytes {
		return nodeavailability.Document{}, model.Err("availability_metadata_invalid", "availability metadata has unsupported storage type or exceeds8KiB")
	}
	doc, err := nodeavailability.DecodeDocument([]byte(raw), m.host, m.owner)
	if err != nil {
		return nodeavailability.Document{}, model.Err("availability_metadata_invalid", "Owner availability metadata is invalid; preserve it and inspect the current record before changing it")
	}
	return doc, nil
}

func (m *Manager) NodeAvailability() (nodeavailability.Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, err := m.nodeAvailabilityDocument()
	return doc.Report(m.host), err
}

// SetNodeAvailability is self-owner metadata only. The API caller must be the
// authenticated local owner; no peer operation may invoke it. Current last-key
// retry is exact; after intervening edits, a stale revision never applies again.
func (m *Manager) SetNodeAvailability(update nodeavailability.Update) (nodeavailability.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := update.Validate(); err != nil {
		return nodeavailability.Document{}, model.Err("invalid_request", err.Error())
	}
	if m.closing {
		return nodeavailability.Document{}, model.Err("backend_stopping", "availability update unavailable during shutdown")
	}
	old, err := m.nodeAvailabilityDocument()
	if err != nil {
		return old, err
	}
	if old.Revision > 0 && old.LastUpdate.Key == update.Key {
		if old.LastUpdate != update {
			return old, model.Err("idempotency_conflict", "current availability key belongs to a different update")
		}
		return old, nil
	}
	if old.Revision != update.ExpectedRevision || old.Revision == ^uint64(0) {
		return old, model.Err("revision_conflict", "availability changed; an earlier update may have applied. Read the current profile before another explicit change")
	}
	doc := nodeavailability.Document{Schema: 1, Host: m.host, Owner: m.owner, Revision: old.Revision + 1, Profile: update.Profile, Reason: update.Reason, UpdatedAt: time.Now().UTC(), LastUpdate: update}
	raw, err := json.Marshal(doc)
	if err != nil {
		return old, err
	}
	if _, err = nodeavailability.DecodeDocument(raw, m.host, m.owner); err != nil {
		return old, err
	}
	_, err = m.db.Exec(`INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, nodeavailability.MetadataKey, string(raw))
	if err != nil {
		return old, model.Err("availability_update_unknown", "availability metadata write result is uncertain; read current profile and retain the original key/revision")
	}
	return doc, nil
}
