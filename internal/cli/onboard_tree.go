package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"
	"getarm.org/internal/agentprojects"
	"getarm.org/internal/agentsessions"
)

type onboardTreeRow struct {
	group       int
	child       int // -1 denotes a project parent
	contextOnly bool
	rank        int
}

type onboardRecipient struct {
	group   string
	session agentsessions.SavedSession
}

// onboardTree owns an immutable display snapshot and independent project-scope
// and activation-recipient selections. Filtering never changes either set.
type onboardTree struct {
	groups     []agentprojects.Group
	projects   map[string]bool
	recipients map[agentsessions.SavedSessionRef]onboardRecipient
	expanded   map[string]bool
}

func newOnboardTree(groups []agentprojects.Group) (*onboardTree, error) {
	if len(groups) > 128 {
		return nil, fmt.Errorf("project group limit reached")
	}
	tree := &onboardTree{projects: map[string]bool{}, recipients: map[agentsessions.SavedSessionRef]onboardRecipient{}, expanded: map[string]bool{}}
	keys := map[string]bool{}
	refs := map[agentsessions.SavedSessionRef]bool{}
	total := 0
	directories := map[string]bool{}
	names, warnings, textBytes := 0, 0, 0
	charge := func(values ...string) bool {
		for _, value := range values {
			textBytes += len(value)
		}
		return textBytes <= 8<<20
	}
	for _, group := range groups {
		if group.Key == "" || len(group.Key) > 4096 || keys[group.Key] || (group.Kind != "git" && group.Kind != "directory") || !onboardTreePath(group.Directory) || len(group.Name) > 1024 || !charge(group.Key, group.Name, group.Directory) {
			return nil, fmt.Errorf("unsupported project grouping")
		}
		keys[group.Key] = true
		if len(group.Sessions) > agentsessions.Limit-total || len(group.Directories) > 512 || len(group.Names) > agentsessions.Limit-names || len(group.Warnings) > 128-warnings {
			return nil, fmt.Errorf("project metadata limit reached")
		}
		if len(group.Directories) == 0 {
			if len(group.Sessions) > 0 {
				return nil, fmt.Errorf("session membership evidence is missing")
			}
			group.Directories = []string{group.Directory}
		}
		members := map[string]bool{}
		for _, directory := range group.Directories {
			directories[directory] = true
			members[directory] = true
			if len(directories) > 512 || !onboardTreePath(directory) || !charge(directory) || group.Kind == "directory" && directory != group.Directory {
				return nil, fmt.Errorf("unsupported project directories")
			}
		}
		if !members[group.Directory] {
			return nil, fmt.Errorf("project root evidence is missing")
		}
		for _, name := range group.Names {
			names++
			if len(name) > 64<<10 || !charge(name) {
				return nil, fmt.Errorf("saved-name limit reached")
			}
		}
		for _, warning := range group.Warnings {
			warnings++
			if len(warning) > 1024 || !charge(warning) {
				return nil, fmt.Errorf("group diagnostic limit reached")
			}
		}
		for _, session := range group.Sessions {
			total++
			if !session.Valid() || refs[session.Ref] || !members[session.Directory] || len(session.Name) > 64<<10 || !charge(session.Name, session.Directory, session.Ref.Adapter, session.Ref.Home, session.Ref.ID) {
				return nil, fmt.Errorf("unsupported or ambiguous saved sessions")
			}
			refs[session.Ref] = true
		}
		// Validate counts, exact membership and aggregate bytes before cloning.
		// This is not proof of filesystem or native Project relationships.
		group = cloneOnboardGroup(group)
		tree.groups = append(tree.groups, group)
		tree.expanded[group.Key] = true
	}
	return tree, nil
}

func onboardTreePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsFunc(path, func(r rune) bool { return r < 32 || r == 127 })
}

func cloneOnboardGroup(group agentprojects.Group) agentprojects.Group {
	group.Directories = slices.Clone(group.Directories)
	group.Names = slices.Clone(group.Names)
	group.Warnings = slices.Clone(group.Warnings)
	group.Sessions = slices.Clone(group.Sessions)
	return group
}

func (t *onboardTree) counts(index int) (selected, total int) {
	group := t.groups[index]
	for _, session := range group.Sessions {
		if _, ok := t.recipients[session.Ref]; ok {
			selected++
		}
	}
	return selected, len(group.Sessions)
}

func (t *onboardTree) mark(row onboardTreeRow) string {
	group := t.groups[row.group]
	if row.child >= 0 {
		if _, ok := t.recipients[group.Sessions[row.child].Ref]; ok {
			return "[x]"
		}
		return "[ ]"
	}
	selected, total := t.counts(row.group)
	if t.projects[group.Key] && selected == total {
		return "[x]"
	}
	if t.projects[group.Key] || selected > 0 {
		return "[-]"
	}
	return "[ ]"
}

func (t *onboardTree) toggle(row onboardTreeRow) {
	group := t.groups[row.group]
	if row.child >= 0 {
		session := group.Sessions[row.child]
		if _, selected := t.recipients[session.Ref]; selected {
			delete(t.recipients, session.Ref)
		} else {
			t.recipients[session.Ref] = onboardRecipient{group.Key, session}
			t.projects[group.Key] = true
		}
		return
	}
	selected, total := t.counts(row.group)
	on := !t.projects[group.Key] || selected != total
	t.projects[group.Key] = on
	for _, session := range group.Sessions {
		if on {
			t.recipients[session.Ref] = onboardRecipient{group.Key, session}
		} else {
			delete(t.recipients, session.Ref)
		}
	}
}

func (t *onboardTree) hasSelection() bool {
	for _, selected := range t.projects {
		if selected {
			return true
		}
	}
	return len(t.recipients) > 0
}

func (t *onboardTree) selectedGroups() []agentprojects.Group {
	var groups []agentprojects.Group
	for _, group := range t.groups {
		if t.projects[group.Key] {
			groups = append(groups, cloneOnboardGroup(group))
		}
	}
	return groups
}

func (t *onboardTree) selectedRecipients() []onboardRecipient {
	var out []onboardRecipient
	for _, group := range t.groups {
		for _, session := range group.Sessions {
			if selected, ok := t.recipients[session.Ref]; ok {
				out = append(out, selected)
			}
		}
	}
	return out
}

func (t *onboardTree) rows(query string) []onboardTreeRow {
	var targets []string
	var all []onboardTreeRow
	parentSlot := make([]int, len(t.groups))
	childSlots := make([][]int, len(t.groups))
	for index, group := range t.groups {
		parentSlot[index] = len(all)
		all = append(all, onboardTreeRow{group: index, child: -1})
		parentTarget := agentprojects.SanitizeTitle(group.Name) + " " + group.Directory
		// Legacy names remain searchable when no native children are available.
		// Real child matches must not turn their contextual parent into a match.
		if len(group.Sessions) == 0 {
			for _, name := range group.Names {
				parentTarget += " " + agentprojects.SanitizeTitle(name)
			}
		}
		targets = append(targets, parentTarget)
		for child, session := range group.Sessions {
			childSlots[index] = append(childSlots[index], len(all))
			all = append(all, onboardTreeRow{group: index, child: child})
			targets = append(targets, agentprojects.SanitizeTitle(session.Name)+" "+session.Directory+" "+session.Ref.Adapter+" "+session.Ref.ID)
		}
	}
	parentMatch := make([]bool, len(t.groups))
	childMatch := map[int]bool{}
	anyChild := make([]bool, len(t.groups))
	ranks := map[int]int{}
	if query != "" {
		for relevance, rank := range list.DefaultFilter(query, targets) {
			ranks[rank.Index] = relevance
			row := all[rank.Index]
			if row.child < 0 {
				parentMatch[row.group] = true
			} else {
				childMatch[rank.Index] = true
				anyChild[row.group] = true
			}
		}
	}
	var rows []onboardTreeRow
	for index, group := range t.groups {
		if query != "" && !parentMatch[index] && !anyChild[index] {
			continue
		}
		parent := onboardTreeRow{group: index, child: -1, contextOnly: query != "" && !parentMatch[index]}
		if query != "" {
			parent.rank = -1
			if relevance, ok := ranks[parentSlot[index]]; ok {
				parent.rank = relevance
			}
		}
		rows = append(rows, parent)
		if !t.expanded[group.Key] && query == "" {
			continue
		}
		for child := range group.Sessions {
			row := onboardTreeRow{group: index, child: child}
			slot := childSlots[index][child]
			if query != "" {
				row.rank = -1
				if relevance, ok := ranks[slot]; ok {
					row.rank = relevance
				}
			}
			if query == "" || parentMatch[index] || childMatch[slot] {
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func firstOnboardTreeRow(rows []onboardTreeRow) int {
	best := -1
	for index, row := range rows {
		if !row.contextOnly && row.rank >= 0 && (best < 0 || row.rank < rows[best].rank) {
			best = index
		}
	}
	return best
}

// choose preserves existing explicit sets; only the empty-selection Enter
// fallback chooses a highlighted parent/all-children or exact child/project.
func (t *onboardTree) choose(rows []onboardTreeRow, index int) bool {
	if t.hasSelection() {
		return true
	}
	if index < 0 || index >= len(rows) {
		return false
	}
	t.toggle(rows[index])
	return t.hasSelection()
}

func (t *onboardTree) title(row onboardTreeRow) string {
	group := t.groups[row.group]
	if row.child >= 0 {
		session := group.Sessions[row.child]
		name := agentprojects.SanitizeTitle(session.Name)
		if name == "" {
			name = "Session " + session.Ref.ID[:8] + "…" + session.Ref.ID[len(session.Ref.ID)-4:]
		}
		return "  " + t.mark(row) + " " + name + " · " + session.Ref.Adapter
	}
	name := agentprojects.SanitizeTitle(group.Name)
	if name == "" {
		name = terminalSafe(filepath.Base(group.Directory))
	}
	kind := "Directory project"
	if group.Kind == "git" {
		kind = "Git project"
	}
	selected, total := t.counts(row.group)
	policy := "policy not selected"
	if t.projects[group.Key] {
		policy = "policy selected"
	}
	return fmt.Sprintf("%s %s · %s · %s · %d/%d chats", t.mark(row), name, kind, policy, selected, total)
}
