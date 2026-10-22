// Package workspaceapi contains the native operator DTOs. These are not routed
// over the direct-peer RPC or workspace protocol and confer no transport grant.
package workspaceapi

import (
	"encoding/json"
	"github.com/yrashk/arm/internal/workspace"
	"time"
)

type Summary struct {
	Workspace  string    `json:"workspace"`
	Generation string    `json:"generation"`
	Name       string    `json:"name"`
	Role       string    `json:"role"`
	State      string    `json:"state"`
	Revision   int64     `json:"revision"`
	Directory  string    `json:"directory_host"`
	Members    int       `json:"members"`
	Unresolved int       `json:"unresolved_operations"`
	Updated    time.Time `json:"updated_at"`
}
type List struct {
	Initialized bool      `json:"initialized"`
	Workspaces  []Summary `json:"workspaces"`
}
type InspectRequest struct {
	Workspace string `json:"workspace"`
	Section   string `json:"section"`
	Offset    int    `json:"offset"`
	Limit     int    `json:"limit"`
}
type Page struct {
	Workspace workspace.Instance `json:"workspace"`
	Section   string             `json:"section"`
	Offset    int                `json:"offset"`
	Total     int                `json:"total"`
	Next      *int               `json:"next_offset,omitempty"`
	Items     []json.RawMessage  `json:"items"`
}
type Selector struct {
	Workspace string `json:"workspace"`
}
type Key struct {
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
}
type Lookup struct {
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
	Principal string `json:"principal,omitempty"`
}
type LookupResult struct {
	Found     bool                 `json:"found"`
	Operation *workspace.Operation `json:"operation,omitempty"`
}
type Decision struct {
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
	Revision  int64  `json:"revision"`
	Review    string `json:"review_sha256"`
	Approve   bool   `json:"approve"`
}
type AdmissionCommit struct {
	Request workspace.NativeAdmissionRequest `json:"request"`
	Review  string                           `json:"review_sha256"`
}
type Join struct {
	Key        string               `json:"key"`
	Invitation workspace.Invitation `json:"invitation"`
	Introducer bool                 `json:"introducer"`
	Reason     string               `json:"reason"`
}
type Mutation struct {
	Workspace string                    `json:"workspace"`
	Request   workspace.MutationRequest `json:"request"`
}
type Cancel struct {
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
	Source    string `json:"source_host,omitempty"`
	Revision  int64  `json:"revision"`
}
type Finalize struct {
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
	Revision  int64  `json:"revision"`
}
type Recovery struct {
	Kind    string                    `json:"kind"`
	Request workspace.RecoveryRequest `json:"request"`
}
type RecoveryRun struct {
	Workspace string `json:"workspace"`
	Key       string `json:"key"`
	Revision  int64  `json:"revision"`
}
type RefreshResult struct {
	GraphHash       string    `json:"graph_sha256,omitempty"`
	GraphRevision   int64     `json:"graph_revision,omitempty"`
	GraphValidUntil time.Time `json:"graph_valid_until,omitempty"`
	Workspace       string    `json:"workspace"`
	Revision        int64     `json:"directory_revision"`
	Members         int       `json:"members"`
	ValidUntil      time.Time `json:"valid_until"`
}
type RecoveryPrepare struct {
	Kind              string             `json:"kind"`
	Workspace         string             `json:"workspace"`
	OriginalKey       string             `json:"original_key"`
	OriginalPrincipal string             `json:"original_principal,omitempty"`
	OriginalRevision  int64              `json:"original_revision"`
	Key               string             `json:"key"`
	PermitProof       bool               `json:"permit_proof"`
	CandidateConsent  *workspace.Consent `json:"candidate_consent,omitempty"`
	Reason            string             `json:"reason"`
}
