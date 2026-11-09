package agentstatus

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"getarm.org/internal/rollout"
)

func onboardingProjectID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}
func onboardingScopeValid(scope OnboardingScope) bool {
	if !onboardingProjectID(scope.Project) || scope.PolicyRevision < 1 || !hexID(scope.BindingsDigest, 64) || len(scope.Bindings) == 0 || len(scope.Bindings) > 128 {
		return false
	}
	previous := ""
	for _, b := range scope.Bindings {
		if b.Removed || !filepath.IsAbs(b.Root) || len(b.Root) > 4096 || !utf8.ValidString(b.Root) || strings.ContainsAny(b.Root, "\x00\r\n") || b.Identity == "" || len(b.Identity) > 512 {
			return false
		}
		switch b.Kind {
		case "directory":
			if b.GitDirectory != "" || b.GitIdentity != "" {
				return false
			}
		case "git", rollout.WorktreesKind:
			if !filepath.IsAbs(b.GitDirectory) || len(b.GitDirectory) > 4096 || b.GitIdentity == "" || len(b.GitIdentity) > 512 {
				return false
			}
		default:
			return false
		}
		raw, _ := json.Marshal(b)
		if previous != "" && string(raw) <= previous {
			return false
		}
		previous = string(raw)
	}
	return onboardingDigest(scope.Bindings) == scope.BindingsDigest
}
func (s OnboardingState) validate() error {
	bad := func() error { return fmt.Errorf("malformed status setup state; preserved") }
	if s.Version != 1 || s.Revision != len(s.Operations) || s.Preferences == nil || s.Operations == nil || len(s.Preferences) > 128 || len(s.Operations) > 256 {
		return bad()
	}
	operations := map[string]OnboardingRecord{}
	for i, r := range s.Operations {
		change, e := onboardingNormalize(r.Change)
		if e != nil || onboardingDigest(change) != onboardingDigest(r.Change) || r.Key != change.Key || r.ReviewRevision != i || r.Created.IsZero() || r.State != "saved" && r.State != "stale" && r.State != "unknown" || r.ReservedBytes != 0 && r.ReservedBytes != onboardingFinalizationBytes || r.State != "unknown" && r.ReservedBytes != 0 || r.RequestDigest != onboardingRequestDigest(change, r.Scopes) || r.Review != onboardingPlan(change, r.Scopes, r.ReviewRevision).Review || len(r.Scopes) != len(change.Projects) {
			return bad()
		}
		if _, exists := operations[r.Key]; exists {
			return bad()
		}
		for j, scope := range r.Scopes {
			if !onboardingScopeValid(scope) || scope.Project != change.Projects[j] {
				return bad()
			}
		}
		operations[r.Key] = r
	}
	projects := map[string]bool{}
	for _, p := range s.Preferences {
		r, ok := operations[p.Key]
		if !ok || projects[p.Scope.Project] || p.Updated.IsZero() || !p.Updated.Equal(r.Created) || p.Setup != r.Change.Setup || onboardingDigest(p.Contexts) != onboardingDigest(r.Change.Contexts) {
			return bad()
		}
		scopeMatch := false
		for _, scope := range r.Scopes {
			if onboardingDigest(scope) == onboardingDigest(p.Scope) {
				scopeMatch = true
				break
			}
		}
		if !scopeMatch {
			return bad()
		}
		projects[p.Scope.Project] = true
	}
	for _, r := range s.Operations {
		for _, scope := range r.Scopes {
			if !projects[scope.Project] {
				return bad()
			}
		}
	}
	return nil
}
