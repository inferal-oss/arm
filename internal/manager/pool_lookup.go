package manager

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/yrashk/arm/internal/model"
)

// A missing result is a read-only snapshot, never proof that an in-flight
// request cannot commit. Retain the exact request/key until resolved.
type PoolMutationLookup struct {
	Found   bool                `json:"found"`
	Request *model.PoolMutation `json:"request,omitempty"`
	Change  *model.PoolChange   `json:"change,omitempty"`
}

func (m *Manager) LookupPoolMutation(key string) (PoolMutationLookup, error) {
	if key == "" || len(key) > 128 {
		return PoolMutationLookup{}, model.Err("invalid_request", "pool operation key must contain 1 to 128 bytes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var raw, result []byte
	err := m.db.QueryRow(`SELECT request,result FROM pool_operations WHERE key=?`, key).Scan(&raw, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return PoolMutationLookup{}, nil
	}
	if err != nil {
		return PoolMutationLookup{}, err
	}
	var request model.PoolMutation
	var change model.PoolChange
	if err = json.Unmarshal(raw, &request); err != nil {
		return PoolMutationLookup{}, err
	}
	if err = json.Unmarshal(result, &change); err != nil {
		return PoolMutationLookup{}, err
	}
	if change.Actor != m.owner || change.Pool.Authority != m.host {
		return PoolMutationLookup{}, nil
	}
	return PoolMutationLookup{Found: true, Request: &request, Change: &change}, nil
}
