package model

import "time"

type FileIdentity struct {
	Device          uint64 `json:"device"`
	Inode           uint64 `json:"inode"`
	Owner           uint32 `json:"owner"`
	BornSeconds     int64  `json:"born_seconds"`
	BornNanoseconds int64  `json:"born_nanoseconds"`
	BirthKnown      bool   `json:"birth_known"`
}

type Artifact struct {
	Name             string            `json:"name"`
	Path             string            `json:"path"`
	Identity         *FileIdentity     `json:"identity"`
	Phase            string            `json:"phase"`
	Policy           string            `json:"policy"`
	AllocatedBefore  *int64            `json:"allocated_before_cleanup"`
	ContentObserved  bool              `json:"content_observed"`
	NamespaceRemoved bool              `json:"namespace_removed"`
	DeletedEntries   int               `json:"deleted_entries"`
	LastError        string            `json:"last_error,omitempty"`
	Updated          time.Time         `json:"updated_at"`
	Retention        *StorageRetention `json:"retention,omitempty"`
}

type StoragePreview struct {
	Lease       Lease    `json:"lease"`
	Consumers   []string `json:"consumers"`
	ActivePIDs  []int    `json:"active_pids"`
	Entries     int      `json:"entries"`
	Allocated   int64    `json:"allocated_blocks_bytes"`
	Reclaimable *int64   `json:"reclaimable_bytes"`
	CanClean    bool     `json:"can_clean"`
	Detail      string   `json:"detail"`
}
