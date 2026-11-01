package agentstatus

import (
	"context"
	"fmt"
	"getarm.org/internal/rollout"
)

// SetupProjection contains presentation preferences only. It deliberately omits
// bindings, native client contexts, operation keys and agent associations.
type SetupProjection struct {
	Projects   []SetupSummary `json:"projects"`
	Diagnostic string         `json:"diagnostic,omitempty"`
}
type SetupSummary struct {
	Project        string `json:"project"`
	PolicyRevision int    `json:"policy_revision"`
	BindingsDigest string `json:"bindings_digest"`
	Setup          bool   `json:"setup"`
	Provenance     string `json:"provenance"`
}

func (s OnboardingStore) ProjectSetup(ctx context.Context, reader OnboardingPolicyReader) SetupProjection {
	result := SetupProjection{Projects: []SetupSummary{}}
	state, err := s.read() // one bounded sidecar snapshot per request
	if err != nil {
		result.Diagnostic = "Status setup preferences unavailable; retained evidence was preserved."
		return result
	}
	if len(state.Preferences) == 0 {
		return result
	}
	if reader == nil {
		result.Diagnostic = "Current project policy unavailable; status setup provenance is unknown."
	}
	var projects []rollout.Project
	if reader == nil {
		err = fmt.Errorf("policy unavailable")
	} else {
		projects, err = reader(ctx)
	} // one actual policy snapshot, never one per preference
	if err != nil {
		result.Diagnostic = "Current project policy unavailable; status setup provenance is unknown."
	}
	for _, preference := range state.Preferences {
		provenance := "unknown"
		if err == nil {
			actual, e := BuildOnboardingScopes(projects, []string{preference.Scope.Project})
			provenance = "stale"
			if e == nil && onboardingDigest(actual[0]) == onboardingDigest(preference.Scope) {
				provenance = "matching"
			}
		}
		result.Projects = append(result.Projects, SetupSummary{Project: preference.Scope.Project, PolicyRevision: preference.Scope.PolicyRevision, BindingsDigest: preference.Scope.BindingsDigest, Setup: preference.Setup, Provenance: provenance})
	}
	return result
}
