package model

import (
	"encoding/json"
	"time"
)

// UsagePolicy is resolved before admission and immutable for the lease.
// It governs observed usage, not a kernel quota or guaranteed physical capacity.
type UsagePolicy struct {
	Mode      string        `json:"mode"`
	Window    time.Duration `json:"window_ns"`
	Grace     time.Duration `json:"grace_ns"`
	Warnings  int           `json:"warnings"`
	Tolerance float64       `json:"tolerance"`
}

type UsageIncident struct {
	ID           string         `json:"id"`
	Lease        string         `json:"lease"`
	Host         string         `json:"host"`
	Owner        string         `json:"owner"`
	Agent        string         `json:"agent"`
	Session      string         `json:"session"`
	Registration string         `json:"registration"`
	Resource     string         `json:"resource"`
	Unit         string         `json:"unit"`
	Limit        json.Number    `json:"limit"`
	Threshold    float64        `json:"threshold"`
	Policy       UsagePolicy    `json:"policy"`
	State        string         `json:"state"`
	Outcome      string         `json:"outcome,omitempty"`
	Opened       time.Time      `json:"opened_at"`
	Updated      time.Time      `json:"updated_at"`
	BreachSince  *time.Time     `json:"breach_since,omitempty"`
	Deadline     *time.Time     `json:"deadline,omitempty"`
	WarningCount int            `json:"warning_count"`
	Messages     []string       `json:"messages"`
	Evidence     *WorkloadUsage `json:"evidence,omitempty"`
	Detail       string         `json:"detail"`
	Revision     int            `json:"revision"`
}
