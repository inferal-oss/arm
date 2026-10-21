package model

import "time"

type BulkEndRequest struct {
	Action string `json:"action"`
	Key    string `json:"idempotency_key"`
}

type LeaseResult struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Outcome string `json:"outcome,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

type BulkEndReport struct {
	Key       string        `json:"idempotency_key"`
	Action    string        `json:"action"`
	At        time.Time     `json:"started_at"`
	Results   []LeaseResult `json:"results"`
	Remaining []LeaseResult `json:"remaining"`
}

type ShutdownRequest struct {
	Force   bool          `json:"force"`
	Wait    bool          `json:"wait"`
	Timeout time.Duration `json:"timeout_ns,omitempty"`
}

type ShutdownReport struct {
	Stopped         bool          `json:"stopped"`
	ID              string        `json:"id"`
	Actor           string        `json:"actor"`
	ActorUsername   string        `json:"actor_username,omitempty"`
	Phase           string        `json:"phase"`
	Started         time.Time     `json:"started_at"`
	Deadline        *time.Time    `json:"force_at,omitempty"`
	Timeout         time.Duration `json:"timeout_ns,omitempty"`
	ForceApplied    bool          `json:"force_applied"`
	CancelledQueued []LeaseResult `json:"cancelled_queued"`
	Results         []LeaseResult `json:"results"`
	Remaining       []LeaseResult `json:"remaining"`
	Detail          string        `json:"detail"`
}
