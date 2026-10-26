package model

import "time"

// CPUAvailability estimates recent allocation headroom in logical-core
// equivalents. It is not a scheduler entitlement or performance guarantee.
// Unknown observations retain any known inventory, but never headroom.
type CPUAvailability struct {
	Known        bool          `json:"known"`
	At           time.Time     `json:"at"`
	Interval     time.Duration `json:"interval_ns"`
	EligibleCPUs *int          `json:"eligible_cpus"`
	Capacity     *float64      `json:"capacity_cores"`  // eligible count capped by observed quotas
	Busy         *float64      `json:"busy_cores"`      // busy time across the eligible CPU set, not cgroup usage
	Available    *float64      `json:"available_cores"` // idle time capped by remaining ancestor quotas
	Scope        string        `json:"scope"`
	Method       string        `json:"method"`
	Quality      string        `json:"quality"`
	Detail       string        `json:"detail"`
}

type CPUBudget struct {
	Observation   CPUAvailability `json:"observation"`
	Ceiling       *float64        `json:"ceiling_cores"`
	Capacity      float64         `json:"capacity_cores"`
	Reserved      float64         `json:"reserved_cores"`
	ManagedCredit float64         `json:"managed_credit_cores"`
	Available     float64         `json:"available_cores"`
	Deficit       float64         `json:"deficit_cores"`
	Known         bool            `json:"known"`
	Stale         bool            `json:"stale"`
	Age           time.Duration   `json:"age_ns"`
	StaleAfter    time.Duration   `json:"stale_after_ns"`
	Accounting    string          `json:"accounting"`
}

// CPUWarningEvidence describes warning confidence only. PossibleCompliant is
// a policy bound assuming supervised work obeys its promises, never a native
// consumption measurement or authority to admit work, pause, or release it.
type CPUWarningEvidence struct {
	State             string        `json:"state"`
	NativeKnown       bool          `json:"native_known"`
	At                time.Time     `json:"at"`
	Interval          time.Duration `json:"interval_ns"`
	Idle              float64       `json:"idle_cores"`
	VerifiedManaged   float64       `json:"verified_managed_cores"`
	PossibleCompliant float64       `json:"possible_compliant_supervised_cores"`
	MinimumCoverage   float64       `json:"minimum_coverage_cores"`
	PossibleCoverage  float64       `json:"possible_compliant_coverage_cores"`
	Detail            string        `json:"detail"`
}
