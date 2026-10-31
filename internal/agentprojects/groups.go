package agentprojects

import "getarm.org/internal/agentsessions"

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
}
