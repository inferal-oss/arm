package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/agentprojects"
	"getarm.org/internal/agentsessions"
	"getarm.org/internal/gitmeta"
	"getarm.org/internal/rollout"
)

type pickerFixtureProbe struct {
	infos  map[string]gitmeta.Info
	calls  []string
	action func()
}

func (p *pickerFixtureProbe) Resolve(path string) (gitmeta.Info, error) {
	p.calls = append(p.calls, path)
	if p.action != nil {
		p.action()
	}
	info, ok := p.infos[path]
	if !ok {
		return gitmeta.Info{}, gitmeta.ErrUnsupported
	}
	return info, nil
}
func pickerTestDirectory(t *testing.T, path string) gitmeta.Directory {
	t.Helper()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	directory, err := agentprojects.InspectDirectory(t.Context(), canonical)
	if err != nil {
		t.Fatal(err)
	}
	return directory
}
func pickerNestedFixture(t *testing.T) (pickerSelection, *pickerFixtureProbe, string) {
	t.Helper()
	base := t.TempDir()
	root := pickerTestDirectory(t, filepath.Join(base, "checkout"))
	nested := pickerTestDirectory(t, filepath.Join(root.Path, "nested"))
	common := pickerTestDirectory(t, filepath.Join(base, "common"))
	linkedGit := pickerTestDirectory(t, filepath.Join(base, "linked-git"))
	home := pickerTestDirectory(t, filepath.Join(base, "client"))
	outerInfo := gitmeta.Info{Root: root, Git: common, Common: common}
	innerInfo := gitmeta.Info{Root: nested, Git: linkedGit, Common: common}
	children := []agentsessions.SavedSession{{Ref: agentsessions.SavedSessionRef{Adapter: "codex", Home: home.Path, ID: "11111111-0000-4000-8000-000000000000"}, Directory: root.Path, Name: "Outer chat"}, {Ref: agentsessions.SavedSessionRef{Adapter: "codex", Home: home.Path, ID: "22222222-0000-4000-8000-000000000000"}, Directory: nested.Path, Name: "Unchecked inner chat"}}
	group := agentprojects.Group{Key: "nested-project", Kind: "git", Name: "Example", Directory: root.Path, Directories: []string{root.Path, nested.Path}, Sessions: children, Memberships: []agentprojects.Membership{{Member: root, Checkout: &outerInfo}, {Member: nested, Checkout: &innerInfo}}}
	selection := pickerSelection{Groups: []agentprojects.Group{group}, Settings: []pickerSetting{{Group: group.Key, Project: "example", Kind: "git", Mode: "disabled"}}, Checked: []pickerChecked{{Group: group.Key, Session: children[0]}}}
	return selection, &pickerFixtureProbe{infos: map[string]gitmeta.Info{root.Path: outerInfo, nested.Path: innerInfo}}, filepath.Join(base, "state")
}

func TestProjectPickerExplicitNestedRootsAndUncheckedCoverage(t *testing.T) {
	selection, probe, _ := pickerNestedFixture(t)
	request, err := pickerRequest(t.Context(), selection, "Nested checkout fixture", func(*gitmeta.Budget) (pickerGitProbe, error) { return probe, nil })
	if err != nil || len(request.Changes) != 2 || len(probe.calls) != 2 {
		t.Fatal(request, err, probe.calls)
	}
	roots := []string{}
	for _, change := range request.Changes {
		roots = append(roots, change.Binding.Root)
	}
	if !slices.Contains(roots, selection.Groups[0].Memberships[0].Checkout.Root.Path) || !slices.Contains(roots, selection.Groups[0].Memberships[1].Checkout.Root.Path) {
		t.Fatal("nested checkout collapsed by path containment", roots)
	}
	unchecked := clonePickerSelection(selection)
	unchecked.Checked = nil
	other, err := pickerRequest(t.Context(), unchecked, "Nested checkout fixture", func(*gitmeta.Budget) (pickerGitProbe, error) { return probe, nil })
	if err != nil || len(other.Changes) != 2 || other.Context == request.Context {
		t.Fatal("unchecked chats changed policy coverage or were not sealed", other, err)
	}
}

func TestProjectPickerRevalidatesUncheckedNestedMembershipBeforeApply(t *testing.T) {
	selection, probe, home := pickerNestedFixture(t)
	// Initially both exact member cwds belong to the outer checkout. Only the
	// outer chat is checked, and the old outer root/identity remains unchanged.
	outer := *selection.Groups[0].Memberships[0].Checkout
	selection.Groups[0].Memberships[1].Checkout = &outer
	probe.infos[selection.Groups[0].Memberships[1].Member.Path] = outer
	selection.Settings[0].Kind = "directory" // no actual Git process in Store
	factory := func(*gitmeta.Budget) (pickerGitProbe, error) { return probe, nil }
	plan, err := preparePickerPolicy(t.Context(), home, "Membership regression", selection, factory)
	if err != nil {
		t.Fatal(err)
	}
	member := selection.Groups[0].Memberships[1].Member
	changedGit := pickerTestDirectory(t, filepath.Join(t.TempDir(), "new-linked-git"))
	probe.infos[member.Path] = gitmeta.Info{Root: member, Git: changedGit, Common: outer.Common}
	_, err = applyPickerPolicy(t.Context(), home, "Membership regression", selection, plan, factory)
	if err == nil || !strings.Contains(err.Error(), "membership changed") {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(err) {
		t.Fatal("unchecked member drift reached policy save", err)
	}
	if len(selection.Checked) != 1 || selection.Groups[0].Memberships[1].Checkout.Root != outer.Root {
		t.Fatal("selection was re-sealed or reassociated")
	}
}

func TestProjectPickerChangedMemberAndContextCannotReuseReview(t *testing.T) {
	for _, mode := range []string{"directory-identity", "checked", "project", "policy", "missing-proof"} {
		t.Run(mode, func(t *testing.T) {
			selection, probe, home := pickerNestedFixture(t)
			selection.Settings[0].Kind = "directory"
			factory := func(*gitmeta.Budget) (pickerGitProbe, error) { return probe, nil }
			plan, err := preparePickerPolicy(t.Context(), home, "Sealed selection fixture", selection, factory)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "directory-identity":
				path := selection.Groups[0].Memberships[1].Member.Path
				if err = os.Rename(path, path+"-retained"); err != nil {
					t.Fatal(err)
				}
				if err = os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "checked":
				selection.Checked = nil
			case "project":
				selection.Settings[0].Project = "other"
			case "policy":
				selection.Settings[0].Mode = "observe"
			case "missing-proof":
				selection.Groups[0].Memberships = nil
			}
			if _, err = applyPickerPolicy(t.Context(), home, "Sealed selection fixture", selection, plan, factory); err == nil {
				t.Fatal("stale review accepted", mode)
			}
			if _, err = os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(err) {
				t.Fatal("stale selection wrote policy", err)
			}
		})
	}
}

func TestProjectPickerEntryDeadlineCancellationAndStageSeparation(t *testing.T) {
	selection, probe, home := pickerNestedFixture(t)
	selection.Settings[0].Kind = "directory"
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(8*time.Second))
	defer cancel()
	deadline, _ := ctx.Deadline()
	observed := false
	factory := func(budget *gitmeta.Budget) (pickerGitProbe, error) {
		got, ok := budget.Context().Deadline()
		if !ok || got.After(deadline) {
			t.Fatal("membership restarted caller deadline", got, deadline)
		}
		observed = true
		return probe, nil
	}
	if _, err := preparePickerPolicy(ctx, home, "Deadline fixture", selection, factory); err != nil || !observed {
		t.Fatal(err, observed)
	}
	ctx, cancelNow := context.WithCancel(ctx)
	defer cancelNow()
	probe.action = cancelNow
	if _, err := preparePickerPolicy(ctx, home, "Canceled member fixture", selection, factory); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "rollout.json")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestProjectPickerProjectionRejectsUnknownAndConflictingSelections(t *testing.T) {
	for _, mode := range []string{"unknown-chat", "duplicate-chat", "different-group", "invalid-id", "root-conflict"} {
		t.Run(mode, func(t *testing.T) {
			selection, probe, _ := pickerNestedFixture(t)
			switch mode {
			case "unknown-chat":
				selection.Checked[0] = pickerChecked{}
			case "duplicate-chat":
				selection.Checked = append(selection.Checked, selection.Checked[0])
			case "different-group":
				selection.Checked[0].Group = "other"
			case "invalid-id":
				selection.Settings[0].Project = "not a project"
			case "root-conflict":
				copyGroup := cloneOnboardGroup(selection.Groups[0])
				copyGroup.Key = "other"
				copyGroup.Sessions = nil
				selection.Groups = append(selection.Groups, copyGroup)
				selection.Settings = append(selection.Settings, pickerSetting{Group: "other", Project: "other", Kind: "git", Mode: "disabled"})
			}
			if _, err := pickerRequest(t.Context(), selection, "Invalid projection fixture", func(*gitmeta.Budget) (pickerGitProbe, error) { return probe, nil }); err == nil {
				t.Fatal("unsupported selection accepted", mode)
			}
		})
	}
}

func TestProjectPickerReadinessPreservesDisabledAndNoopSemantics(t *testing.T) {
	for _, plan := range []struct {
		plan rollout.OnboardBatchPlan
		want bool
	}{{rollout.OnboardBatchPlan{Projects: []rollout.OnboardProjectPlan{{After: rollout.Project{Mode: "disabled"}}}}, false}, {rollout.OnboardBatchPlan{Projects: []rollout.OnboardProjectPlan{{After: rollout.Project{Mode: "all"}, Unchanged: true}, {After: rollout.Project{Mode: "disabled"}}}}, false}, {rollout.OnboardBatchPlan{Projects: []rollout.OnboardProjectPlan{{After: rollout.Project{Mode: "new-sessions"}}}}, true}} {
		if pickerNeedsInstructions(plan.plan) != plan.want {
			t.Fatal(plan)
		}
	}
}

func TestProjectPickerMembershipProbeStageHasIndependentCeiling(t *testing.T) {
	base := t.TempDir()
	root := pickerTestDirectory(t, filepath.Join(base, "root"))
	common := pickerTestDirectory(t, filepath.Join(base, "common"))
	info := gitmeta.Info{Root: root, Git: common, Common: common}
	group := agentprojects.Group{Key: "bounded-git", Kind: "git", Name: "Bounded", Directory: root.Path, Directories: []string{root.Path}}
	probe := &pickerFixtureProbe{infos: map[string]gitmeta.Info{}}
	for i := 0; i < 65; i++ {
		member := pickerTestDirectory(t, filepath.Join(root.Path, fmt.Sprintf("member-%02d", i)))
		copyInfo := info
		group.Directories = append(group.Directories, member.Path)
		group.Memberships = append(group.Memberships, agentprojects.Membership{Member: member, Checkout: &copyInfo})
		probe.infos[member.Path] = info
	}
	selection := pickerSelection{Groups: []agentprojects.Group{group}, Settings: []pickerSetting{{Group: group.Key, Project: "bounded", Kind: "directory", Mode: "disabled"}}}
	if _, err := pickerRequest(t.Context(), selection, "Stage ceiling fixture", func(*gitmeta.Budget) (pickerGitProbe, error) { return probe, nil }); !errors.Is(err, gitmeta.ErrBudget) || len(probe.calls) != 64 {
		t.Fatal(err, len(probe.calls))
	}
}
