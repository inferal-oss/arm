package model

import "time"

// Reviews are optional conversations, never evidence of compliance or authority.
type LeaseReviewPolicy struct {
	Generation         int64         `json:"policy_generation"`
	Lease              string        `json:"lease"`
	Revision           int           `json:"lease_revision"`
	Enabled            bool          `json:"enabled"`
	Interval           time.Duration `json:"interval_ns"`
	ContentionInterval time.Duration `json:"contention_interval_ns"`
	ReplyTimeout       time.Duration `json:"reply_timeout_ns"`
	Updated            time.Time     `json:"updated_at"`
	Next               time.Time     `json:"next_at"`
}
type LeaseReviewConfigure struct {
	Generation         *int64        `json:"policy_generation"`
	Lease              string        `json:"lease"`
	Revision           int           `json:"lease_revision"`
	Enabled            bool          `json:"enabled"`
	Interval           time.Duration `json:"interval_ns"`
	ContentionInterval time.Duration `json:"contention_interval_ns"`
	ReplyTimeout       time.Duration `json:"reply_timeout_ns"`
}
type LeaseReviewUsage struct {
	Observed      *time.Time `json:"observed_at,omitempty"`
	CPUCores      *float64   `json:"cpu_cores"`
	CPUQuality    string     `json:"cpu_quality"`
	MemoryBytes   *uint64    `json:"memory_bytes"`
	MemoryQuality string     `json:"memory_quality"`
	Stale         bool       `json:"stale"`
	Complete      bool       `json:"complete"`
	Detail        string     `json:"detail"`
}
type LeaseReviewContext struct {
	PolicyGeneration int64            `json:"policy_generation"`
	Lease            string           `json:"lease"`
	Revision         int              `json:"lease_revision"`
	Owner            string           `json:"owner"`
	Purpose          string           `json:"purpose"`
	Resources        Resources        `json:"resources"`
	Pools            []PoolClaim      `json:"pools,omitempty"`
	Ports            []PortClaim      `json:"ports,omitempty"`
	Expires          time.Time        `json:"expires_at"`
	Remaining        time.Duration    `json:"remaining_ns"`
	Usage            LeaseReviewUsage `json:"usage"`
	QueuedDemand     int              `json:"queued_demand"`
}
type LeaseReviewReply struct {
	ID           string `json:"review"`
	Agent        string `json:"agent"`
	Session      string `json:"session"`
	Registration string `json:"registration"`
	Intent       string `json:"intent"` // needed, release, renew or resize; all are inert intent.
	Explanation  string `json:"explanation"`
}
type LeaseReview struct {
	Sequence     int64                `json:"sequence"`
	ID           string               `json:"id"`
	Host         string               `json:"host"`
	Agent        string               `json:"agent"`
	Session      string               `json:"session"`
	Registration string               `json:"registration"`
	Message      string               `json:"message"`
	Created      time.Time            `json:"created_at"`
	Deadline     time.Time            `json:"reply_deadline_at"`
	State        string               `json:"state"` // pending, replied, timed_out or superseded.
	Closed       *time.Time           `json:"closed_at,omitempty"`
	Detail       string               `json:"detail,omitempty"`
	Context      []LeaseReviewContext `json:"leases"`
	Reply        *LeaseReviewReply    `json:"reply,omitempty"`
}
type LeaseReviewQuery struct {
	After   int64  `json:"after,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Lease   string `json:"lease,omitempty"`
	Pending bool   `json:"pending,omitempty"`
}
type LeaseReviewPage struct {
	SchedulerError string        `json:"scheduler_error,omitempty"`
	Items          []LeaseReview `json:"items"`
	Cursor         int64         `json:"cursor"`
	More           bool          `json:"more"`
	MaxPending     int           `json:"max_pending"`
	RetainedClosed int           `json:"retained_closed"`
}
