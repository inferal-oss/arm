package agentstatus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"getarm.org/internal/rollout"
)

// Onboarding setup is presentation provenance, never participation or authority.
type OnboardingContext struct {
	Adapter       string `json:"adapter"`
	ClientProject string `json:"client_project,omitempty"`
}
type OnboardingChange struct {
	Version  int                 `json:"version"`
	Key      string              `json:"key"`
	Projects []string            `json:"projects"`
	Setup    bool                `json:"setup"`
	Contexts []OnboardingContext `json:"contexts"`
}
type OnboardingScope struct {
	Project        string            `json:"project"`
	PolicyRevision int               `json:"policy_revision"`
	BindingsDigest string            `json:"bindings_digest"`
	Bindings       []rollout.Binding `json:"bindings"`
}
type OnboardingPlan struct {
	Version        int               `json:"version"`
	Change         OnboardingChange  `json:"change"`
	Scopes         []OnboardingScope `json:"scopes"`
	StatusRevision int               `json:"status_revision"`
	Review         string            `json:"review_sha256"`
	Recorded       *OnboardingRecord `json:"recorded,omitempty"`
}
type OnboardingPreference struct {
	Scope    OnboardingScope     `json:"scope"`
	Setup    bool                `json:"setup"`
	Contexts []OnboardingContext `json:"contexts"`
	Key      string              `json:"original_key"`
	Updated  time.Time           `json:"updated_at"`
}
type OnboardingRecord struct {
	Key            string            `json:"original_key"`
	RequestDigest  string            `json:"request_digest"`
	Review         string            `json:"review_sha256"`
	Scopes         []OnboardingScope `json:"scopes"`
	State          string            `json:"state"`
	Created        time.Time         `json:"created_at"`
	Change         OnboardingChange  `json:"change"`
	ReservedBytes  int               `json:"reserved_finalization_bytes,omitempty"`
	ReviewRevision int               `json:"review_status_revision"`
}
type OnboardingState struct {
	Version     int                    `json:"version"`
	Revision    int                    `json:"revision"`
	Preferences []OnboardingPreference `json:"preferences"`
	Operations  []OnboardingRecord     `json:"operations"`
}
type OnboardingPolicyReader func(context.Context) ([]rollout.Project, error)

// BuildOnboardingScopes receives only the trusted reader's actual saved policy
// records. It hashes all effective bindings without discovering agents or Git.
func BuildOnboardingScopes(projects []rollout.Project, selected []string) ([]OnboardingScope, error) {
	if len(selected) == 0 || len(selected) > 128 || len(projects) > 128 {
		return nil, fmt.Errorf("select 1–128 actual saved projects")
	}
	wanted := append([]string(nil), selected...)
	sort.Strings(wanted)
	result := make([]OnboardingScope, 0, len(wanted))
	for index, id := range wanted {
		if id == "" || len(id) > 64 || strings.ContainsAny(id, "\x00\r\n") || index > 0 && wanted[index-1] == id {
			return nil, fmt.Errorf("invalid or duplicate status project selection")
		}
		var actual *rollout.Project
		for i := range projects {
			if projects[i].ID == id {
				if actual != nil {
					return nil, fmt.Errorf("ambiguous saved project")
				}
				actual = &projects[i]
			}
		}
		if actual == nil || actual.Removed || actual.Revision < 1 {
			return nil, fmt.Errorf("selected saved project is absent, removed or unsupported")
		}
		bindings := make([]rollout.Binding, 0, len(actual.Bindings))
		for _, binding := range actual.Bindings {
			if !binding.Removed {
				bindings = append(bindings, binding)
			}
		}
		if len(bindings) == 0 || len(bindings) > 128 {
			return nil, fmt.Errorf("complete project binding snapshot exceeds status support")
		}
		sort.Slice(bindings, func(i, j int) bool {
			a, _ := json.Marshal(bindings[i])
			b, _ := json.Marshal(bindings[j])
			return string(a) < string(b)
		})
		raw, err := json.Marshal(bindings)
		if err != nil || len(raw) > 1<<20 {
			return nil, fmt.Errorf("project binding snapshot exceeds status bound")
		}
		digest := sha256.Sum256(raw)
		scope := OnboardingScope{Project: actual.ID, PolicyRevision: actual.Revision, BindingsDigest: hex.EncodeToString(digest[:]), Bindings: bindings}
		if !onboardingScopeValid(scope) {
			return nil, fmt.Errorf("saved project binding snapshot is unsupported")
		}
		result = append(result, scope)
	}
	return result, nil
}
