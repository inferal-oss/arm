package model

import "time"

// Observed identity is evidence, not permission to signal a process.
type ProcessIdentity struct {
	PID       int    `json:"pid"`
	PGID      int    `json:"pgid"`
	UID       string `json:"uid"`
	Start     string `json:"start"`
	Namespace string `json:"namespace"`
}

type ProcessUsage struct {
	Identity             ProcessIdentity `json:"identity"`
	CPUSeconds           *float64        `json:"cpu_seconds"`
	ResidentBytes        *uint64         `json:"resident_bytes"`
	AccountedMemoryBytes *uint64         `json:"accounted_memory_bytes"`
	MemoryMetric         string          `json:"memory_metric"`
	Detail               string          `json:"detail,omitempty"`
}

type WorkloadUsage struct {
	Observed             time.Time       `json:"observed_at"`
	Live                 bool            `json:"live"`
	Stale                bool            `json:"stale"`
	Root                 ProcessIdentity `json:"root"`
	IdentityVerified     bool            `json:"identity_verified"`
	Complete             bool            `json:"complete"`
	CPUCores             *float64        `json:"cpu_cores"`
	CPUQuality           string          `json:"cpu_quality"`
	Window               time.Duration   `json:"window_ns"`
	AccountedMemoryBytes *uint64         `json:"accounted_memory_bytes"`
	MemoryMetric         string          `json:"memory_metric"`
	MemoryQuality        string          `json:"memory_quality"`
	MemberCount          int             `json:"member_count"`
	Method               string          `json:"method"`
	Scope                string          `json:"scope"`
	Detail               string          `json:"detail"`
	Members              []ProcessUsage  `json:"members"`
}

type UsageView struct {
	Lease  string         `json:"lease"`
	Host   string         `json:"host"`
	State  string         `json:"state"`
	Usage  *WorkloadUsage `json:"usage"`
	Detail string         `json:"detail,omitempty"`
}
