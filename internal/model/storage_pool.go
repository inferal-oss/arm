package model

import "time"

// StoragePoolConfig selects an existing directory, never adopts its contents.
// ARM creates a private managed root below Path on the first allocation.
type StoragePoolConfig struct {
	Name    string `json:"name" toml:"name"`
	Path    string `json:"path" toml:"path"`
	Tier    string `json:"tier,omitempty" toml:"tier"`
	Ceiling int64  `json:"ceiling_bytes,omitempty" toml:"-"`
}

type StorageDevice struct {
	Device     string `json:"device,omitempty"`
	Model      string `json:"model,omitempty"`
	Transport  string `json:"transport,omitempty"`
	SolidState *bool  `json:"solid_state,omitempty"`
	Internal   *bool  `json:"internal,omitempty"`
}

type StorageLocation struct {
	Path         string          `json:"path"`
	Mount        string          `json:"mount"`
	Source       string          `json:"source"`
	Filesystem   string          `json:"filesystem"`
	Volume       string          `json:"volume"`
	FilesystemID string          `json:"filesystem_id"`
	Backing      string          `json:"backing_pool"`
	Devices      []StorageDevice `json:"devices"`
	Method       string          `json:"method"`
	Detail       string          `json:"detail"`
	At           time.Time       `json:"at"`
}

type StoragePerformance struct {
	At                  time.Time     `json:"at"`
	Volume              string        `json:"volume"`
	Bytes               int64         `json:"bytes"`
	BlockBytes          int           `json:"block_bytes"`
	ReadBytesPerSecond  float64       `json:"read_bytes_per_second"`
	WriteBytesPerSecond float64       `json:"write_bytes_per_second"`
	RandomReadIOPS      float64       `json:"random_read_iops"`
	RandomReadLatencyNS int64         `json:"random_read_latency_ns"`
	Duration            time.Duration `json:"duration_ns"`
	Method              string        `json:"method"`
	Detail              string        `json:"detail"`
}

// StoragePoolRecord binds durable ownership to the original location. Removing
// its config stops new admission; its identity and unresolved leases survive.
type StoragePoolRecord struct {
	Name        string              `json:"name"`
	Path        string              `json:"path"`
	Volume      string              `json:"volume"`
	Backing     string              `json:"backing_pool"`
	Root        *FileIdentity       `json:"managed_root,omitempty"`
	Performance *StoragePerformance `json:"performance,omitempty"`
}

type StoragePoolView struct {
	Name             string              `json:"name"`
	Tier             string              `json:"tier,omitempty"`
	Enabled          bool                `json:"enabled"`
	Location         StorageLocation     `json:"location"`
	Budget           DiskBudget          `json:"budget"`
	Performance      *StoragePerformance `json:"performance,omitempty"`
	PerformanceStale bool                `json:"performance_stale"`
	Detail           string              `json:"detail"`
}

type StoragePoolBinding struct {
	Name          string `json:"name"`
	Volume        string `json:"volume"`
	Backing       string `json:"backing_pool"`
	SharesDefault bool   `json:"shares_default"`
}

// StorageBenchmarkResult keeps the observed profile separate from the owned
// cleanup obligation. Namespace removal does not prove exclusive reclamation.
type StorageBenchmarkResult struct {
	StoragePerformance
	StorageLease   string `json:"storage_lease"`
	ExecutionLease string `json:"execution_lease"`
	CleanupState   string `json:"cleanup_state"`
}
