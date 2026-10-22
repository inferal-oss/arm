package model

import "time"

// CargoBinding is a public projection of the separately journalled Cargo
// workload. Its paths identify born managed output; they confer no authority
// over source, shared Cargo caches or other similarly named directories.
type CargoBinding struct {
	Schema         int        `json:"schema"`
	Adapter        string     `json:"adapter"`
	State          string     `json:"state"`
	Jobs           int        `json:"jobs"`
	CommandSHA256  string     `json:"command_sha256"`
	PreparedSHA256 string     `json:"prepared_sha256,omitempty"`
	Workspace      string     `json:"workspace,omitempty"`
	Target         string     `json:"target,omitempty"`
	Build          string     `json:"build,omitempty"`
	Control        string     `json:"control,omitempty"`
	CargoVersion   string     `json:"cargo_version,omitempty"`
	RustcVersion   string     `json:"rustc_version,omitempty"`
	RustdocVersion string     `json:"rustdoc_version,omitempty"`
	Toolchain      string     `json:"toolchain,omitempty"`
	PreparingAt    *time.Time `json:"preparing_at,omitempty"`
	AuthorizedAt   *time.Time `json:"authorized_at,omitempty"`
	StartedAt      *time.Time `json:"command_started_at,omitempty"`
	Detail         string     `json:"detail"`
}
