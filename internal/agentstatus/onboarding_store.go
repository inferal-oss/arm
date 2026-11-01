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
	"unicode/utf8"
)

const OnboardingMaxBytes = 1 << 20
const OnboardingMaxProjects = 128
const OnboardingMaxOperations = 256
const onboardingFinalizationBytes = 4096

type OnboardingStore struct{ Home string }

// An attempted publication cannot become a generic "cancelled; no write" error.
type OnboardingOutcomeUnknown struct {
	Key string
	Err error
}

func (e *OnboardingOutcomeUnknown) Error() string {
	return "status setup outcome may be unknown; inspect original key " + e.Key
}
func (e *OnboardingOutcomeUnknown) Unwrap() error { return e.Err }
func onboardingEmpty() OnboardingState {
	return OnboardingState{Version: 1, Preferences: []OnboardingPreference{}, Operations: []OnboardingRecord{}}
}
func onboardingDigest(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func onboardingRequestDigest(change OnboardingChange, scopes []OnboardingScope) string {
	return onboardingDigest(struct {
		Change OnboardingChange
		Scopes []OnboardingScope
	}{change, scopes})
}
func onboardingNormalize(change OnboardingChange) (OnboardingChange, error) {
	if change.Version != 1 || change.Key == "" || len(change.Key) > 100 || !utf8.ValidString(change.Key) || strings.ContainsAny(change.Key, "\x00\r\n") || len(change.Projects) == 0 || len(change.Projects) > 128 || len(change.Contexts) > 16 {
		return change, fmt.Errorf("unsupported bounded status setup request")
	}
	for _, r := range change.Key {
		if r < 33 || r > 126 {
			return change, fmt.Errorf("status setup key must be bounded printable ASCII")
		}
	}
	change.Projects = append([]string(nil), change.Projects...)
	sort.Strings(change.Projects)
	for i, id := range change.Projects {
		if !onboardingProjectID(id) || i > 0 && change.Projects[i-1] == id {
			return change, fmt.Errorf("select unique actual saved project IDs")
		}
	}
	change.Contexts = append([]OnboardingContext(nil), change.Contexts...)
	sort.Slice(change.Contexts, func(i, j int) bool {
		if change.Contexts[i].Adapter != change.Contexts[j].Adapter {
			return change.Contexts[i].Adapter < change.Contexts[j].Adapter
		}
		return change.Contexts[i].ClientProject < change.Contexts[j].ClientProject
	})
	for i, c := range change.Contexts {
		if c.Adapter != "codex" && c.Adapter != "claude" && c.Adapter != "generic" || len(c.ClientProject) > 256 || !utf8.ValidString(c.ClientProject) || strings.ContainsAny(c.ClientProject, "\x00\r\n") || i > 0 && change.Contexts[i-1] == c {
			return change, fmt.Errorf("explicit unique adapter/client-project contexts required")
		}
	}
	return change, nil
}
func onboardingRecord(state OnboardingState, key string) (OnboardingRecord, bool) {
	for _, r := range state.Operations {
		if r.Key == key {
			return r, true
		}
	}
	return OnboardingRecord{}, false
}
func onboardingPlan(change OnboardingChange, scopes []OnboardingScope, revision int) OnboardingPlan {
	p := OnboardingPlan{Version: 1, Change: change, Scopes: scopes, StatusRevision: revision}
	p.Review = onboardingDigest(p)
	return p
}

// Plan never creates a directory, lock, backend, message or model job.
func (s OnboardingStore) Plan(ctx context.Context, reader OnboardingPolicyReader, change OnboardingChange) (OnboardingPlan, error) {
	var result OnboardingPlan
	if reader == nil {
		return result, fmt.Errorf("actual saved policy reader required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	change, err := onboardingNormalize(change)
	if err != nil {
		return result, err
	}
	state, err := s.read()
	if err != nil {
		return result, err
	}
	if r, ok := onboardingRecord(state, change.Key); ok {
		if r.RequestDigest != onboardingRequestDigest(change, r.Scopes) {
			return result, fmt.Errorf("original key belongs to a different status request")
		}
		return OnboardingPlan{Version: 1, Change: change, Scopes: r.Scopes, StatusRevision: state.Revision, Review: r.Review, Recorded: &r}, nil
	}
	projects, err := reader(ctx)
	if err != nil {
		return result, fmt.Errorf("saved project policy is unavailable")
	}
	scopes, err := BuildOnboardingScopes(projects, change.Projects)
	if err != nil {
		return result, err
	}
	return onboardingPlan(change, scopes, state.Revision), nil
}
func (s OnboardingStore) Inspect(ctx context.Context, reader OnboardingPolicyReader, key string) (OnboardingRecord, error) {
	state, err := s.read()
	if err != nil {
		return OnboardingRecord{}, err
	}
	r, ok := onboardingRecord(state, key)
	if !ok {
		return r, fmt.Errorf("original status key is not recorded; absence does not settle an in-flight write")
	}
	if reader == nil {
		return r, fmt.Errorf("actual saved policy reader required")
	}
	projects, err := reader(ctx)
	if err != nil {
		r.State = "unknown"
		return r, nil
	}
	current, err := BuildOnboardingScopes(projects, r.Change.Projects)
	if err != nil || onboardingDigest(current) != onboardingDigest(r.Scopes) {
		r.State = "stale"
		return r, nil
	}
	for _, scope := range r.Scopes {
		for _, p := range state.Preferences {
			if p.Scope.Project == scope.Project && p.Key != key {
				r.State = "superseded"
				return r, nil
			}
		}
	}
	return r, nil
}

func (s OnboardingStore) Apply(ctx context.Context, reader OnboardingPolicyReader, change OnboardingChange, expected string) (OnboardingRecord, error) {
	var result OnboardingRecord
	plan, err := s.Plan(ctx, reader, change)
	if err != nil {
		return result, err
	}
	if expected == "" || plan.Review != expected {
		return result, fmt.Errorf("exact status review changed; inspect and review again")
	}
	if plan.Recorded != nil {
		return *plan.Recorded, nil
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	created := false
	began, err := s.update(func(st *OnboardingState) error {
		if old, ok := onboardingRecord(*st, change.Key); ok {
			if old.RequestDigest != onboardingRequestDigest(plan.Change, plan.Scopes) || old.Review != expected {
				return fmt.Errorf("original key belongs to another exact request")
			}
			result = old
			return nil
		}
		if st.Revision != plan.StatusRevision {
			return fmt.Errorf("status setup revision changed")
		}
		if len(st.Operations) >= OnboardingMaxOperations {
			return fmt.Errorf("status operation capacity reached; evidence retained")
		}
		actual, err := reader(ctx)
		if err != nil {
			return fmt.Errorf("saved project policy is unavailable before status publication")
		}
		scopes, err := BuildOnboardingScopes(actual, plan.Change.Projects)
		if err != nil || onboardingDigest(scopes) != onboardingDigest(plan.Scopes) {
			return fmt.Errorf("saved policy or bindings changed; review again")
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		now := time.Now().UTC()
		result = OnboardingRecord{Key: plan.Change.Key, RequestDigest: onboardingRequestDigest(plan.Change, plan.Scopes), Review: expected, Scopes: plan.Scopes, State: "unknown", Created: now, Change: plan.Change, ReservedBytes: onboardingFinalizationBytes, ReviewRevision: plan.StatusRevision}
		for _, scope := range scopes {
			p := OnboardingPreference{Scope: scope, Setup: plan.Change.Setup, Contexts: plan.Change.Contexts, Key: plan.Change.Key, Updated: now}
			found := false
			for i := range st.Preferences {
				if st.Preferences[i].Scope.Project == scope.Project {
					st.Preferences[i] = p
					found = true
					break
				}
			}
			if !found {
				st.Preferences = append(st.Preferences, p)
			}
		}
		if len(st.Preferences) > OnboardingMaxProjects {
			return fmt.Errorf("status project capacity reached; no preferences discarded")
		}
		st.Operations = append(st.Operations, result)
		st.Revision++
		created = true
		return nil
	})
	if err != nil {
		if began {
			return result, &OnboardingOutcomeUnknown{change.Key, err}
		}
		return result, err
	}
	if !created {
		return result, nil
	}
	// Cancellation after publication cannot erase it. Post-check is separately
	// bounded and establishes no atomicity with the independent policy store.
	post, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	actual, checkErr := reader(post)
	outcome := "saved"
	if checkErr != nil {
		outcome = "unknown"
	} else {
		current, e := BuildOnboardingScopes(actual, plan.Change.Projects)
		if e != nil || onboardingDigest(current) != onboardingDigest(plan.Scopes) {
			outcome = "stale"
		}
	}
	_, err = s.update(func(st *OnboardingState) error {
		for i := range st.Operations {
			if st.Operations[i].Key == result.Key {
				st.Operations[i].State = outcome
				if outcome != "unknown" {
					st.Operations[i].ReservedBytes = 0
				}
				result = st.Operations[i]
				return nil
			}
		}
		return fmt.Errorf("original status setup operation is no longer recorded")
	})
	if err != nil || outcome == "unknown" {
		return result, &OnboardingOutcomeUnknown{change.Key, fmt.Errorf("status publication or post-write policy observation is uncertain")}
	}
	return result, nil
}
