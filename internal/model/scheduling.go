package model

import (
	"encoding/json"
	"time"
)

// QueueInfo is a snapshot, never a future reservation or an entitlement.
type QueueInfo struct {
	Policy    string         `json:"policy"`
	Position  int            `json:"position"`
	Blockers  []QueueBlocker `json:"blockers"`
	Estimate  QueueEstimate  `json:"estimate"`
	Updated   time.Time      `json:"updated_at"`
	Rejection *Error         `json:"rejection,omitempty"`
}

type QueueBlocker struct {
	Code          string       `json:"code"`
	Resource      string       `json:"resource,omitempty"`
	Requested     *json.Number `json:"requested"`
	Available     *json.Number `json:"available"`
	Unit          string       `json:"unit,omitempty"`
	Leases        []string     `json:"blocking_leases,omitempty"`
	BlockingCount int          `json:"blocking_count,omitempty"`
	Detail        string       `json:"detail"`
	ObservationAt *time.Time   `json:"observation_at,omitempty"`
	Stale         bool         `json:"stale,omitempty"`
}

type QueueEstimate struct {
	Kind   string     `json:"kind"`
	Start  *time.Time `json:"start_at"`
	Detail string     `json:"detail"`
}
