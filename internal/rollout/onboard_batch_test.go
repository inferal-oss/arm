package rollout

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/gitmeta"
)

func onboardBatchFixture(t *testing.T) (Store, OnboardBatch) {
	t.Helper()
	base := t.TempDir()
	var changes []Change
	for _, id := range []string{"alpha", "beta"} {
		root := filepath.Join(base, id)
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		changes = append(changes, Change{Action: "onboard", Project: id, Binding: &Binding{Kind: "directory", Root: root}, Mode: "new-sessions", Reason: "Synthetic reviewed project selection"})
	}
	return Store{Home: filepath.Join(base, "state")}, OnboardBatch{Version: 1, Context: OnboardReviewContext{Version: 1, Digest: strings.Repeat("a", 64)}, Changes: changes}
}

func TestOnboardBatchAtomicProjectsApplyTimeAndNoop(t *testing.T) {
	store, request := onboardBatchFixture(t)
	previewAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	applyAt := previewAt.Add(time.Hour)
	store.Now = func() time.Time { return previewAt }
	preview, err := store.PreviewOnboardBatch(t.Context(), request)
	if err != nil || len(preview.Projects) != 2 {
		t.Fatal(err)
	}
	if _, err = os.Stat(store.Home); !os.IsNotExist(err) {
		t.Fatal("preview created state")
	}
	store.Now = func() time.Time { return applyAt }
	saves := 0
	applied, err := store.applyOnboardBatch(t.Context(), request, preview.Review, func(state State) error {
		saves++
		return store.save(state)
	})
	if err != nil || saves != 1 || applied.Unchanged {
		t.Fatal("batch did not use one save", saves, err)
	}
	state, err := store.Read()
	if err != nil || len(state.Projects) != 2 || len(state.History) != 2 {
		t.Fatal("batch did not persist both projects", err)
	}
	for _, project := range state.Projects {
		if !project.NewAfter.Equal(applyAt) || project.Mode != "new-sessions" {
			t.Fatal("cutoff came from preview or different apply timestamps")
		}
	}
	before, err := os.ReadFile(filepath.Join(store.Home, "rollout.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.Now = func() time.Time { return applyAt.Add(time.Hour) }
	preview, err = store.PreviewOnboardBatch(t.Context(), request)
	if err != nil || !preview.Unchanged {
		t.Fatal("configured batch was not unchanged", err)
	}
	applied, err = store.applyOnboardBatch(t.Context(), request, preview.Review, func(State) error {
		t.Fatal("unchanged batch attempted to save")
		return nil
	})
	after, readErr := os.ReadFile(filepath.Join(store.Home, "rollout.json"))
	if err != nil || readErr != nil || !applied.Unchanged || string(before) != string(after) {
		t.Fatal("no-op changed bytes or cutoff", err, readErr)
	}
}

func TestOnboardBatchRejectsSnapshotStateAndBindingDrift(t *testing.T) {
	for _, kind := range []string{"context", "state", "binding"} {
		t.Run(kind, func(t *testing.T) {
			store, request := onboardBatchFixture(t)
			preview, err := store.PreviewOnboardBatch(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "context":
				request.Context.Digest = strings.Repeat("b", 64)
			case "state":
				other := request.Changes[0]
				other.Project = "prior-project"
				plan, err := store.Preview(other)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.Apply(other, plan.Review); err != nil {
					t.Fatal(err)
				}
			case "binding":
				root := request.Changes[0].Binding.Root
				if err = os.Rename(root, root+"-old"); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = store.ApplyOnboardBatch(t.Context(), request, preview.Review); err == nil {
				t.Fatal("stale review was accepted")
			}
			state, err := store.Read()
			if err != nil || kind != "state" && len(state.Projects) != 0 || kind == "state" && (len(state.Projects) != 1 || state.Projects[0].ID != "prior-project") {
				t.Fatal("drift applied an unreviewed batch", err)
			}
			if kind != "state" {
				if _, err = os.Stat(store.Home); !os.IsNotExist(err) {
					t.Fatal("rejected stale preview created state")
				}
			}
		})
	}
}

func TestOnboardBatchDedupOrderingAndConflicts(t *testing.T) {
	store, request := onboardBatchFixture(t)
	duplicate := request.Changes[0]
	request.Changes = []Change{request.Changes[1], duplicate, duplicate}
	preview, err := store.PreviewOnboardBatch(t.Context(), request)
	if err != nil || len(preview.Request.Changes) != 2 || preview.Request.Changes[0].Project != "alpha" {
		t.Fatal("identical cross-client scope was not deterministically deduplicated", err)
	}
	request.Changes = []Change{duplicate, request.Changes[0]}
	ordered, err := store.PreviewOnboardBatch(t.Context(), request)
	if err != nil || ordered.Review != preview.Review {
		t.Fatal("input ordering changed the normalized review", err)
	}
	for _, conflict := range []string{"mode", "owner", "kind", "reason", "action", "identities"} {
		t.Run(conflict, func(t *testing.T) {
			store, request := onboardBatchFixture(t)
			change := request.Changes[0]
			switch conflict {
			case "mode":
				change.Mode = "all"
			case "owner":
				change.Project = "another-project"
			case "kind":
				change.Binding = &Binding{Kind: "unsupported", Root: change.Binding.Root}
			case "reason":
				change.Reason = "Contradictory duplicate request"
			case "action":
				change.Action = "revoke"
			case "identities":
				change.Sessions = []string{"display-uuid-is-not-an-arm-identity"}
			}
			request.Changes = append(request.Changes, change)
			if _, err := store.PreviewOnboardBatch(t.Context(), request); err == nil {
				t.Fatal("contradictory or unrelated selectors were accepted")
			}
			if _, err := os.Stat(store.Home); !os.IsNotExist(err) {
				t.Fatal("conflict created policy state")
			}
		})
	}
}

func TestOnboardBatchCancellationBeforeSaveAndLateSuccess(t *testing.T) {
	store, request := onboardBatchFixture(t)
	preview, err := store.PreviewOnboardBatch(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = store.ApplyOnboardBatch(ctx, request, preview.Review); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancelled batch did not refuse", err)
	}
	if _, err = os.Stat(store.Home); !os.IsNotExist(err) {
		t.Fatal("pre-cancelled batch created state")
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	store.Now = func() time.Time {
		calls++
		if calls == 2 {
			cancel()
		}
		return time.Now().UTC()
	}
	if _, err = store.applyOnboardBatch(ctx, request, preview.Review, func(State) error {
		t.Fatal("cancelled transition attempted save")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled locked transition lost cancellation", err)
	}
	store.Now = nil
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	saves := 0
	if _, err = store.applyOnboardBatch(ctx, request, preview.Review, func(state State) error {
		saves++
		cancel() // Save has started; its actual success must win.
		return store.save(state)
	}); err != nil || saves != 1 {
		t.Fatal("late cancellation replaced actual save success", saves, err)
	}
}

func TestOnboardBatchPossibleSaveFailureRetainsUnknown(t *testing.T) {
	store, request := onboardBatchFixture(t)
	preview, err := store.PreviewOnboardBatch(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	saves := 0
	_, err = store.applyOnboardBatch(t.Context(), request, preview.Review, func(state State) error {
		saves++
		if err := store.save(state); err != nil {
			return err
		}
		return errors.New("synthetic post-save durability uncertainty")
	})
	var unknown *OnboardOutcomeUnknown
	if !errors.As(err, &unknown) || unknown.Review != preview.Review || saves != 1 {
		t.Fatal("possible save was reclassified or retried", saves, err)
	}
	state, readErr := store.Read()
	if readErr != nil || len(state.Projects) != 2 {
		t.Fatal("possible save failure lost the full committed result", readErr)
	}
}

func TestOnboardBatchProjectSnapshotsAndAffectedIdentitiesAreCopied(t *testing.T) {
	f := setup(t)
	f.project("observe")
	_, observed := f.identify(time.Time{})
	request := OnboardBatch{Version: 1, Context: OnboardReviewContext{Version: 1, Digest: strings.Repeat("a", 64)}}
	for n := 0; n < 2; n++ {
		root := t.TempDir()
		request.Changes = append(request.Changes, Change{Action: "onboard", Project: "demo", Binding: &Binding{Kind: "directory", Root: root}, Mode: "observe", Reason: "Synthetic additional binding"})
	}
	preview, err := f.PreviewOnboardBatch(t.Context(), request)
	if err != nil || len(preview.Projects) != 1 || len(preview.Projects[0].Before.Bindings) != 1 || len(preview.Projects[0].After.Bindings) != 3 || len(preview.Affected) != 1 || preview.Affected[0].Identity != observed.Session.Identity {
		t.Fatal("project snapshots or affected identity aggregation are incorrect", err)
	}
	beforeRoot := preview.Projects[0].Before.Bindings[0].Root
	preview.Projects[0].After.Bindings[0].Root = "/mutated-output"
	if preview.Projects[0].Before.Bindings[0].Root != beforeRoot {
		t.Fatal("Before aliased final After binding slices")
	}
	state, err := f.Read()
	if err != nil || len(state.Projects[0].Bindings) != 1 {
		t.Fatal("preview changed persisted state", err)
	}
}

func TestOnboardBatchCapacityRefusesBeforeSave(t *testing.T) {
	for _, capacity := range []string{"projects", "bindings", "history", "request", "context"} {
		t.Run(capacity, func(t *testing.T) {
			store, request := onboardBatchFixture(t)
			state := empty()
			state.Revision = maxEvents
			project := Project{ID: "existing", Mode: "disabled", Revision: 1, Bindings: []Binding{{Kind: "directory", Root: "/saved/root", Identity: "fixture"}}, Clients: []ClientBinding{}}
			switch capacity {
			case "projects":
				for n := 0; n < maxProjects; n++ {
					copy := cloneOnboardProject(project)
					copy.ID = fmt.Sprintf("existing-%d", n)
					state.Projects = append(state.Projects, copy)
				}
			case "bindings":
				project.ID = request.Changes[0].Project
				project.Bindings = nil
				for n := 0; n < 16; n++ {
					project.Bindings = append(project.Bindings, Binding{Kind: "directory", Root: fmt.Sprintf("/saved/root-%d", n), Identity: "fixture"})
				}
				state.Projects = []Project{project}
			case "history":
				state.Projects = []Project{project}
				for n := 0; n < maxEvents; n++ {
					copy := cloneOnboardProject(project)
					state.History = append(state.History, Event{Revision: n + 1, Project: project.ID, Policy: &copy})
				}
			case "request":
				for len(request.Changes) <= MaxOnboardChanges {
					request.Changes = append(request.Changes, request.Changes[0])
				}
			case "context":
				request.Context.Digest = "unversioned-selection-label"
			}
			if err := os.Mkdir(store.Home, 0700); err != nil {
				t.Fatal(err)
			}
			if err := store.save(state); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(store.Home, "rollout.json"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.PreviewOnboardBatch(t.Context(), request); err == nil {
				t.Fatal("capacity or context refusal was skipped")
			}
			after, err := os.ReadFile(filepath.Join(store.Home, "rollout.json"))
			if err != nil || string(before) != string(after) {
				t.Fatal("refusal rewrote existing state", err)
			}
		})
	}
}

func TestOnboardBatchSharedGitBudgetSpansPreflightAndLockedRecheck(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	meta := filepath.Join(base, "metadata")
	for _, path := range []string{bin, meta} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	count := filepath.Join(base, "calls")
	script := "#!/bin/sh\nn=0\nif [ -f \"$ARM_ONBOARD_COUNT\" ]; then IFS= read -r n < \"$ARM_ONBOARD_COUNT\"; fi\nn=$((n+1))\nprintf '%s\\n' \"$n\" > \"$ARM_ONBOARD_COUNT\"\nprintf '%s\\n' \"$6\" \"$ARM_ONBOARD_META\" \"$ARM_ONBOARD_META\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("ARM_ONBOARD_COUNT", count)
	t.Setenv("ARM_ONBOARD_META", meta)
	request := OnboardBatch{Version: 1, Context: OnboardReviewContext{Version: 1, Digest: strings.Repeat("a", 64)}}
	for n := 0; n < 32; n++ {
		root := filepath.Join(base, fmt.Sprintf("checkout-%d", n))
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		request.Changes = append(request.Changes, Change{Action: "onboard", Project: fmt.Sprintf("project-%d", n), Binding: &Binding{Kind: "git", Root: root}, Mode: "observe", Reason: "Synthetic Git budget fixture"})
	}
	store := Store{Home: filepath.Join(base, "state")}
	preview, err := store.PreviewOnboardBatch(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ApplyOnboardBatch(t.Context(), request, preview.Review); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	calls, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || calls != 96 { // 32 preview + one shared-budget apply of 32+32.
		t.Fatal("Git normalization did not retain both bounded passes", calls, err)
	}
	// A genuine precharge leaves only 63 calls for the required 32+32 passes.
	// Resetting the budget for the locked phase would wrongly permit a save.
	budget := gitmeta.NewBudget(t.Context())
	defer budget.Close()
	resolver, err := gitmeta.NewResolver(budget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = resolver.Resolve(request.Changes[0].Binding.Root); err != nil {
		t.Fatal(err)
	}
	operation := &onboardOperation{budget: budget, resolver: resolver}
	refused := Store{Home: filepath.Join(base, "precharged-state")}
	saves := 0
	if _, err = refused.applyOnboardBatchOperation(operation, request, preview.Review, func(state State) error {
		saves++
		return refused.save(state)
	}); !errors.Is(err, gitmeta.ErrBudget) || saves != 0 || budget.Usage().Probes != gitmeta.MaxProbes {
		t.Fatal("precharged operation escaped its shared refusal boundary", saves, err)
	}
	if _, err = os.Stat(filepath.Join(refused.Home, "rollout.json")); !os.IsNotExist(err) {
		t.Fatal("budget refusal persisted policy state", err)
	}
	root := filepath.Join(base, "checkout-extra")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	request.Changes = append(request.Changes, Change{Action: "onboard", Project: "extra-project", Binding: &Binding{Kind: "git", Root: root}, Mode: "observe", Reason: "Synthetic Git budget fixture"})
	if _, err = store.PreviewOnboardBatch(t.Context(), request); err == nil {
		t.Fatal("excess requested Git bindings were accepted")
	}
}
