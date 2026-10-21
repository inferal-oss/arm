package model

import "time"

// MemoryAvailability is an estimate of host allocation headroom, not a quota
// or a guarantee that the next allocation will succeed. Nil is unknown.
type MemoryAvailability struct {
	At        time.Time `json:"at"`
	Total     *int64    `json:"total_bytes"`
	Available *int64    `json:"available_bytes"`
	Scope     string    `json:"scope"`
	Method    string    `json:"method"`
	Quality   string    `json:"quality"`
	Detail    string    `json:"detail"`
}

type MemoryBudget struct {
	Observation MemoryAvailability `json:"observation"`
	Ceiling     *int64             `json:"ceiling_bytes"`
	Capacity    int64              `json:"capacity_bytes"`
	Reserved    int64              `json:"reserved_bytes"`
	Available   int64              `json:"available_bytes"`
	Deficit     int64              `json:"deficit_bytes"`
	Stale       bool               `json:"stale"`
	Age         time.Duration      `json:"age_ns"`
	StaleAfter  time.Duration      `json:"stale_after_ns"`
	Accounting  string             `json:"accounting"`
}
