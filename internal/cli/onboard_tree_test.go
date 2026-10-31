package cli

import (
	"fmt"
	"strings"
	"testing"

	"getarm.org/internal/agentprojects"
	"getarm.org/internal/agentsessions"
)

func onboardTreeFixture() []agentprojects.Group {
	ref := func(adapter, home string, n int) agentsessions.SavedSessionRef {
		return agentsessions.SavedSessionRef{Adapter: adapter, Home: home, ID: fmt.Sprintf("00000000-0000-0000-0000-%012x", n)}
	}
	return []agentprojects.Group{
		{Key: "alpha", Kind: "git", Name: "Alpha repository", Directory: "/work/alpha", Directories: []string{"/work/alpha", "/worktrees/alpha"}, Names: []string{"Needleλ"}, Sessions: []agentsessions.SavedSession{
			{Ref: ref("codex", "/clients/codex", 1), Directory: "/work/alpha", Name: "Duplicate title"},
			{Ref: ref("codex", "/clients/codex", 2), Directory: "/worktrees/alpha", Name: "Needleλ"},
		}},
		{Key: "beta", Kind: "directory", Name: "Beta directory", Directory: "/work/beta", Directories: []string{"/work/beta"}, Sessions: []agentsessions.SavedSession{
			{Ref: ref("claude", "/clients/claude", 1), Directory: "/work/beta", Name: "Duplicate title"},
		}},
	}
}

func mustOnboardTree(t *testing.T) *onboardTree {
	t.Helper()
	tree, err := newOnboardTree(onboardTreeFixture())
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestOnboardTreeParentToggleCoversHiddenSnapshotChildren(t *testing.T) {
	tree := mustOnboardTree(t)
	tree.expanded["alpha"] = false
	rows := tree.rows("Needleλ")
	if len(rows) != 2 || !rows[0].contextOnly || rows[1].child != 1 {
		t.Fatal("child search lost its parent context", rows)
	}
	tree.toggle(rows[0])
	if selected, total := tree.counts(0); selected != 2 || total != 2 || tree.mark(rows[0]) != "[x]" {
		t.Fatal("parent did not select hidden children", selected, total)
	}
	if len(tree.selectedGroups()) != 1 || len(tree.selectedRecipients()) != 2 {
		t.Fatal("selection widened to another project")
	}
	tree.toggle(onboardTreeRow{group: 0, child: 0})
	if selected, total := tree.counts(0); selected != 1 || total != 2 || tree.mark(rows[0]) != "[-]" {
		t.Fatal("child override lost mixed state", selected, total)
	}
	for _, query := range []string{"", "does-not-match", "Beta"} {
		tree.rows(query)
		if len(tree.selectedRecipients()) != 1 || tree.selectedRecipients()[0].session.Ref != tree.groups[0].Sessions[1].Ref {
			t.Fatal("filter changed the selected snapshot")
		}
	}
	tree.toggle(rows[0])
	if len(tree.selectedRecipients()) != 2 || tree.mark(rows[0]) != "[x]" {
		t.Fatal("mixed parent did not select all children")
	}
	tree.toggle(rows[0])
	if tree.hasSelection() || tree.mark(rows[0]) != "[ ]" {
		t.Fatal("checked parent did not clear project and children")
	}
}

func TestOnboardTreeUncheckedChatsPreserveProjectOnlySelection(t *testing.T) {
	tree := mustOnboardTree(t)
	parent := onboardTreeRow{group: 0, child: -1}
	tree.toggle(parent)
	for child := range tree.groups[0].Sessions {
		tree.toggle(onboardTreeRow{group: 0, child: child})
	}
	if len(tree.selectedRecipients()) != 0 || len(tree.selectedGroups()) != 1 || tree.mark(parent) != "[-]" || !strings.Contains(tree.title(parent), "policy selected · 0/2 chats") {
		t.Fatal("unchecking chats removed their project policy")
	}
	rows := tree.rows("Beta directory")
	if !tree.choose(rows, firstOnboardTreeRow(rows)) || len(tree.selectedGroups()) != 1 || tree.selectedGroups()[0].Key != "alpha" {
		t.Fatal("Enter added the highlighted project to an explicit project-only selection")
	}
	rows = tree.rows("no-match-zzzzz")
	if len(rows) != 0 || !tree.choose(rows, -1) || len(tree.selectedGroups()) != 1 {
		t.Fatal("a no-match filter lost an explicit project-only selection")
	}
}

func TestOnboardTreeEnterUsesLatestMatchedChildAndExplicitSets(t *testing.T) {
	tree := mustOnboardTree(t)
	tree.rows("Duplicate title") // An older query is intentionally not selected.
	rows := tree.rows("Needleλ")
	index := firstOnboardTreeRow(rows)
	if index < 0 || rows[index].child != 1 || !tree.choose(rows, index) {
		t.Fatal("Enter selected a contextual parent instead of its matching child", rows, index)
	}
	selected := tree.selectedRecipients()
	if len(selected) != 1 || selected[0].session.Ref != tree.groups[0].Sessions[1].Ref || len(tree.selectedGroups()) != 1 {
		t.Fatal("child fallback selected other children")
	}
	rows = tree.rows("Beta directory")
	if !tree.choose(rows, firstOnboardTreeRow(rows)) || len(tree.selectedGroups()) != 1 || len(tree.selectedRecipients()) != 1 {
		t.Fatal("Enter changed an explicit selection")
	}
	tree = mustOnboardTree(t)
	rows = tree.rows("Alpha repository")
	if !tree.choose(rows, firstOnboardTreeRow(rows)) || len(tree.selectedRecipients()) != 2 {
		t.Fatal("parent fallback did not choose its whole snapshot")
	}
	tree = mustOnboardTree(t)
	rows = tree.rows("no-such-match-zzzz")
	if len(rows) != 0 || tree.choose(rows, firstOnboardTreeRow(rows)) || tree.hasSelection() {
		t.Fatal("no-match Enter selected a project")
	}
}

func TestOnboardTreeKeysKeepClientHomeAndNativeContext(t *testing.T) {
	groups := onboardTreeFixture()
	groups[1].Sessions[0].Ref.Adapter = "codex"
	groups[1].Sessions[0].Ref.Home = "/clients/another-codex"
	tree, err := newOnboardTree(groups)
	if err != nil {
		t.Fatal(err)
	}
	tree.toggle(onboardTreeRow{group: 0, child: 0})
	tree.toggle(onboardTreeRow{group: 1, child: 0})
	if len(tree.selectedRecipients()) != 2 {
		t.Fatal("equal native UUIDs or names merged across client homes")
	}
	groups[1].Sessions[0].Ref = groups[0].Sessions[0].Ref
	if _, err = newOnboardTree(groups); err == nil {
		t.Fatal("ambiguous duplicate reference was accepted")
	}
	groups = onboardTreeFixture()
	groups[0].Sessions[0].Ref.ID = "display-name-is-not-an-id"
	if _, err = newOnboardTree(groups); err == nil {
		t.Fatal("a display name became a native reference")
	}
}

func TestOnboardTreeSnapshotCopiesAndFallbackLabels(t *testing.T) {
	groups := onboardTreeFixture()
	groups[0].Sessions[0].Name = "\x1b[2JBad\nname\u202e"
	groups[1].Sessions[0].Name = ""
	tree, err := newOnboardTree(groups)
	if err != nil {
		t.Fatal(err)
	}
	tree.toggle(onboardTreeRow{group: 0, child: -1})
	groups[0].Sessions[0].Ref.Home = "/rebound-home"
	groups[0].Directories[0] = "/rebound-root"
	selected := tree.selectedGroups()
	selected[0].Directories[0] = "/mutated-return"
	selected[0].Sessions[0].Ref.ID = "mutated-return"
	if tree.groups[0].Directories[0] != "/work/alpha" || !tree.selectedRecipients()[0].session.Ref.Valid() {
		t.Fatal("caller mutation changed the selection snapshot")
	}
	if title := tree.title(onboardTreeRow{group: 0, child: 0}); strings.ContainsAny(title, "\x1b\n\u202e") || !strings.HasPrefix(title, "  [x]") {
		t.Fatal("unsafe or unindented child label", title)
	}
	if title := tree.title(onboardTreeRow{group: 1, child: 0}); !strings.Contains(title, "Session ") || !strings.Contains(title, "claude") {
		t.Fatal("unnamed saved metadata had no truthful label", title)
	}
}

func TestOnboardTreeBoundsAndEmptyProject(t *testing.T) {
	groups := []agentprojects.Group{{Key: "empty", Kind: "directory", Directory: "/work/empty"}}
	tree, err := newOnboardTree(groups)
	if err != nil {
		t.Fatal(err)
	}
	tree.toggle(onboardTreeRow{group: 0, child: -1})
	if !tree.hasSelection() || len(tree.selectedRecipients()) != 0 || tree.mark(onboardTreeRow{group: 0, child: -1}) != "[x]" {
		t.Fatal("project without discoverable sessions was not selectable")
	}
	for n := 0; n <= agentsessions.Limit; n++ {
		groups[0].Sessions = append(groups[0].Sessions, agentsessions.SavedSession{Ref: agentsessions.SavedSessionRef{Adapter: "codex", Home: "/clients/codex", ID: fmt.Sprintf("00000000-0000-0000-0000-%012x", n)}, Directory: "/work/empty"})
	}
	if _, err = newOnboardTree(groups); err == nil {
		t.Fatal("session snapshot limit exceeded")
	}
	groups = nil
	for n := 0; n < 129; n++ {
		groups = append(groups, agentprojects.Group{Key: fmt.Sprint(n), Kind: "directory", Directory: fmt.Sprintf("/work/%d", n)})
	}
	if _, err = newOnboardTree(groups); err == nil {
		t.Fatal("project snapshot limit exceeded")
	}
}

func TestOnboardTreeRejectsUnprovenDirectoryMembership(t *testing.T) {
	for _, directory := range []string{"/unrelated/project", "/work/beta-sibling"} {
		groups := onboardTreeFixture()
		groups[1].Sessions[0].Directory = directory
		if _, err := newOnboardTree(groups); err == nil {
			t.Fatal("outside cwd accepted without explicit membership evidence")
		}
		groups[1].Directories = append(groups[1].Directories, directory)
		if _, err := newOnboardTree(groups); err == nil {
			t.Fatal("directory fallback claimed an unrelated explicit path")
		}
	}
	groups := onboardTreeFixture()
	groups[0].Directories = nil
	if _, err := newOnboardTree(groups); err == nil {
		t.Fatal("Git grouping with children accepted missing directory evidence")
	}
	groups = onboardTreeFixture()
	groups[0].Directories = []string{"/work/alpha"}
	if _, err := newOnboardTree(groups); err == nil {
		t.Fatal("external worktree child accepted without exact cwd evidence")
	}
}

func TestOnboardTreeRejectsOversizedTextBeforeCopying(t *testing.T) {
	groups := onboardTreeFixture()
	groups[0].Name = strings.Repeat("n", 1025)
	if _, err := newOnboardTree(groups); err == nil {
		t.Fatal("oversized group label accepted")
	}
	groups = onboardTreeFixture()
	groups[0].Warnings = []string{strings.Repeat("w", 1025)}
	if _, err := newOnboardTree(groups); err == nil {
		t.Fatal("oversized diagnostic accepted")
	}
	groups = onboardTreeFixture()
	groups[0].Names = make([]string, agentsessions.Limit+1)
	if _, err := newOnboardTree(groups); err == nil {
		t.Fatal("oversized name roster accepted")
	}
	groups = onboardTreeFixture()
	groups[0].Sessions[0].Name = strings.Repeat("s", (64<<10)+1)
	if _, err := newOnboardTree(groups); err == nil {
		t.Fatal("oversized raw session label accepted")
	}
	groups = onboardTreeFixture()
	label := strings.Repeat("s", 64<<10)
	groups[0].Names = make([]string, 129)
	for index := range groups[0].Names {
		groups[0].Names[index] = label
	}
	if _, err := newOnboardTree(groups); err == nil {
		t.Fatal("aggregate metadata byte bound was bypassed")
	}
}
