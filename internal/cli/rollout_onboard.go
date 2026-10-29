package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"getarm.org/internal/agentprojects"
	"getarm.org/internal/install"
	"getarm.org/internal/model"
	"getarm.org/internal/rollout"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

func (a *app) rolloutOnboard(args []string) error {
	f := a.flags("rollout project onboard")
	adapter := f.String("adapter", "all", "project discovery: all, codex or claude")
	codexHome := f.String("codex-home", "", "explicit Codex metadata directory")
	claudeHome := f.String("claude-home", "", "explicit Claude metadata directory")
	claudeConfig := f.String("claude-config", "", "explicit Claude project configuration file")
	reason := f.String("reason", "Onboard project for gradual agent coordination", "reason recorded with the confirmed policy")
	dry := f.Bool("dry-run", false, "discover suggestions only; do not open an editor or write policy")
	f.Bool("json", false, "return read-only discovery JSON instead of the interactive editor")
	if e := a.parse(f, args, false); e != nil {
		return e
	}
	if *adapter != "all" && *adapter != "codex" && *adapter != "claude" {
		return fmt.Errorf("adapter must be all, codex or claude")
	}
	userHome, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	readiness := install.InspectInstructions(a.home)
	chooseHome := func(explicit, env, kind string) string {
		if explicit != "" {
			return explicit
		}
		if p := os.Getenv(env); p != "" {
			return p
		}
		var roots []string
		for _, target := range readiness.Targets {
			if target.Adapter == kind && target.State == "ready" {
				roots = append(roots, filepath.Dir(target.Path))
			}
		}
		if len(roots) == 1 {
			return roots[0]
		}
		return filepath.Join(userHome, "."+kind)
	}
	*codexHome = chooseHome(*codexHome, "CODEX_HOME", "codex")
	*claudeHome = chooseHome(*claudeHome, "CLAUDE_CONFIG_DIR", "claude")
	for _, path := range []*string{codexHome, claudeHome} {
		*path, e = filepath.Abs(*path)
		if e != nil {
			return e
		}
	}
	if *claudeConfig == "" {
		*claudeConfig = filepath.Join(*claudeHome, ".claude.json")
		if filepath.Clean(*claudeHome) == filepath.Join(userHome, ".claude") {
			*claudeConfig = filepath.Join(userHome, ".claude.json")
		}
	} else {
		*claudeConfig, e = filepath.Abs(*claudeConfig)
		if e != nil {
			return e
		}
	}
	var adapters []agentprojects.Adapter
	if *adapter == "all" || *adapter == "codex" {
		adapters = append(adapters, agentprojects.Codex{Home: *codexHome})
	}
	if *adapter == "all" || *adapter == "claude" {
		adapters = append(adapters, agentprojects.Claude{Home: *claudeHome, ConfigFile: *claudeConfig})
	}
	if *dry || a.machine() {
		catalog := agentprojects.Discover(a.ctx, adapters)
		if e := a.ctx.Err(); e != nil {
			return e
		}
		return a.printRollout(struct {
			ReadOnly     bool                         `json:"read_only"`
			Instructions install.InstructionReadiness `json:"instructions"`
			Catalog      agentprojects.Catalog        `json:"catalog"`
		}{true, readiness, catalog})
	}
	if !a.inputTTY {
		return fmt.Errorf("onboarding requires terminal input; use --json or --dry-run for discovery only")
	}
	model := newRolloutOnboard(a.ctx, a.home, *reason, adapters)
	opts := []tea.ProgramOption{tea.WithContext(a.ctx), tea.WithInput(a.in), tea.WithOutput(a.out), tea.WithoutSignalHandler()}
	if !a.colors {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}
	_, e = tea.NewProgram(model, opts...).Run()
	return model.result(e)
}

func rolloutOutcomeUnknown(home string, err error) error {
	return model.Err("rollout_outcome_unknown", fmt.Sprintf("rollout policy may have been applied; inspect `%s` and `%s` before retrying: %v", installNextCommand(home, "rollout status"), installNextCommand(home, "rollout history"), err))
}

func (m *rolloutOnboardModel) result(e error) error {
	if e != nil && m.applyStarted {
		return rolloutOutcomeUnknown(m.home, e)
	}
	if e != nil {
		return e
	}
	return m.err
}

type onboardStage int

const (
	onboardLoading onboardStage = iota
	onboardProjects
	onboardDirectory
	onboardResolving
	onboardScope
	onboardName
	onboardPolicy
	onboardPreparing
	onboardReview
	onboardApplying
	onboardDone
)

type onboardCatalogMsg struct{ catalog agentprojects.Catalog }
type onboardScopeMsg struct {
	generation int
	directory  rollout.Binding
	git        *rollout.Binding
	err        error
}
type onboardPreviewMsg struct {
	generation int
	plan       rollout.Plan
	err        error
}
type onboardAppliedMsg struct {
	plan rollout.Plan
	err  error
}
type onboardChoice struct{ title, detail, value string }
type onboardItem struct {
	project agentprojects.Project
	manual  bool
}

func (i onboardItem) Title() string {
	if i.manual {
		return "Choose another directory…"
	}
	var sources []string
	for _, e := range i.project.Evidence {
		if !slices.Contains(sources, e.Adapter) {
			sources = append(sources, e.Adapter)
		}
	}
	return terminalSafe(filepath.Base(i.project.Directory)) + "  ·  " + terminalSafe(strings.Join(sources, " + "))
}
func (i onboardItem) Description() string {
	if i.manual {
		return "Enter a directory yourself; discovery does not authorize adoption."
	}
	strong := "Known project"
	for _, e := range i.project.Evidence {
		if e.Kind == "selected-workspace" {
			strong = "Selected workspace (saved state)"
		}
		if e.Kind == "session-marker" {
			strong = "Session marker (activity unverified)"
			break
		}
	}
	return terminalSafe(i.project.Directory) + " · " + strong
}
func (i onboardItem) FilterValue() string { return i.Title() + " " + i.Description() }

type rolloutOnboardModel struct {
	ctx                               context.Context
	home, reason                      string
	adapters                          []agentprojects.Adapter
	stage                             onboardStage
	width, height, generation, cursor int
	projects                          list.Model
	input                             textinput.Model
	review                            viewport.Model
	catalog                           agentprojects.Catalog
	readiness                         install.InstructionReadiness
	choices                           []onboardChoice
	directory                         rollout.Binding
	git                               *rollout.Binding
	binding                           rollout.Binding
	id, mode, notice                  string
	reviewContent                     string
	plan                              rollout.Plan
	applyStarted, cancelled           bool
	err                               error
}

func newRolloutOnboard(ctx context.Context, home, reason string, adapters []agentprojects.Adapter) *rolloutOnboardModel {
	picker := list.New(nil, list.NewDefaultDelegate(), 76, 12)
	picker.Title = "Choose a project directory"
	picker.DisableQuitKeybindings()
	picker.SetStatusBarItemName("choice", "choices")
	input := textinput.New()
	input.CharLimit = 4096
	input.SetWidth(68)
	v := viewport.New(viewport.WithWidth(72), viewport.WithHeight(14))
	v.SoftWrap = true
	return &rolloutOnboardModel{ctx: ctx, home: home, reason: reason, adapters: adapters, readiness: install.InspectInstructions(home), stage: onboardLoading, width: 80, height: 30, projects: picker, input: input, review: v}
}
func (m *rolloutOnboardModel) Init() tea.Cmd {
	return func() tea.Msg { return onboardCatalogMsg{agentprojects.Discover(m.ctx, m.adapters)} }
}
func (m *rolloutOnboardModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.stage == onboardDone {
		return m, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		padding := 13
		if msg.Width < 60 {
			padding = 16
		}
		if !m.readiness.Ready {
			padding += 2
		}
		m.projects.SetSize(max(20, msg.Width-4), max(6, min(16, msg.Height-padding)))
		m.input.SetWidth(max(20, msg.Width-10))
		m.review.SetWidth(max(20, msg.Width-8))
		m.review.SetHeight(max(5, msg.Height-12))
		if m.stage == onboardReview {
			m.review.SetContent(ansi.Wrap(m.reviewContent, m.review.Width(), ""))
		}
		return m, nil
	case onboardCatalogMsg:
		m.catalog = msg.catalog
		items := []list.Item{}
		for _, project := range msg.catalog.Projects {
			items = append(items, onboardItem{project: project})
		}
		items = append(items, onboardItem{manual: true})
		m.stage = onboardProjects
		return m, m.projects.SetItems(items)
	case onboardScopeMsg:
		if msg.generation != m.generation || m.stage != onboardResolving {
			return m, nil
		}
		if msg.err != nil {
			m.notice = msg.err.Error()
			m.stage = onboardDirectory
			return m, m.input.Focus()
		}
		m.directory, m.git = msg.directory, msg.git
		m.stage = onboardScope
		m.setScopes()
		return m, nil
	case onboardPreviewMsg:
		if msg.generation != m.generation || m.stage != onboardPreparing {
			return m, nil
		}
		if msg.err != nil {
			m.notice = "Preview refused: " + msg.err.Error()
			m.stage = onboardPolicy
			return m, nil
		}
		m.plan = msg.plan
		m.stage = onboardReview
		m.reviewContent = m.reviewText()
		m.review.SetContent(ansi.Wrap(m.reviewContent, m.review.Width(), ""))
		m.review.GotoTop()
		return m, nil
	case onboardAppliedMsg:
		m.stage = onboardDone
		m.notice = ""
		m.plan = msg.plan
		if msg.err != nil {
			m.err = rolloutOutcomeUnknown(m.home, msg.err)
		}
		return m, tea.Quit
	case tea.KeyPressMsg:
		key := msg.String()
		// Once confirmed, the write owns completion. Resizing must not make
		// cancellation claim that a pending or completed write never happened.
		if m.stage == onboardApplying {
			if key == "ctrl+c" || key == "esc" || key == "q" {
				m.notice = "A policy write is in progress. Inspect status/history if interrupted."
			}
			return m, nil
		}
		if key == "ctrl+c" {
			m.cancelled = true
			m.stage = onboardDone
			return m, tea.Quit
		}
		if m.width < 40 || m.height < 20 {
			if key == "esc" || key == "q" {
				m.cancelled = true
				m.stage = onboardDone
				return m, tea.Quit
			}
			return m, nil
		}
		if m.stage == onboardProjects {
			if m.projects.FilterState() != list.Filtering {
				if key == "q" || key == "esc" && m.projects.FilterState() == list.Unfiltered {
					m.cancelled = true
					m.stage = onboardDone
					return m, tea.Quit
				}
				if key == "enter" {
					if item, ok := m.projects.SelectedItem().(onboardItem); ok {
						if item.manual {
							m.stage = onboardDirectory
							cwd, _ := os.Getwd()
							m.input.CharLimit = 4096
							m.input.SetValue(cwd)
							m.notice = ""
							return m, m.input.Focus()
						}
						return m, m.resolve(item.project.Directory)
					}
				}
			}
		} else if m.stage == onboardDirectory || m.stage == onboardName {
			if key == "esc" {
				if m.stage == onboardName {
					m.stage = onboardScope
					m.setScopes()
				} else {
					m.stage = onboardProjects
				}
				m.generation++
				m.notice = ""
				return m, nil
			}
			if key == "enter" {
				if m.stage == onboardDirectory {
					return m, m.resolve(m.input.Value())
				}
				m.id = m.input.Value()
				valid := len(m.id) > 0 && len(m.id) <= 64
				for _, c := range m.id {
					if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
						valid = false
					}
				}
				if !valid {
					m.notice = "Use 1–64 lowercase letters, digits, hyphens or underscores."
					return m, nil
				}
				m.setPolicies()
				m.stage = onboardPolicy
				m.notice = ""
				return m, nil
			}
		} else if m.stage == onboardScope || m.stage == onboardPolicy {
			switch key {
			case "up", "k":
				m.cursor = max(0, m.cursor-1)
				return m, nil
			case "down", "j":
				m.cursor = min(len(m.choices)-1, m.cursor+1)
				return m, nil
			case "esc", "q":
				if m.stage == onboardPolicy {
					m.stage = onboardName
					m.input.CharLimit = 64
					m.input.SetValue(m.id)
					m.notice = ""
					return m, m.input.Focus()
				}
				m.stage = onboardProjects
				m.notice = ""
				return m, nil
			case "enter":
				if m.stage == onboardScope {
					m.binding = m.directory
					if m.choices[m.cursor].value == "git" {
						m.binding = *m.git
					}
					m.id = onboardProjectID(m.binding.Root)
					st, e := (rollout.Store{Home: m.home}).Read()
					if e != nil {
						m.notice = e.Error()
						return m, nil
					}
					existingBinding := false
					for _, p := range st.Projects {
						if p.Removed {
							continue
						}
						for _, b := range p.Bindings {
							if b.Root == m.binding.Root {
								m.id = p.ID
								existingBinding = true
								break
							}
						}
						if existingBinding {
							break
						}
					}
					if !existingBinding {
						base := m.id
						for suffix := 2; ; suffix++ {
							exists := false
							for _, p := range st.Projects {
								if p.ID == m.id {
									exists = true
									break
								}
							}
							if !exists {
								break
							}
							ending := fmt.Sprintf("-%d", suffix)
							m.id = strings.TrimRight(base[:min(len(base), 64-len(ending))], "-") + ending
						}
					}
					m.stage = onboardName
					m.input.CharLimit = 64
					m.input.SetValue(m.id)
					m.notice = ""
					return m, m.input.Focus()
				}
				m.mode = m.choices[m.cursor].value
				m.stage = onboardPreparing
				m.generation++
				generation := m.generation
				change := m.change()
				store := rollout.Store{Home: m.home}
				return m, func() tea.Msg { p, e := store.Preview(change); return onboardPreviewMsg{generation, p, e} }
			}
		} else if m.stage == onboardReview {
			switch key {
			case "y", "Y":
				if !m.plan.Unchanged && m.mode != "disabled" && !install.InspectInstructions(m.home).Ready {
					m.notice = "Managed instructions are not ready. Run `" + installNextCommand(m.home, "install") + "` before enabling adoption, or choose Disabled."
					m.stage = onboardPolicy
					return m, nil
				}
				m.stage = onboardApplying
				m.applyStarted = true
				change := m.change()
				review := m.plan.Review
				store := rollout.Store{Home: m.home}
				home := m.home
				ctx := m.ctx
				unchanged := m.plan.Unchanged
				return m, func() tea.Msg {
					if e := ctx.Err(); e != nil {
						return onboardAppliedMsg{err: e}
					}
					if !unchanged && change.Mode != "disabled" && !install.InspectInstructions(home).Ready {
						return onboardAppliedMsg{err: fmt.Errorf("managed instructions changed before application")}
					}
					p, e := store.Apply(change, review)
					return onboardAppliedMsg{p, e}
				}
			case "b", "esc":
				m.stage = onboardPolicy
				m.notice = ""
				return m, nil
			case "enter", "n", "N", "q":
				m.cancelled = true
				m.stage = onboardDone
				return m, tea.Quit
			}
		} else if key == "esc" && (m.stage == onboardResolving || m.stage == onboardPreparing || m.stage == onboardLoading) {
			m.generation++
			m.cancelled = true
			m.stage = onboardDone
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	switch m.stage {
	case onboardProjects:
		m.projects, cmd = m.projects.Update(message)
	case onboardDirectory, onboardName:
		m.input, cmd = m.input.Update(message)
	case onboardReview:
		m.review, cmd = m.review.Update(message)
	}
	return m, cmd
}
func (m *rolloutOnboardModel) resolve(path string) tea.Cmd {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, e := os.UserHomeDir(); e == nil {
			if path == "~" {
				path = home
			} else {
				path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
			}
		}
	}
	m.input.CharLimit = 4096
	m.input.SetValue(path)
	m.stage = onboardResolving
	m.notice = ""
	m.generation++
	generation := m.generation
	return func() tea.Msg {
		directory, e := rollout.NewBinding("directory", path)
		if e != nil {
			return onboardScopeMsg{generation: generation, err: e}
		}
		git, gitErr := rollout.NewBinding("git", path)
		var root *rollout.Binding
		if gitErr == nil {
			root = &git
		}
		return onboardScopeMsg{generation, directory, root, nil}
	}
}
func onboardProjectID(path string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(filepath.Base(path)) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	id := strings.Trim(b.String(), "-")
	if len(id) > 64 {
		id = strings.TrimRight(id[:64], "-")
	}
	if id == "" {
		id = "project"
	}
	return id
}
func (m *rolloutOnboardModel) setPolicies() {
	m.cursor = 0
	m.choices = []onboardChoice{{"New sessions only (recommended)", "Existing chats stay out unless explicitly invited.", "new-sessions"}, {"All sessions in this project", "Existing and future sessions may acknowledge participation.", "all"}, {"Discovery only / manual pilot", "Observe sessions, then invite a small chosen cohort.", "observe"}, {"Keep disabled", "Save the project boundary without enabling discovery or adoption.", "disabled"}}
	st, e := (rollout.Store{Home: m.home}).Read()
	if e != nil {
		return
	}
	for _, p := range st.Projects {
		if p.ID == m.id && !p.Removed {
			m.choices = append([]onboardChoice{{"Keep current policy: " + p.Mode, "Other project bindings and pause state are retained.", p.Mode}}, m.choices...)
			break
		}
	}
}
func (m *rolloutOnboardModel) change() rollout.Change {
	binding := m.binding
	return rollout.Change{Action: "onboard", Project: m.id, Binding: &binding, Mode: m.mode, Reason: m.reason}
}
func (m *rolloutOnboardModel) setScopes() {
	m.cursor = 0
	m.choices = []onboardChoice{}
	if m.git != nil {
		m.choices = append(m.choices, onboardChoice{"Git checkout (recommended)", m.git.Root, "git"})
	}
	m.choices = append(m.choices, onboardChoice{"Directory tree", m.directory.Root + " and its descendants", "directory"})
}
func (m *rolloutOnboardModel) reviewText() string {
	p := m.plan
	var b strings.Builder
	fmt.Fprintln(&b, "Project: "+terminalSafe(p.After.ID))
	fmt.Fprintln(&b, "Policy: "+terminalSafe(p.After.Mode))
	if p.Before != nil {
		fmt.Fprintln(&b, "Previous policy: "+p.Before.Mode)
	} else {
		fmt.Fprintln(&b, "Creates a new project and its policy atomically.")
	}
	if p.Unchanged {
		fmt.Fprintln(&b, "This binding and policy are unchanged; invitations and policy revision are preserved.")
	}
	if p.After.Paused {
		fmt.Fprintln(&b, "Enrollment remains PAUSED.")
	}
	for _, binding := range p.After.Bindings {
		state := ""
		if binding.Removed {
			state = " (removed; blocks broader fallback)"
		}
		fmt.Fprintln(&b, "  "+binding.Kind+state+": "+terminalSafe(binding.Root))
	}
	for _, client := range p.After.Clients {
		fmt.Fprintln(&b, "  "+client.Adapter+" project mapping: "+terminalSafe(client.ID))
	}
	if p.Before != nil && p.Before.Mode == "new-sessions" && p.After.Mode == "new-sessions" {
		fmt.Fprintln(&b, "Existing new-session cutoff is retained.")
	}
	fmt.Fprintf(&b, "Existing session records affected: %d\n", len(p.Affected))
	fmt.Fprintln(&b, "Reason: "+terminalSafe(p.Change.Reason))
	fmt.Fprintln(&b, "Review: "+p.Review)
	fmt.Fprintln(&b, "\n"+p.Effect)
	fmt.Fprintln(&b, "Discovery is separate from enrollment. Sessions still identify and acknowledge eligibility.")
	if p.After.Mode != "disabled" && !install.InspectInstructions(m.home).Ready {
		fmt.Fprintln(&b, "\nInstructions are NOT READY. Run `"+terminalSafe(installNextCommand(m.home, "install"))+"` before enabling adoption.")
	}
	return b.String()
}
func (m *rolloutOnboardModel) View() tea.View {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	muted := lipgloss.NewStyle().Faint(true)
	selected := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	var b strings.Builder
	fmt.Fprintln(&b, title.Render("ARM · Project onboarding"))
	if m.stage != onboardDone && (m.width < 40 || m.height < 20) {
		if m.stage == onboardApplying {
			return tea.NewView(b.String() + "Policy write in progress. Waiting for the result.\n")
		}
		return tea.NewView(b.String() + "Enlarge the terminal to at least 40×20. Esc cancels.\n")
	}
	fmt.Fprintln(&b, muted.Render("Directory → Scope → Name → Policy → Review"))
	fmt.Fprintln(&b)
	switch m.stage {
	case onboardLoading:
		fmt.Fprintln(&b, "Reading supported clients' project metadata…")
	case onboardProjects:
		fmt.Fprintln(&b, "Suggestions only. Client state and session markers do not prove running work.")
		if !m.readiness.Ready {
			fmt.Fprintln(&b, "Instructions not ready. Enable adoption after installing instructions; Disabled can still be saved.")
			fmt.Fprintln(&b, "Run: `"+terminalSafe(installNextCommand(m.home, "install"))+"`")
		}
		if len(m.catalog.Projects) == 0 {
			fmt.Fprintln(&b, "No client project directories found. You can choose a directory manually.")
		}
		warnings := 0
		for _, adapter := range m.catalog.Adapters {
			warnings += len(adapter.Warnings)
		}
		if warnings > 0 {
			fmt.Fprintf(&b, "%d metadata warning(s); --json includes adapter diagnostics.\n", warnings)
		}
		fmt.Fprintln(&b, m.projects.View())
		fmt.Fprintln(&b, "Enter chooses · / filters · Esc clears a filter or cancels · q cancels")
	case onboardDirectory:
		fmt.Fprintln(&b, "Enter the project directory")
		fmt.Fprintln(&b, m.input.View())
		fmt.Fprintln(&b, "Enter continues · Esc returns to suggestions")
	case onboardResolving:
		fmt.Fprintln(&b, "Inspecting the selected directory and Git checkout…")
	case onboardScope, onboardPolicy:
		heading := "Choose the project boundary"
		if m.stage == onboardPolicy {
			heading = "How should agents join this project?"
		}
		fmt.Fprintln(&b, title.Render(heading))
		visible := max(1, (m.height-11)/2)
		start := max(0, m.cursor-visible+1)
		end := min(len(m.choices), start+visible)
		for i := start; i < end; i++ {
			choice := m.choices[i]
			line := "  " + choice.title
			if i == m.cursor {
				line = selected.Render("> " + choice.title)
			}
			fmt.Fprintln(&b, line)
			fmt.Fprintln(&b, "    "+ansi.Truncate(terminalSafe(choice.detail), max(10, m.width-9), "…"))
		}
		fmt.Fprintf(&b, "Choice %d of %d\n", m.cursor+1, len(m.choices))
		fmt.Fprintln(&b, muted.Render("↑/↓ choose · Enter continues · Esc goes back"))
	case onboardName:
		fmt.Fprintln(&b, "Name this ARM project")
		fmt.Fprintln(&b, m.input.View())
		fmt.Fprintln(&b, "Lowercase letters, digits, hyphens or underscores. Enter continues · Esc goes back")
	case onboardPreparing:
		fmt.Fprintln(&b, "Preparing an exact read-only policy preview…")
	case onboardReview:
		fmt.Fprintln(&b, title.Render("Review the selected scope and policy"))
		fmt.Fprintln(&b, m.review.View())
		fmt.Fprintln(&b, "↑/↓ scroll · b back · Apply these changes? [y/N]")
	case onboardApplying:
		fmt.Fprintln(&b, "Applying the reviewed local policy…")
	case onboardDone:
		if m.cancelled {
			fmt.Fprintln(&b, "Cancelled. No onboarding policy was applied.")
		} else if m.err != nil {
			fmt.Fprintln(&b, terminalSafe(m.err.Error()))
		} else {
			if m.plan.Unchanged {
				fmt.Fprintln(&b, "Project "+terminalSafe(m.plan.After.ID)+" is already configured; no changes were made.")
			} else {
				fmt.Fprintln(&b, "Project "+terminalSafe(m.plan.After.ID)+" configured: "+m.plan.After.Mode)
			}
			fmt.Fprintln(&b, "Sessions still identify and acknowledge participation. Existing jobs and leases are unchanged.")
		}
		fmt.Fprintln(&b, "Next: `"+terminalSafe(installNextCommand(m.home, "rollout status"))+"`")
	}
	if m.notice != "" {
		fmt.Fprintln(&b, "\n"+terminalSafe(m.notice))
	}
	return tea.NewView(lipgloss.NewStyle().Padding(1, 1).Width(max(20, m.width-2)).Render(b.String()))
}
