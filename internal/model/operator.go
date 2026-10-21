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
}
