package model

import "time"

// ExportSelection names one regular file relative to a managed allocation and
// its relative path inside a new exported bundle. Directories are not adopted.
type ExportSelection struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

type StorageExportRequest struct {
	Key         string            `json:"idempotency_key"`
	Lease       string            `json:"lease"`
	Revision    int               `json:"revision,omitempty"`
	Destination string            `json:"destination"`
	Files       []ExportSelection `json:"files"`
	Reason      string            `json:"reason"`
}

type ExportFile struct {
	ExportSelection
	Identity       FileIdentity  `json:"source_identity"`
	Size           int64         `json:"size_bytes"`
	Mode           uint32        `json:"mode"`
	Links          uint64        `json:"links"`
	ModifiedSecond int64         `json:"modified_seconds"`
	ModifiedNanos  int64         `json:"modified_nanoseconds"`
	ChangedSecond  int64         `json:"changed_seconds"`
	ChangedNanos   int64         `json:"changed_nanoseconds"`
	SHA256         string        `json:"sha256,omitempty"`
	OutputIdentity *FileIdentity `json:"output_identity,omitempty"`
}

type StorageExportPlan struct {
	LeaseRevision  int          `json:"lease_revision"`
	SourceExpires  time.Time    `json:"source_expires_at"`
	Source         Artifact     `json:"source"`
	Destination    string       `json:"destination"`
	ParentIdentity FileIdentity `json:"parent_identity"`
	Files          []ExportFile `json:"files"`
	Bytes          int64        `json:"bytes"`
	Detail         string       `json:"detail"`
}

// StorageExport records namespace publication, not exclusive disk reclamation.
// Uncertain/partial output is preserved until explicit operator disposition.
type StorageExport struct {
	ID                  string               `json:"id"`
	Request             StorageExportRequest `json:"request"`
	Host                string               `json:"host"`
	Owner               string               `json:"owner"`
	OwnerUsername       string               `json:"owner_username,omitempty"`
	Plan                StorageExportPlan    `json:"plan"`
	State               string               `json:"state"`
	Running             bool                 `json:"running"`
	StageName           string               `json:"stage_name"`
	StagePath           string               `json:"staging_path"`
	StageIdentity       *FileIdentity        `json:"stage_identity,omitempty"`
	DestinationIdentity *FileIdentity        `json:"destination_identity,omitempty"`
	Created             time.Time            `json:"created_at"`
	Updated             time.Time            `json:"updated_at"`
	Error               string               `json:"error,omitempty"`
	Detail              string               `json:"detail"`
	NextCommand         []string             `json:"next_command,omitempty"`
	Handoff             *ExportHandoff       `json:"handoff,omitempty"`
}

type ExportHandoff struct {
	Actor         string    `json:"actor"`
	ActorUsername string    `json:"actor_username,omitempty"`
	At            time.Time `json:"at"`
	Reason        string    `json:"reason"`
}

type StorageRetention struct {
	At      time.Time `json:"at"`
	Expires time.Time `json:"expires_at"`
	Reason  string    `json:"reason"`
}

// Lists stay bounded independently of selected-file manifests. Inspect an
// operation for full reasons, errors, paths and file evidence.
type StorageExportSummary struct {
	ID              string    `json:"id"`
	Key             string    `json:"idempotency_key"`
	Lease           string    `json:"lease"`
	State           string    `json:"state"`
	Running         bool      `json:"running"`
	Owner           string    `json:"owner"`
	OwnerUsername   string    `json:"owner_username,omitempty"`
	Destination     string    `json:"destination"`
	Files           int       `json:"files"`
	Bytes           int64     `json:"bytes"`
	Reason          string    `json:"reason"`
	ReasonTruncated bool      `json:"reason_truncated"`
	Error           string    `json:"error,omitempty"`
	ErrorTruncated  bool      `json:"error_truncated"`
	Created         time.Time `json:"created_at"`
	Updated         time.Time `json:"updated_at"`
}
