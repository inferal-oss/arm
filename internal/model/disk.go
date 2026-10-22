package model

import "time"

// DiskAvailability is reported nonprivileged headroom for ARM's state
// filesystem, not exclusive extents, purgeable space or a guaranteed quota.
type DiskAvailability struct {
	At         time.Time `json:"at"`
	Total      *int64    `json:"total_bytes"`
	Available  *int64    `json:"available_bytes"`
	Scope      string    `json:"scope"`
	Path       string    `json:"path,omitempty"`
	Filesystem string    `json:"filesystem"`
	Volume     string    `json:"volume"`
	Pool       string    `json:"pool,omitempty"`
	Shared     bool      `json:"shared"`
	Method     string    `json:"method"`
	Quality    string    `json:"quality"`
	Detail     string    `json:"detail"`
}
type DiskBudget struct {
	Observation DiskAvailability `json:"observation"`
	Ceiling     *int64           `json:"ceiling_bytes"`
	Capacity    int64            `json:"capacity_bytes"`
	Reserved    int64            `json:"reserved_bytes"`
	Available   int64            `json:"available_bytes"`
	Deficit     int64            `json:"deficit_bytes"`
	Known       bool             `json:"known"`
	Stale       bool             `json:"stale"`
	Age         time.Duration    `json:"age_ns"`
	StaleAfter  time.Duration    `json:"stale_after_ns"`
	Accounting  string           `json:"accounting"`
}
