package agentstatus

import (
	"context"
	"encoding/json"
	"errors"
	"getarm.org/internal/rollout"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnboardingProjectionPreservesSeparateProvenance(t *testing.T) {
	store, projects, change := onboardingFixture(t)
	reads := 0
	reader := func(context.Context) ([]rollout.Project, error) { reads++; return projects, nil }
	if result := store.ProjectSetup(t.Context(), reader); len(result.Projects) != 0 || result.Diagnostic != "" || reads != 0 {
		t.Fatal(result, reads)
	}
	plan, err := store.Plan(t.Context(), reader, change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Apply(t.Context(), reader, change, plan.Review); err != nil {
		t.Fatal(err)
	}
	reads = 0
	result := store.ProjectSetup(t.Context(), reader)
	if len(result.Projects) != 1 || result.Projects[0].Provenance != "matching" || reads != 1 {
		t.Fatal(result, reads)
	}
	raw, _ := json.Marshal(result)
	for _, private := range []string{"contexts", "bindings\"", "original_key", "synthetic-directory"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("projection leaked private scope/context")
		}
	}
	projects[0].Revision++
	if result = store.ProjectSetup(t.Context(), reader); result.Projects[0].Provenance != "stale" {
		t.Fatal(result)
	}
	if result = store.ProjectSetup(t.Context(), func(context.Context) ([]rollout.Project, error) { return nil, errors.New("private failure") }); result.Projects[0].Provenance != "unknown" || strings.Contains(result.Diagnostic, "private failure") {
		t.Fatal(result)
	}
	if err = os.WriteFile(filepath.Join(store.Home, "status-onboarding-v1", "state.json"), []byte(`{"version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	if result = store.ProjectSetup(t.Context(), reader); len(result.Projects) != 0 || result.Diagnostic == "" {
		t.Fatal(result)
	}
}
