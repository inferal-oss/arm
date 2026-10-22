package model

import "time"

// NodeMetadata is reported information, not an authentication assertion or a
// remote grant. The identical schema is used for localhost and direct peers.
// Freshness of cached inventory is independent of the response timestamp.
type NodeMetadata struct {
	SchemaVersion int           `json:"schema_version"`
	Host          string        `json:"host"`
	Hostname      string        `json:"hostname,omitempty"`
	Platform      NodePlatform  `json:"platform"`
	Build         NodeBuild     `json:"build"`
	Inventory     NodeInventory `json:"inventory"`
	Capabilities  []string      `json:"capabilities"`
	ReportedAt    time.Time     `json:"reported_at"`
	Unavailable   []string      `json:"unavailable"`
}

type NodePlatform struct {
	OS            string `json:"os"`
	KernelRelease string `json:"kernel_release,omitempty"`
	Architecture  string `json:"process_architecture"`
}

type NodeBuild struct {
	Version   string `json:"arm_version"`
	GoVersion string `json:"go_version"`
}

// Inventory covers the running process's logical CPU inventory, host memory,
// and ARM's state filesystem. It does not enumerate every mount or GPU.
type NodeInventory struct {
	LogicalCPUs     int               `json:"logical_cpus"`
	Memory          NodeInventorySize `json:"host_memory"`
	StateFilesystem NodeInventorySize `json:"state_filesystem"`
}

type NodeInventorySize struct {
	Total      *int64        `json:"total_bytes"`
	ObservedAt *time.Time    `json:"observed_at"`
	Known      bool          `json:"known"`
	Stale      bool          `json:"stale"`
	Age        time.Duration `json:"age_ns"`
	StaleAfter time.Duration `json:"stale_after_ns"`
}
