package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"getarm.org/internal/agentprojects"
	"getarm.org/internal/gitmeta"
	"getarm.org/internal/install"
	"getarm.org/internal/rollout"
	"github.com/charmbracelet/x/ansi"
)

type projectPickerStage int

const (
	pickerLoading projectPickerStage = iota
	pickerProjects
	pickerManual
	pickerResolving
	pickerSettings
	pickerID
	pickerPreparing
	pickerReview
	pickerApplying
	pickerOffer
	pickerDiagnostics
	pickerDone
	pickerStatusOffer
	pickerStatusPreparing
	pickerStatusReview
	pickerStatusApplying
)

type pickerLoadedMsg struct {
	generation int
	catalog    agentprojects.Catalog
	hierarchy  agentprojects.Hierarchy
	err        error
	readiness  *install.InstructionReadiness
}
type pickerManualMsg struct {
	generation int
	group      agentprojects.Group
	err        error
}
type pickerPreviewMsg struct {
	generation int
	selection  pickerSelection
	plan       rollout.OnboardBatchPlan
	err        error
}
type pickerAppliedMsg struct {
	plan rollout.OnboardBatchPlan
	err  error
}

type projectOnboardModel struct {
	ctx                               context.Context
	home, reason                      string
	adapters                          []agentprojects.Adapter
	sources                           []agentprojects.HierarchySource
	stage                             projectPickerStage
	width, height, generation, cursor int
	search, editor                    textinput.Model
	searchFocus                       bool
	review                            viewport.Model
	tree                              *onboardTree
	rows                              []onboardTreeRow
	hierarchy                         agentprojects.Hierarchy
	catalog                           agentprojects.Catalog
	settings                          []pickerSetting
	selection                         pickerSelection
	plan                              rollout.OnboardBatchPlan
	readiness                         install.InstructionReadiness
	notice, reviewContent             string
	err                               error
	cancelled, applyStarted, applied  bool
	activateChecked                   bool
	statusSetup                       *onboardStatusSetup
	opCancel                          context.CancelFunc
	// Pass-local fixture seams; production supplies no replacements.
	discover     func(context.Context) (agentprojects.Catalog, agentprojects.Hierarchy, error)
	probeFactory pickerProbeFactory
}

func newProjectOnboard(ctx context.Context, home, reason string, adapters []agentprojects.Adapter, sources []agentprojects.HierarchySource) *projectOnboardModel {
	search := textinput.New()
	search.CharLimit = 64
	search.SetWidth(64)
	editor := textinput.New()
	editor.CharLimit = 4096
	editor.SetWidth(64)
	v := viewport.New(viewport.WithWidth(72), viewport.WithHeight(14))
	v.SoftWrap = true
	return &projectOnboardModel{ctx: ctx, home: home, reason: reason, adapters: adapters, sources: sources, stage: pickerLoading, width: 80, height: 30, search: search, editor: editor, searchFocus: false, review: v, readiness: install.InspectInstructions(home)}
}
func (m *projectOnboardModel) Init() tea.Cmd { return m.load() }
func (m *projectOnboardModel) cancelOperation() {
	if m.opCancel != nil {
		m.opCancel()
		m.opCancel = nil
	}
}
func (m *projectOnboardModel) operation() (context.Context, int) {
	m.cancelOperation()
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	m.opCancel = cancel
	m.generation++
	return ctx, m.generation
}
func (m *projectOnboardModel) load() tea.Cmd {
	ctx, generation := m.operation()
	m.stage = pickerLoading
	m.notice = ""
	m.plan = rollout.OnboardBatchPlan{}
	m.settings = nil
	m.selection = pickerSelection{}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := ctx.Err(); err != nil {
			return pickerLoadedMsg{generation: generation, err: err}
		}
		readiness := install.InspectInstructions(m.home)
		if err := ctx.Err(); err != nil {
			return pickerLoadedMsg{generation: generation, err: err, readiness: &readiness}
		}
		if m.discover != nil {
			catalog, hierarchy, err := m.discover(ctx)
			return pickerLoadedMsg{generation: generation, catalog: catalog, hierarchy: hierarchy, err: err, readiness: &readiness}
		}
		catalog := agentprojects.Discover(ctx, m.adapters)
		hierarchy := agentprojects.DiscoverHierarchy(ctx, catalog, m.sources)
		return pickerLoadedMsg{generation: generation, catalog: catalog, hierarchy: hierarchy, err: ctx.Err(), readiness: &readiness}
	}
}
func (m *projectOnboardModel) rebuildRows(reset bool) {
	if m.tree == nil {
		m.rows = nil
		m.cursor = 0
		return
	}
	var previous onboardTreeRow
	if !reset && m.cursor < len(m.rows) {
		previous = m.rows[m.cursor]
	} else {
		previous = onboardTreeRow{group: -2, child: -2}
	}
	m.rows = m.tree.rows(m.search.Value())
	if m.search.Value() == "" {
		m.rows = append(m.rows, onboardTreeRow{group: -1, child: -1})
	}
	m.cursor = 0
	if reset {
		valid := m.rows
		if len(valid) > 0 && valid[len(valid)-1].group < 0 {
			valid = valid[:len(valid)-1]
		}
		if len(valid) > 0 {
			m.cursor = firstOnboardTreeRow(valid)
		}
		return
	}
	for i, row := range m.rows {
		if row.group == previous.group && row.child == previous.child {
			m.cursor = i
			return
		}
	}
	m.cursor = min(m.cursor, max(0, len(m.rows)-1))
}
func (m *projectOnboardModel) finish(cancelled bool) tea.Cmd {
	m.cancelOperation()
	m.cancelled = cancelled
	m.stage = pickerDone
	return tea.Quit
}
func (m *projectOnboardModel) result(err error) error {
	if statusErr := m.statusSetupResult(err); statusErr != nil {
		return statusErr
	}
	// A delivered possible-save result is authoritative even if the terminal
	// loop is canceled before it can observe the queued Quit command.
	if m.err != nil {
		return m.err
	}
	if err != nil && m.applyStarted {
		return rolloutOutcomeUnknown(m.home, err)
	}
	if err != nil {
		return err
	}
	return m.err
}
func (m *projectOnboardModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.stage == pickerDone {
		return m, nil
	}
	if handled, cmd := m.updateStatusSetup(message); handled {
		return m, cmd
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.search.SetWidth(max(16, msg.Width-12))
		m.editor.SetWidth(max(16, msg.Width-10))
		m.review.SetWidth(max(20, msg.Width-8))
		m.review.SetHeight(max(5, msg.Height-12))
		m.review.SetContent(ansi.Wrap(m.reviewContent, m.review.Width(), ""))
		return m, nil
	case pickerLoadedMsg:
		if msg.generation != m.generation || m.stage != pickerLoading {
			return m, nil
		}
		m.cancelOperation()
		m.catalog, m.hierarchy = msg.catalog, msg.hierarchy
		if msg.readiness != nil {
			m.readiness = *msg.readiness
		}
		m.search.SetValue("")
		m.searchFocus = false
		m.search.Blur()
		if msg.err != nil {
			m.notice = "Discovery unavailable: " + msg.err.Error()
		}
		var err error
		if msg.hierarchy.Version != 1 {
			err = fmt.Errorf("unsupported hierarchy version")
		} else {
			m.tree, err = newOnboardTree(msg.hierarchy.Groups)
		}
		if err != nil {
			m.notice = "Unsupported hierarchy: " + err.Error()
			m.tree, _ = newOnboardTree(nil)
		}
		m.hierarchy.Groups = nil // the tree owns its defensive snapshot copy
		m.stage = pickerProjects
		m.rebuildRows(true)
		return m, nil
	case pickerManualMsg:
		if msg.generation != m.generation || m.stage != pickerResolving {
			return m, nil
		}
		m.cancelOperation()
		if msg.err != nil {
			m.notice = msg.err.Error()
			m.stage = pickerManual
			return m, m.editor.Focus()
		}
		var err error
		m.tree, err = newOnboardTree([]agentprojects.Group{msg.group})
		if err != nil {
			m.notice = err.Error()
			m.stage = pickerManual
			return m, nil
		}
		m.tree.toggle(onboardTreeRow{group: 0, child: -1})
		m.search.SetValue("")
		m.rebuildRows(true)
		return m, m.beginSettings()
	case pickerPreviewMsg:
		if msg.generation != m.generation || m.stage != pickerPreparing {
			return m, nil
		}
		m.cancelOperation()
		if msg.err != nil {
			m.stage = pickerSettings
			m.notice = "Preview refused: " + msg.err.Error()
			return m, nil
		}
		m.selection, m.plan = msg.selection, msg.plan
		m.stage = pickerReview
		m.setReview(m.policyReview())
		return m, nil
	case pickerAppliedMsg:
		if m.stage != pickerApplying {
			return m, nil
		}
		m.cancelOperation()
		m.applyStarted = false
		if msg.err != nil {
			m.plan = rollout.OnboardBatchPlan{}
			if pickerUnknown(msg.err) {
				m.err = rolloutOutcomeUnknown(m.home, msg.err)
				return m, m.finish(false)
			}
			m.stage = pickerSettings
			m.generation++
			m.notice = "No policy saved. A new review is required: " + msg.err.Error()
			return m, nil
		}
		m.plan = msg.plan
		m.applied = true
		m.notice = ""
		m.offerStatusSetup()
		return m, nil
	case tea.KeyPressMsg:
		key := msg.String()
		if m.stage == pickerApplying {
			if key == "esc" || key == "ctrl+c" || key == "q" {
				m.notice = "Policy apply owns its result. If interrupted, inspect status/history before retrying."
			}
			return m, nil
		}
		if key == "ctrl+c" {
			return m, m.finish(!m.applied)
		}
		if m.width < 40 || m.height < 20 {
			if key == "esc" {
				return m, m.finish(!m.applied)
			}
			return m, nil
		}
		switch m.stage {
		case pickerProjects:
			return m, m.updateTree(message)
		case pickerManual, pickerID:
			if key == "esc" {
				if m.stage == pickerManual {
					m.stage = pickerProjects
				} else {
					m.stage = pickerSettings
				}
				m.notice = ""
				return m, nil
			}
			if key == "enter" {
				if m.stage == pickerManual {
					return m, m.resolveManual(m.editor.Value())
				}
				if !pickerValidID(m.editor.Value()) {
					m.notice = "Use 1–64 lowercase letters, digits, hyphens or underscores."
					return m, nil
				}
				m.settings[m.cursor].Project = m.editor.Value()
				m.plan = rollout.OnboardBatchPlan{}
				m.generation++
				m.stage = pickerSettings
				m.notice = ""
				return m, nil
			}
		case pickerSettings:
			switch key {
			case "esc", "q":
				m.stage = pickerProjects
				m.plan = rollout.OnboardBatchPlan{}
				m.notice = ""
				m.rebuildRows(true)
				return m, nil
			case "up":
				m.cursor = max(0, m.cursor-1)
				return m, nil
			case "down":
				m.cursor = min(len(m.settings)-1, m.cursor+1)
				return m, nil
			case "i":
				m.editor.CharLimit = 64
				m.editor.SetValue(m.settings[m.cursor].Project)
				m.stage = pickerID
				return m, m.editor.Focus()
			case "s":
				setting := &m.settings[m.cursor]
				if m.group(setting.Group).Kind == "git" {
					scopes := []string{"git", "directory", rollout.WorktreesKind}
					setting.Kind = scopes[(slices.Index(scopes, setting.Kind)+1)%len(scopes)]
					m.generation++
					m.plan = rollout.OnboardBatchPlan{}
				}
				return m, nil
			case "p":
				modes := []string{"new-sessions", "all", "observe", "disabled"}
				setting := &m.settings[m.cursor]
				index := slices.Index(modes, setting.Mode)
				setting.Mode = modes[(index+1)%len(modes)]
				m.generation++
				m.plan = rollout.OnboardBatchPlan{}
				return m, nil
			case "enter":
				return m, m.prepare()
			}
		case pickerReview:
			switch key {
			case "y", "Y":
				if pickerNeedsInstructions(m.plan) && !install.InspectInstructions(m.home).Ready {
					m.notice = "Managed instructions are not ready. Run `" + installNextCommand(m.home, "install") + "` or choose Disabled."
					m.stage = pickerSettings
					return m, nil
				}
				ctx, _ := m.operation()
				selection := clonePickerSelection(m.selection)
				reviewed := m.plan
				m.stage = pickerApplying
				m.applyStarted = true
				m.notice = ""
				return m, func() tea.Msg {
					plan, err := applyPickerPolicy(ctx, m.home, m.reason, selection, reviewed, m.probeFactory)
					return pickerAppliedMsg{plan, err}
				}
			case "b", "esc":
				m.stage = pickerSettings
				m.plan = rollout.OnboardBatchPlan{}
				m.generation++
				return m, nil
			case "enter", "n", "N", "q":
				return m, m.finish(true)
			}
		case pickerOffer:
			if key == "y" || key == "Y" {
				m.activateChecked = true
				return m, m.finish(false)
			}
			if key == "enter" || key == "n" || key == "N" || key == "esc" || key == "q" {
				return m, m.finish(false)
			}
		case pickerDiagnostics:
			if key == "esc" || key == "q" {
				m.stage = pickerProjects
				return m, nil
			}
		case pickerLoading, pickerResolving, pickerPreparing:
			if key == "esc" {
				return m, m.finish(true)
			}
		}
	}
	var cmd tea.Cmd
	switch m.stage {
	case pickerProjects:
		cmd = m.updateTree(message)
	case pickerManual, pickerID:
		m.editor, cmd = m.editor.Update(message)
	case pickerReview, pickerDiagnostics, pickerStatusReview:
		m.review, cmd = m.review.Update(message)
	}
	return m, cmd
}

func (m *projectOnboardModel) updateTree(message tea.Msg) tea.Cmd {
	if msg, ok := message.(tea.KeyPressMsg); ok {
		key := msg.String()
		switch key {
		case "esc":
			if m.search.Value() != "" {
				m.search.SetValue("")
				m.rebuildRows(true)
				return nil
			}
			return m.finish(true)
		case "ctrl+r":
			return m.load()
		case "ctrl+n":
			return m.beginManual()
		case "ctrl+o":
			m.stage = pickerDiagnostics
			m.setReview(m.diagnostics())
			return nil
		case "tab":
			m.searchFocus = !m.searchFocus
			if m.searchFocus {
				return m.search.Focus()
			}
			m.search.Blur()
			return nil
		case "up", "ctrl+k":
			m.searchFocus = false
			m.search.Blur()
			m.cursor = max(0, m.cursor-1)
			return nil
		case "down", "ctrl+j":
			m.searchFocus = false
			m.search.Blur()
			m.cursor = min(max(0, len(m.rows)-1), m.cursor+1)
			return nil
		case "pgup":
			m.searchFocus = false
			m.search.Blur()
			m.cursor = max(0, m.cursor-max(1, (m.height-13)/2))
			return nil
		case "pgdown":
			m.searchFocus = false
			m.search.Blur()
			m.cursor = min(max(0, len(m.rows)-1), m.cursor+max(1, (m.height-13)/2))
			return nil
		case "enter":
			if m.tree != nil && m.tree.hasSelection() {
				return m.beginSettings()
			}
			if m.cursor >= len(m.rows) {
				return nil
			}
			row := m.rows[m.cursor]
			if row.group < 0 {
				return m.beginManual()
			}
			if m.tree.choose(m.rows, m.cursor) {
				return m.beginSettings()
			}
			return nil
		case "ctrl+space", "ctrl+@":
			return m.toggleCurrent()
		case "space":
			if !m.searchFocus {
				return m.toggleCurrent()
			}
		case "left", "right":
			if !m.searchFocus && m.cursor < len(m.rows) {
				row := m.rows[m.cursor]
				if row.group >= 0 {
					m.tree.expanded[m.tree.groups[row.group].Key] = key == "right"
					if key == "left" {
						m.rows[m.cursor].child = -1
					}
					m.rebuildRows(false)
				}
				return nil
			}
		}
		if !m.searchFocus && msg.Text != "" {
			m.searchFocus = true
			m.search.Focus()
		}
	}
	if _, ok := message.(tea.PasteMsg); ok {
		m.searchFocus = true
		m.search.Focus()
	}
	if !m.searchFocus {
		return nil
	}
	before := m.search.Value()
	input, cmd := m.search.Update(message)
	m.search = input
	if input.Value() != before {
		m.rebuildRows(true)
	}
	return cmd
}
func (m *projectOnboardModel) toggleCurrent() tea.Cmd {
	if m.cursor >= len(m.rows) || m.rows[m.cursor].group < 0 {
		return nil
	}
	m.tree.toggle(m.rows[m.cursor])
	m.generation++
	m.plan = rollout.OnboardBatchPlan{}
	return nil
}
func (m *projectOnboardModel) beginManual() tea.Cmd {
	m.stage = pickerManual
	m.notice = ""
	cwd, _ := os.Getwd()
	m.editor.CharLimit = 4096
	m.editor.SetValue(cwd)
	return m.editor.Focus()
}
func (m *projectOnboardModel) resolveManual(path string) tea.Cmd {
	ctx, generation := m.operation()
	m.stage = pickerResolving
	m.notice = ""
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if path == "~" || strings.HasPrefix(path, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return pickerManualMsg{generation: generation, err: err}
			}
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
			}
		}
		if err := ctx.Err(); err != nil {
			return pickerManualMsg{generation: generation, err: err}
		}
		canonical, err := filepath.Abs(path)
		if err == nil {
			canonical, err = filepath.EvalSymlinks(canonical)
		}
		if err != nil {
			return pickerManualMsg{generation: generation, err: err}
		}
		member, err := agentprojects.InspectDirectory(ctx, canonical)
		if err != nil {
			return pickerManualMsg{generation: generation, err: err}
		}
		group := agentprojects.Group{Kind: "directory", Directory: member.Path, Directories: []string{member.Path}, Memberships: []agentprojects.Membership{{Member: member}}}
		budget := gitmeta.NewBudget(ctx)
		defer budget.Close()
		factory := m.probeFactory
		if factory == nil {
			factory = pickerDefaultProbe
		}
		if probe, e := factory(budget); e == nil {
			if info, e := probe.Resolve(member.Path); e == nil {
				group.Kind = "git"
				group.Directory = info.Root.Path
				group.Memberships[0].Checkout = &info
				if !slices.Contains(group.Directories, info.Root.Path) {
					group.Directories = append(group.Directories, info.Root.Path)
				}
			}
		}
		group.Name = agentprojects.SanitizeTitle(filepath.Base(group.Directory))
		digest := sha256.Sum256([]byte("manual:v1\x00" + member.Path + "\x00" + member.Identity))
		group.Key = hex.EncodeToString(digest[:])
		return pickerManualMsg{generation, group, ctx.Err()}
	}
}
func (m *projectOnboardModel) group(key string) agentprojects.Group {
	for _, group := range m.tree.groups {
		if group.Key == key {
			return group
		}
	}
	return agentprojects.Group{}
}
func (m *projectOnboardModel) beginSettings() tea.Cmd {
	groups := m.tree.selectedGroups()
	if len(groups) == 0 {
		return nil
	}
	state, err := (rollout.Store{Home: m.home}).Read()
	if err != nil {
		m.notice = err.Error()
		return nil
	}
	previous := map[string]pickerSetting{}
	for _, setting := range m.settings {
		previous[setting.Group] = setting
	}
	m.settings = nil
	used := map[string]bool{}
	for _, group := range groups {
		if retained, ok := previous[group.Key]; ok {
			used[retained.Project] = true
		}
	}
	for _, group := range groups {
		setting := pickerSetting{Group: group.Key, Project: onboardProjectID(group.Directory), Kind: group.Kind, Mode: "new-sessions"}
		if retained, ok := previous[group.Key]; ok {
			m.settings = append(m.settings, retained)
			used[retained.Project] = true
			continue
		}
		found := false
		for _, project := range state.Projects {
			if project.Removed {
				continue
			}
			for _, binding := range project.Bindings {
				matches := binding.Root == group.Directory
				if (binding.Kind == "git" || binding.Kind == rollout.WorktreesKind) && group.Kind == "git" {
					for _, member := range group.Memberships {
						checkout := member.Checkout
						matches = matches || checkout != nil && checkout.Common.Path == binding.GitDirectory && checkout.Common.Identity == binding.GitIdentity
					}
				}
				if !binding.Removed && matches {
					setting.Project, setting.Mode = project.ID, project.Mode
					if binding.Kind == rollout.WorktreesKind && group.Kind == "git" {
						setting.Kind = rollout.WorktreesKind
					}
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			base := setting.Project
			for suffix := 2; ; suffix++ {
				exists := used[setting.Project]
				for _, project := range state.Projects {
					exists = exists || project.ID == setting.Project
				}
				if !exists {
					break
				}
				ending := fmt.Sprintf("-%d", suffix)
				setting.Project = strings.TrimRight(base[:min(len(base), 64-len(ending))], "-") + ending
			}
		}
		used[setting.Project] = true
		m.settings = append(m.settings, setting)
	}
	m.cursor = 0
	m.stage = pickerSettings
	m.notice = ""
	m.plan = rollout.OnboardBatchPlan{}
	m.generation++
	return nil
}
func (m *projectOnboardModel) prepare() tea.Cmd {
	ctx, generation := m.operation()
	selection := pickerSelection{Groups: m.tree.selectedGroups(), Settings: slices.Clone(m.settings)}
	for _, recipient := range m.tree.selectedRecipients() {
		selection.Checked = append(selection.Checked, pickerChecked{Group: recipient.group, Session: recipient.session})
	}
	m.stage = pickerPreparing
	m.notice = ""
	selection = clonePickerSelection(selection)
	return func() tea.Msg {
		plan, err := preparePickerPolicy(ctx, m.home, m.reason, selection, m.probeFactory)
		return pickerPreviewMsg{generation, selection, plan, err}
	}
}
func (m *projectOnboardModel) setReview(content string) {
	m.reviewContent = content
	m.review.SetContent(ansi.Wrap(content, m.review.Width(), ""))
	m.review.GotoTop()
}
func (m *projectOnboardModel) diagnostics() string {
	var b strings.Builder
	for _, adapter := range m.catalog.Adapters {
		for _, warning := range adapter.Warnings {
			fmt.Fprintf(&b, "%s: %s\n", terminalSafe(adapter.Adapter), agentprojects.SanitizeTitle(warning))
		}
	}
	for _, coverage := range m.hierarchy.Coverage {
		for _, warning := range coverage.Warnings {
			fmt.Fprintf(&b, "%s: %s\n", terminalSafe(coverage.Adapter), agentprojects.SanitizeTitle(warning))
		}
		if coverage.Truncated {
			fmt.Fprintf(&b, "%s: coverage is truncated.\n", coverage.Adapter)
		}
	}
	for _, warning := range m.hierarchy.Warnings {
		fmt.Fprintln(&b, agentprojects.SanitizeTitle(warning))
	}
	if b.Len() == 0 {
		return "No metadata diagnostics were reported. Saved metadata does not prove liveness or enrollment."
	}
	return b.String()
}
func (m *projectOnboardModel) policyReview() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Projects: %d · checked chats: %d\n", len(m.plan.Projects), len(m.selection.Checked))
	for _, project := range m.plan.Projects {
		fmt.Fprintf(&b, "\n%s: %s", project.After.ID, project.After.Mode)
		if project.Unchanged {
			fmt.Fprint(&b, " (unchanged)")
		}
		if project.Before != nil {
			fmt.Fprintf(&b, "; previous %s", project.Before.Mode)
		}
		fmt.Fprintln(&b)
		if project.After.Paused {
			fmt.Fprintln(&b, "  Enrollment remains paused.")
		}
		fmt.Fprintln(&b, "  All project bindings after this policy (including retained bindings):")
		for _, binding := range project.After.Bindings {
			state := ""
			if binding.Removed {
				state = " (removed; blocks broader fallback)"
			}
			fmt.Fprintf(&b, "    %s%s · %s\n", pickerScopeLabel(binding.Kind), state, terminalSafe(binding.Root))
		}
	}
	fmt.Fprintln(&b, "\nRequested scope:")
	for _, change := range m.plan.Request.Changes {
		fmt.Fprintf(&b, "  %s · %s · %s\n", change.Project, pickerScopeLabel(change.Binding.Kind), terminalSafe(change.Binding.Root))
		if change.Binding.Kind == rollout.WorktreesKind {
			fmt.Fprintf(&b, "    Git common directory: %s\n", terminalSafe(change.Binding.GitDirectory))
		}
		for _, group := range m.selection.Groups {
			var setting pickerSetting
			for _, candidate := range m.selection.Settings {
				if candidate.Group == group.Key {
					setting = candidate
					break
				}
			}
			if setting.Project != change.Project {
				continue
			}
			for _, session := range group.Sessions {
				covered := false
				if change.Binding.Kind == "directory" {
					rel, err := filepath.Rel(change.Binding.Root, session.Directory)
					covered = err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
				}
				for _, membership := range group.Memberships {
					if membership.Member.Path == session.Directory {
						root := membership.Member.Path
						if change.Binding.Kind == "git" && membership.Checkout != nil {
							root = membership.Checkout.Root.Path
							covered = covered || root == change.Binding.Root
						}
						if change.Binding.Kind == rollout.WorktreesKind && membership.Checkout != nil {
							common := membership.Checkout.Common
							covered = common.Path == change.Binding.GitDirectory && common.Identity == change.Binding.GitIdentity
						}
					}
				}
				if !covered {
					continue
				}
				mark := "[ ]"
				for _, recipient := range m.selection.Checked {
					if recipient.Session.Ref == session.Ref {
						mark = "[x]"
						break
					}
				}
				label := agentprojects.SanitizeTitle(session.Name)
				if label == "" {
					label = "Session " + session.Ref.ID[:8] + "…"
				}
				fmt.Fprintf(&b, "    %s %s · %s\n", mark, label, session.Ref.Adapter)
			}
		}
	}
	fmt.Fprintf(&b, "\nExisting ARM session records affected by policy: %d (separate from checked saved chats).\n", len(m.plan.Affected))
	fmt.Fprintln(&b, "Unchecked chats stay in directory policy coverage; no activation is offered to them. Unchecking does not revoke existing participation.")
	fmt.Fprintln(&b, "Policy is one atomic save. Activation is a separate default-no offer; no chat is identified, invited, enrolled or sent a message here.")
	fmt.Fprintln(&b, "New-sessions excludes older chats unless separately invited. Saved metadata supplies no original start or delivery capability.")
	if pickerNeedsInstructions(m.plan) && !install.InspectInstructions(m.home).Ready {
		fmt.Fprintln(&b, "\nInstructions NOT READY. Run `"+installNextCommand(m.home, "install")+"` or choose Disabled.")
	}
	fmt.Fprintf(&b, "\nReason: %s\nReview: %s\n%s\n", terminalSafe(m.reason), m.plan.Review, m.plan.Effect)
	return b.String()
}

func pickerScopeLabel(kind string) string {
	if kind == rollout.WorktreesKind {
		return "All Git worktrees (including future worktrees)"
	}
	return kind
}
func (m *projectOnboardModel) activationGuide(index int) string {
	if index < 0 || index >= len(m.selection.Checked) {
		return "No checked chats."
	}
	recipient := m.selection.Checked[index]
	project, mode := "", ""
	for _, setting := range m.selection.Settings {
		if setting.Group == recipient.Group {
			project, mode = setting.Project, setting.Mode
			break
		}
	}
	name := agentprojects.SanitizeTitle(recipient.Session.Name)
	if name == "" {
		name = "Saved session"
	}
	command := installNextCommand(m.home, "rollout check --cwd . --adapter "+recipient.Session.Ref.Adapter+" --session YOUR_ORIGINAL_SESSION_UUID --json")
	return fmt.Sprintf("Checked chat %d of %d\n%s · %s\nSaved reference: %s (UNVERIFIED)\nRecorded directory: %s (UNVERIFIED)\nProject: %s · policy: %s\n\nCopy-only: automatic current native verification is unavailable. Original conversation start is unknown; eligibility is not established and no delivery generation or ARM identity has been created. Nothing was delivered.\n\nOpen the intended chat and copy these instructions:\n\nRead the current checkout's AGENTS.md and the managed ARM instructions. Use this chat's actual working directory, adapter, original session UUID and original start if known. Do not substitute the saved reference above as verified identity or the current time as original start. Run:\n%s\n\nFollow the returned policy. Identify and acknowledge only if eligible and explicitly authorized; disabled policy permits neither. Do not invite yourself or create a new chat to bypass it. If instructions are not installed, install them before enabling adoption.\n", index+1, len(m.selection.Checked), name, recipient.Session.Ref.Adapter, recipient.Session.Ref.ID, terminalSafe(recipient.Session.Directory), project, mode, terminalSafe(command))
}

func (m *projectOnboardModel) View() tea.View {
	var b strings.Builder
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	selected := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	fmt.Fprintln(&b, title.Render("ARM · Project onboarding"))
	if m.stage != pickerDone && (m.width < 40 || m.height < 20) {
		if m.stage == pickerApplying {
			return tea.NewView(b.String() + "Policy write in progress. Waiting for the result.\n")
		}
		return tea.NewView(b.String() + "Enlarge the terminal to at least 40×20. Esc cancels.\n")
	}
	switch m.stage {
	case pickerLoading:
		fmt.Fprintln(&b, "Reading bounded project and saved-chat metadata…")
	case pickerProjects:
		fmt.Fprintln(&b, "Select projects and chats · saved metadata is unverified")
		focus := "Search: "
		if m.searchFocus {
			focus = "Search> "
		}
		fmt.Fprintln(&b, focus+m.search.View())
		if !m.readiness.Ready {
			fmt.Fprintln(&b, ansi.Truncate("Instructions not ready; Disabled remains available. "+installNextCommand(m.home, "install"), max(20, m.width-4), "…"))
		}
		diagnostics := m.diagnostics()
		if diagnostics != "No metadata diagnostics were reported. Saved metadata does not prove liveness or enrollment." {
			line := strings.SplitN(diagnostics, "\n", 2)[0]
			fmt.Fprintln(&b, ansi.Truncate(line, max(20, m.width-4), "…"))
		}
		visible := max(1, (m.height-13)/2)
		start := max(0, m.cursor-visible+1)
		end := min(len(m.rows), start+visible)
		if len(m.rows) == 0 {
			fmt.Fprintln(&b, "No matches. Your checked selection is retained.")
		}
		for index := start; index < end; index++ {
			row := m.rows[index]
			label, detail := "Choose another directory…", "Manual project-only selection"
			if row.group >= 0 {
				group := m.tree.groups[row.group]
				label = m.tree.title(row)
				detail = terminalSafe(group.Directory)
				if row.child < 0 {
					if row.matchDirectory != "" {
						detail = "Matched directory · " + terminalSafe(row.matchDirectory)
					}
					fold := "▸ "
					if m.tree.expanded[group.Key] {
						fold = "▾ "
					}
					label = fold + label
				} else {
					detail = "    " + terminalSafe(group.Sessions[row.child].Directory)
				}
			}
			prefix := "  "
			if index == m.cursor {
				prefix = "> "
			}
			line := ansi.Truncate(prefix+label, max(20, m.width-4), "…")
			if index == m.cursor {
				line = selected.Render(line)
			}
			fmt.Fprintln(&b, line)
			fmt.Fprintln(&b, ansi.Truncate("  "+detail, max(20, m.width-4), "…"))
		}
		projects, chats := 0, 0
		if m.tree != nil {
			for _, checked := range m.tree.projects {
				if checked {
					projects++
				}
			}
			chats = len(m.tree.recipients)
		}
		fmt.Fprintf(&b, "Enter: review %d projects · %d chats\n", projects, chats)
		fmt.Fprintln(&b, "Space toggles in list · Tab search/list")
		fmt.Fprintln(&b, "←/→ fold · Ctrl+N manual · Esc clears/back")
		fmt.Fprintln(&b, "Ctrl+R refresh · Ctrl+O diagnostics")
	case pickerManual:
		fmt.Fprintln(&b, "Manual project-only directory")
		fmt.Fprintln(&b, m.editor.View())
		fmt.Fprintln(&b, "Enter replaces the checked selection · Esc back")
	case pickerResolving:
		fmt.Fprintln(&b, "Inspecting the explicit directory and checkout…")
	case pickerSettings:
		fmt.Fprintln(&b, "Review project IDs, boundaries and policies")
		visible := max(1, (m.height-12)/3)
		start := max(0, m.cursor-visible+1)
		for i := start; i < min(len(m.settings), start+visible); i++ {
			setting := m.settings[i]
			group := m.group(setting.Group)
			prefix := "  "
			if i == m.cursor {
				prefix = "> "
			}
			fmt.Fprintln(&b, ansi.Truncate(prefix+group.Name+" → "+setting.Project, max(20, m.width-4), "…"))
			fmt.Fprintln(&b, ansi.Truncate("    "+pickerScopeLabel(setting.Kind)+" · "+setting.Mode, max(20, m.width-4), "…"))
			fmt.Fprintln(&b, ansi.Truncate("    "+terminalSafe(group.Directory), max(20, m.width-4), "…"))
		}
		fmt.Fprintln(&b, "↑/↓ project · i edit ID · s boundary · p policy")
		fmt.Fprintln(&b, "new-sessions / all / observe / disabled")
		fmt.Fprintln(&b, "Enter prepares exact review · Esc back")
	case pickerID:
		fmt.Fprintln(&b, "Edit ARM project ID")
		fmt.Fprintln(&b, m.editor.View())
		fmt.Fprintln(&b, "Enter saves this choice · Esc back")
	case pickerPreparing:
		fmt.Fprintln(&b, "Revalidating all member checkouts and preparing review…")
	case pickerReview:
		fmt.Fprintln(&b, title.Render("Review policy coverage and activation selection"))
		fmt.Fprintln(&b, m.review.View())
		fmt.Fprintln(&b, "↑/↓ scroll · b back · Save policy? [y/N]")
	case pickerApplying:
		fmt.Fprintln(&b, "Revalidating reviewed membership and applying policy…")
	case pickerOffer:
		fmt.Fprintln(&b, "Project policy saved. No chats were activated.")
		fmt.Fprintf(&b, "Verify exactly %d checked chats for activation? [y/N]\n", len(m.selection.Checked))
		fmt.Fprintln(&b, "Supported recipients get a separate message review before sending.")
	case pickerStatusOffer, pickerStatusPreparing, pickerStatusReview, pickerStatusApplying:
		fmt.Fprint(&b, m.statusSetupView())
	case pickerDiagnostics:
		fmt.Fprintln(&b, title.Render("Discovery coverage and diagnostics"))
		fmt.Fprintln(&b, m.review.View())
		fmt.Fprintln(&b, "↑/↓ scroll · Esc back")
	case pickerDone:
		if m.err != nil {
			fmt.Fprintln(&b, terminalSafe(m.err.Error()))
		} else if m.cancelled {
			fmt.Fprintln(&b, "Cancelled. No onboarding policy was applied.")
		} else if m.applied {
			if m.plan.Unchanged {
				fmt.Fprintln(&b, "Policy already configured; no policy bytes changed.")
			} else {
				fmt.Fprintln(&b, "Reviewed project policies saved.")
			}
			if m.activateChecked {
				fmt.Fprintln(&b, "Opening the separately requested checked-chat activation review.")
			} else {
				fmt.Fprintln(&b, "No activation was delivered. Unchecking adds no revocations; existing obligations remain.")
			}
		}
		fmt.Fprintln(&b, "Inspect: `"+terminalSafe(installNextCommand(m.home, "rollout status"))+"`")
	}
	if m.notice != "" {
		fmt.Fprintln(&b, "\n"+terminalSafe(m.notice))
	}
	return tea.NewView(lipgloss.NewStyle().Padding(1, 1).Width(max(20, m.width-2)).Render(b.String()))
}
