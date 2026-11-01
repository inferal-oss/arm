package agentprojects

import (
	"getarm.org/internal/agentsessions"
	"getarm.org/internal/gitmeta"
)

// Membership is bounded display evidence captured for one exact member cwd.
// It is copied into review snapshots and must be freshly revalidated before
// preview and apply. Metadata Git/Common directories are never policy roots.
type Membership struct {
	Member   gitmeta.Directory `json:"member"`
	Checkout *gitmeta.Info     `json:"checkout,omitempty"`
}

// Group is display grouping evidence, not an ARM policy binding or native
// Project identity. Git groups require verified repository relationships;
// directory groups associate only exact canonical directories. Directories is
// explicit exact-cwd evidence, including Directory. Git producers must prove
// common-repository membership before adding a cwd.
type Group struct {
	Key         string                       `json:"group_key"`
	Kind        string                       `json:"kind"`
	Name        string                       `json:"name"`
	Directory   string                       `json:"directory"`
	Directories []string                     `json:"directories"`
	Names       []string                     `json:"session_names,omitempty"`
	Sessions    []agentsessions.SavedSession `json:"sessions"`
	Warnings    []string                     `json:"warnings,omitempty"`
	Memberships []Membership                 `json:"memberships,omitempty"`
}
