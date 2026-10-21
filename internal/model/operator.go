package model

import "time"

// LeaseAction binds a reviewed action to the precise lease revision displayed
// by an operator client. Domain eligibility and ownership are checked again
// under the same manager lock as the action.
type LeaseAction struct {
	ID       string        `json:"id"`
	Revision int           `json:"revision"`
	Action   string        `json:"action"`
	TTL      time.Duration `json:"ttl_ns,omitempty"`
	// Consumers binds a destructive parent action to its displayed attached
	// allocations. A non-nil empty list means the operator reviewed no consumers.
	Consumers []LeaseRevision `json:"consumers"`
}

type LeaseRevision struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}
