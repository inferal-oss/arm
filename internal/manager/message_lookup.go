package manager

import (
	"database/sql"
	"errors"

	"github.com/inferal-oss/arm/internal/model"
)

// MessageLookup inspects a previously submitted operation without delivering or
// retrying it. Found=false is a snapshot, not proof that an in-flight send cannot
// subsequently commit. Clients must retain the original request and key.
type MessageLookup struct {
	Found   bool     `json:"found"`
	Message *Message `json:"message,omitempty"`
}

func (m *Manager) LookupMessage(key string) (MessageLookup, error) {
	if key == "" || len(key) > 128 {
		return MessageLookup{}, model.Err("invalid_request", "message key must contain 1 to 128 bytes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lookupMessageLocked(key)
}

func (m *Manager) lookupMessageLocked(key string) (MessageLookup, error) {
	var id string
	err := m.db.QueryRow(`SELECT message FROM message_operations WHERE key=?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return MessageLookup{}, nil
	}
	if err != nil {
		return MessageLookup{}, err
	}
	msg, err := m.message(id)
	if err != nil {
		return MessageLookup{}, err
	}
	return MessageLookup{Found: true, Message: &msg}, nil
}
