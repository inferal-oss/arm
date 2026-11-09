package agentstatus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/rollout"
	"golang.org/x/sys/unix"
)

func onboardingFixture(t *testing.T) (OnboardingStore, []rollout.Project, OnboardingChange) {
	t.Helper()
	return OnboardingStore{Home: t.TempDir()}, []rollout.Project{{ID: "sample", Revision: 3, Mode: "observe", Bindings: []rollout.Binding{{Kind: "directory", Root: "/synthetic/project", Identity: "synthetic-directory"}}}}, OnboardingChange{Version: 1, Key: "setup-one", Projects: []string{"sample"}, Setup: true, Contexts: []OnboardingContext{{Adapter: "codex"}}}
}
func TestOnboardingPreviewAndOriginalKeyReplay(t *testing.T) {
	store, projects, change := onboardingFixture(t)
	reader := func(context.Context) ([]rollout.Project, error) { return projects, nil }
	plan, err := store.Plan(t.Context(), reader, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(filepath.Join(store.Home, "status-onboarding-v1")); !os.IsNotExist(err) {
		t.Fatal("preview created state")
	}
	record, err := store.Apply(t.Context(), reader, change, plan.Review)
	if err != nil || record.State != "saved" {
		t.Fatal("status preference not saved", err)
	}
	state, err := store.read()
	if err != nil || len(state.Preferences) != 1 || len(state.Operations) != 1 || state.Preferences[0].Scope.PolicyRevision != 3 {
		t.Fatal("saved scope changed", err)
	}
	projects[0].Revision = 4
	replay, err := store.Apply(t.Context(), reader, change, plan.Review)
	if err != nil || replay.Key != record.Key {
		t.Fatal("original key replay retargeted", err)
	}
	observed, err := store.Inspect(t.Context(), reader, change.Key)
	if err != nil || observed.State != "stale" {
		t.Fatal("policy drift hidden", err)
	}
	changed := change
	changed.Setup = false
	if _, err = store.Apply(t.Context(), reader, changed, plan.Review); err == nil {
		t.Fatal("key reused for changed setup")
	}
}

func TestOnboardingStatusSetupPreservesRepositoryWorktreeBindings(t *testing.T) {
	store, projects, change := onboardingFixture(t)
	bindings := []rollout.Binding{
		{Kind: rollout.WorktreesKind, Root: "/synthetic/bare.git", Identity: "bare", GitDirectory: "/synthetic/bare.git", GitIdentity: "bare"},
		{Kind: rollout.WorktreesKind, Root: "/synthetic/primary", Identity: "primary", GitDirectory: "/synthetic/primary/.git", GitIdentity: "common"},
	}
	projects[0].Bindings = slices.Clone(bindings)
	reader := func(context.Context) ([]rollout.Project, error) { return projects, nil }
	plan, err := store.Plan(t.Context(), reader, change)
	if err != nil || len(plan.Scopes) != 1 || !slices.Equal(plan.Scopes[0].Bindings, bindings) {
		t.Fatal("preview did not preserve repository bindings", plan, err)
	}
	if record, err := store.Apply(t.Context(), reader, change, plan.Review); err != nil || record.State != "saved" {
		t.Fatal("setup could not save the reviewed bindings", record, err)
	}
	record, err := store.Inspect(t.Context(), reader, change.Key)
	if err != nil || record.State != "saved" || len(record.Scopes) != 1 || !slices.Equal(record.Scopes[0].Bindings, bindings) {
		t.Fatal("saved repository bindings could not be read back", record, err)
	}
	projects[0].Bindings[0].GitIdentity = ""
	if _, err := BuildOnboardingScopes(projects, change.Projects); err == nil {
		t.Fatal("repository binding without a Git identity was accepted")
	}
}

func TestOnboardingPolicyRaceAndUnknownPublication(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale", true: "unknown"}[unknown], func(t *testing.T) {
			store, projects, change := onboardingFixture(t)
			reader := func(context.Context) ([]rollout.Project, error) { return projects, nil }
			plan, err := store.Plan(t.Context(), reader, change)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			changing := func(context.Context) ([]rollout.Project, error) {
				calls++
				if calls >= 3 {
					if unknown {
						return nil, errors.New("synthetic unavailable policy")
					}
					projects[0].Revision++
				}
				return projects, nil
			}
			record, err := store.Apply(t.Context(), changing, change, plan.Review)
			if unknown {
				var typed *OnboardingOutcomeUnknown
				if !errors.As(err, &typed) || typed.Key != change.Key {
					t.Fatal("lost original unknown key", err)
				}
			} else if err != nil || record.State != "stale" {
				t.Fatal("post-write policy race hidden", err)
			}
			state, e := store.read()
			if e != nil || len(state.Preferences) != 1 || !state.Preferences[0].Setup || state.Preferences[0].Scope.PolicyRevision != 3 {
				t.Fatal("status outcome rolled back or retargeted policy", e)
			}
		})
	}
}
func TestOnboardingCancellationAndNonregularEvidence(t *testing.T) {
	store, projects, change := onboardingFixture(t)
	reader := func(context.Context) ([]rollout.Project, error) { return projects, nil }
	plan, err := store.Plan(t.Context(), reader, change)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = store.Apply(ctx, reader, change, plan.Review); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel before write ignored", err)
	}
	if _, err = os.Lstat(filepath.Join(store.Home, "status-onboarding-v1")); !os.IsNotExist(err) {
		t.Fatal("cancelled apply created state")
	}
	dir := filepath.Join(store.Home, "status-onboarding-v1")
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = unix.Mkfifo(filepath.Join(dir, "state.json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Plan(t.Context(), reader, change); err == nil {
		t.Fatal("nonregular status state accepted")
	}
}
func TestOnboardingRejectsIncompleteAndDuplicateScopes(t *testing.T) {
	_, projects, _ := onboardingFixture(t)
	if _, err := BuildOnboardingScopes(projects, []string{"sample", "sample"}); err == nil {
		t.Fatal("duplicate project accepted")
	}
	projects[0].Removed = true
	if _, err := BuildOnboardingScopes(projects, []string{"sample"}); err == nil {
		t.Fatal("removed project accepted")
	}
}

func TestOnboardingDirectoryReplacementPreservesOriginalPublication(t *testing.T) {
	store, projects, change := onboardingFixture(t)
	reader := func(context.Context) ([]rollout.Project, error) { return projects, nil }
	plan, err := store.Plan(t.Context(), reader, change)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.Home, "status-onboarding-v1")
	old := dir + "-original"
	calls := 0
	replacing := func(context.Context) ([]rollout.Project, error) {
		calls++
		if calls == 2 {
			if err := os.Rename(dir, old); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("replacement remains untouched"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		return projects, nil
	}
	_, err = store.Apply(t.Context(), replacing, change, plan.Review)
	var unknown *OnboardingOutcomeUnknown
	if !errors.As(err, &unknown) || unknown.Key != change.Key {
		t.Fatal("replacement lost original outcome/key", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
		t.Fatal("replacement directory was touched", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "sentinel"))
	if err != nil || string(raw) != "replacement remains untouched" {
		t.Fatal("replacement bytes changed", err)
	}
	files, err := filepath.Glob(filepath.Join(old, ".state-*"))
	if err != nil || len(files) != 1 {
		t.Fatal("original publication evidence missing", err)
	}
	raw, err = os.ReadFile(files[0])
	if err != nil || !bytes.Contains(raw, []byte(change.Key)) {
		t.Fatal("original key evidence was discarded", err)
	}
}

func TestOnboardingCancellationAfterPublicationRetainsOutcome(t *testing.T) {
	store, projects, change := onboardingFixture(t)
	reader := func(context.Context) ([]rollout.Project, error) { return projects, nil }
	plan, err := store.Plan(t.Context(), reader, change)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	cancelling := func(post context.Context) ([]rollout.Project, error) {
		calls++
		if calls == 3 {
			cancel()
			if post.Err() != nil {
				t.Fatal("original cancellation replaced owned post-publication context")
			}
		}
		return projects, nil
	}
	record, err := store.Apply(ctx, cancelling, change, plan.Review)
	if err != nil || record.State != "saved" || ctx.Err() == nil {
		t.Fatal("actual saved outcome became generic interruption", err)
	}
	observed, err := store.Inspect(t.Context(), reader, change.Key)
	if err != nil || observed.State != "saved" {
		t.Fatal("saved outcome lost", err)
	}
}

func TestOnboardingRefusalsPreserveSnapshot(t *testing.T) {
	for _, kind := range []string{"unknown_format", "orphan", "capacity"} {
		t.Run(kind, func(t *testing.T) {
			store, projects, change := onboardingFixture(t)
			reader := func(context.Context) ([]rollout.Project, error) { return projects, nil }
			dir := filepath.Join(store.Home, "status-onboarding-v1")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if kind != "capacity" {
				name := "state.json"
				data := []byte(`{"version":999,"synthetic":"preserve"}`)
				if kind == "orphan" {
					name = ".state-interrupted"
				}
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := store.Plan(t.Context(), reader, change); err == nil {
					t.Fatal("unsupported evidence accepted")
				}
				if _, err := store.Apply(t.Context(), reader, change, "unreviewed"); err == nil {
					t.Fatal("unsupported evidence changed")
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(after, data) {
					t.Fatal("unsupported bytes changed", err)
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 1 {
					t.Fatal("refusal created an operation or lock")
				}
				return
			}
			scopes, err := BuildOnboardingScopes(projects, change.Projects)
			if err != nil {
				t.Fatal(err)
			}
			full := onboardingEmpty()
			now := time.Now().UTC()
			for i := 0; i < OnboardingMaxOperations; i++ {
				c := change
				c.Key = fmt.Sprintf("retained-%d", i)
				p := onboardingPlan(c, scopes, i)
				r := OnboardingRecord{Key: c.Key, RequestDigest: onboardingRequestDigest(c, scopes), Review: p.Review, Scopes: scopes, State: "saved", Created: now, Change: c, ReviewRevision: i}
				full.Operations = append(full.Operations, r)
			}
			full.Revision = len(full.Operations)
			full.Preferences = []OnboardingPreference{{Scope: scopes[0], Setup: true, Contexts: change.Contexts, Key: full.Operations[len(full.Operations)-1].Key, Updated: now}}
			if _, err = store.update(func(st *OnboardingState) error { *st = full; return nil }); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "state.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := store.Plan(t.Context(), reader, change)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.Apply(t.Context(), reader, change, plan.Review); err == nil {
				t.Fatal("operation capacity bypassed")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("capacity refusal changed snapshot")
			}
			projects[0].Bindings[0].Root = "/" + strings.Repeat("x", 4090)
			large, err := BuildOnboardingScopes(projects, change.Projects)
			if err != nil {
				t.Fatal(err)
			}
			for i := range full.Operations {
				r := &full.Operations[i]
				r.Scopes = large
				r.RequestDigest = onboardingRequestDigest(r.Change, large)
				r.Review = onboardingPlan(r.Change, large, i).Review
			}
			full.Preferences[0].Scope = large[0]
			if began, err := store.update(func(st *OnboardingState) error { *st = full; return nil }); err == nil || began {
				t.Fatal("document capacity started publication", err)
			}
			after, _ = os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("document refusal changed prior snapshot")
			}
		})
	}
}
