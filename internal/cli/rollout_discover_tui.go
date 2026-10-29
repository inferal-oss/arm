package cli

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"getarm.org/internal/activation"
	"getarm.org/internal/agentsessions"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
)

type discoveryStage int

const (
	discoveryLoading discoveryStage = iota
	discoverySelecting
	discoveryPreparing
	discoveryMessage
	discoveryReview
	discoverySending
	discoveryDone
)

type discoveryDataMsg struct {
	data rolloutDiscovery
	err  error
}
type discoveryPlanMsg struct {
	plan activation.Plan
	send bool
	err  error
}
type discoverySentMsg struct {
	record activation.Record
	err    error
}
type discoveryItem struct{ candidate rolloutCandidate }

func (i discoveryItem) Title() string {
	return label(i.candidate.Adapter) + " · " + shortSession(i.candidate.ID) + " · ARM: " + label(i.candidate.ARMState)
}
func (i discoveryItem) Description() string {
	return terminalSafe(i.candidate.CWD) + " · native " + terminalSafe(i.candidate.Activity) + " (unverified)"
}
func (i discoveryItem) FilterValue() string {
	return i.Title() + " " + i.Description() + " " + i.candidate.ID
}

type rolloutDiscoverModel struct {
	a                    *app
	project              string
	sources              []agentsessions.Source
	stage                discoveryStage
	width, height        int
	picker               list.Model
	view                 viewport.Model
	content, notice, key string
	data                 rolloutDiscovery
	selected             rolloutCandidate
	plan                 activation.Plan
	record               activation.Record
	err                  error
	sendStarted          bool
	copy                 func(string) tea.Cmd
	cancel               context.CancelFunc
	tasks                struct {
		mu     sync.Mutex
		closed bool
		wg     sync.WaitGroup
	}
}

func newRolloutDiscover(a *app, project string, sources []agentsessions.Source) *rolloutDiscoverModel {
	ctx, cancel := context.WithCancel(a.ctx)
	owned := *a
	owned.ctx = ctx
	picker := list.New(nil, list.NewDefaultDelegate(), 76, 12)
	picker.Title = "Select an existing session"
	picker.DisableQuitKeybindings()
	v := viewport.New(viewport.WithWidth(72), viewport.WithHeight(14))
	v.SoftWrap = true
	return &rolloutDiscoverModel{a: &owned, project: project, sources: sources, stage: discoveryLoading, width: 80, height: 30, picker: picker, view: v, copy: tea.SetClipboard, cancel: cancel}
}
func (m *rolloutDiscoverModel) task(fn func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		m.tasks.mu.Lock()
		if m.tasks.closed {
			m.tasks.mu.Unlock()
			return nil
		}
		m.tasks.wg.Add(1)
		m.tasks.mu.Unlock()
		defer m.tasks.wg.Done()
		return fn()
	}
}
func (m *rolloutDiscoverModel) close() {
	m.tasks.mu.Lock()
	m.tasks.closed = true
	m.tasks.mu.Unlock()
	m.cancel()
	m.tasks.wg.Wait()
}
func (m *rolloutDiscoverModel) Init() tea.Cmd {
	return m.task(func() tea.Msg { d, e := m.a.discoverSessions(m.project, m.sources); return discoveryDataMsg{d, e} })
}
func (m *rolloutDiscoverModel) setContent(s string) {
	m.content = s
	m.view.SetContent(ansi.Wrap(s, max(20, m.width-8), ""))
	m.view.GotoTop()
}
func activationDisplayMessage(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = terminalSafe(lines[i])
	}
	return strings.Join(lines, "\n")
}
func (m *rolloutDiscoverModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.stage == discoveryDone {
		return m, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.picker.SetSize(max(20, msg.Width-4), max(5, msg.Height-18))
		m.view.SetWidth(max(20, msg.Width-8))
		m.view.SetHeight(max(5, msg.Height-12))
		m.view.SetContent(ansi.Wrap(m.content, m.view.Width(), ""))
		return m, nil
	case discoveryDataMsg:
		if msg.err != nil {
			m.err = msg.err
			m.stage = discoveryDone
			return m, tea.Quit
		}
		m.data = msg.data
		items := []list.Item{}
		for _, c := range msg.data.Sessions {
			items = append(items, discoveryItem{c})
		}
		m.stage = discoverySelecting
		return m, m.picker.SetItems(items)
	case discoveryPlanMsg:
		if m.stage != discoveryPreparing {
			return m, nil
		}
		if msg.err != nil {
			m.notice = msg.err.Error()
			m.stage = discoverySelecting
			return m, nil
		}
		m.plan = msg.plan
		if msg.send {
			m.key = uuid.NewString()
			m.stage = discoveryReview
			m.setContent(fmt.Sprintf("Project: %s · policy revision %d\nRecipient: %s · %s\nDirectory: %s\nNative CLI: %s\nExecutable SHA256: %s\nDelivery key: %s\nReview: %s\n\nThis queues a follow-up turn in the existing session. Queued does not mean identified or enrolled. No ARM resources are granted.\n\nExact message:\n%s", m.plan.Project, m.plan.PolicyRevision, m.plan.Session.Adapter, m.plan.Session.ID, terminalSafe(m.plan.Session.CWD), terminalSafe(m.plan.Binary), m.plan.BinarySHA, m.key, m.plan.Review, activationDisplayMessage(m.plan.Message)))
		} else {
			m.stage = discoveryMessage
			m.setContent(activationDisplayMessage(m.plan.Message))
		}
		return m, nil
	case discoverySentMsg:
		m.record = msg.record
		m.notice = ""
		m.stage = discoveryDone
		if msg.err != nil {
			if msg.record.State == "unknown" || msg.record.State == "pending" || m.a.ctx.Err() != nil && msg.record.Key != "" {
				m.err = m.a.activationUnknown(m.key, msg.err)
			} else {
				m.err = msg.err
			}
		}
		return m, tea.Quit
	case tea.KeyPressMsg:
		key := msg.String()
		if m.stage == discoverySending {
			m.notice = "Delivery is in progress. Inspect the original key if interrupted."
			return m, nil
		}
		if key == "ctrl+c" {
			m.stage = discoveryDone
			return m, tea.Quit
		}
		if m.width < 40 || m.height < 20 {
			if key == "q" || key == "esc" {
				m.stage = discoveryDone
				return m, tea.Quit
			}
			return m, nil
		}
		if m.stage == discoverySelecting {
			if m.picker.FilterState() != list.Filtering {
				if key == "q" || key == "esc" && m.picker.FilterState() == list.Unfiltered {
					m.stage = discoveryDone
					return m, tea.Quit
				}
				if key == "enter" {
					if i, ok := m.picker.SelectedItem().(discoveryItem); ok {
						m.selected = i.candidate
						return m, m.prepare(false)
					}
				}
				if key == "r" {
					m.stage = discoveryLoading
					m.notice = ""
					return m, m.Init()
				}
			}
		} else if m.stage == discoveryMessage {
			switch key {
			case "c":
				m.notice = "Clipboard copy requested. If your terminal blocks it, select/copy the visible message. No agent message was sent."
				return m, m.copy(m.plan.Message)
			case "s":
				return m, m.prepare(true)
			case "b", "esc":
				m.stage = discoverySelecting
				m.notice = ""
				return m, nil
			case "q":
				m.stage = discoveryDone
				return m, tea.Quit
			}
		} else if m.stage == discoveryReview {
			switch key {
			case "y", "Y":
				m.stage = discoverySending
				m.sendStarted = true
				p, key := m.plan, m.key
				a, sources := m.a, m.sources
				return m, m.task(func() tea.Msg {
					r, e := (activation.Store{Home: a.home}).Send(a.ctx, p, key, func() (activation.Plan, error) {
						return a.activationPlan(p.Project, p.Session.Adapter, p.Session.ID, sources, true)
					})
					return discoverySentMsg{r, e}
				})
			case "b", "esc", "n", "N", "enter":
				m.stage = discoveryMessage
				m.setContent(activationDisplayMessage(m.plan.Message))
				m.notice = "No message sent."
				return m, nil
			case "q":
				m.stage = discoveryDone
				return m, tea.Quit
			}
		} else if key == "esc" || key == "q" {
			m.stage = discoveryDone
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	if m.stage == discoverySelecting {
		m.picker, cmd = m.picker.Update(message)
	} else if m.stage == discoveryMessage || m.stage == discoveryReview {
		m.view, cmd = m.view.Update(message)
	}
	return m, cmd
}
func (m *rolloutDiscoverModel) prepare(send bool) tea.Cmd {
	m.stage = discoveryPreparing
	m.notice = ""
	a, project, sources, selected := m.a, m.project, m.sources, m.selected
	return m.task(func() tea.Msg {
		p, e := a.activationPlan(project, selected.Adapter, selected.ID, sources, send)
		return discoveryPlanMsg{p, send, e}
	})
}
func (m *rolloutDiscoverModel) View() tea.View {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	var b strings.Builder
	fmt.Fprintln(&b, title.Render("ARM · Discover and activate sessions"))
	if m.stage != discoveryDone && (m.width < 40 || m.height < 20) {
		if m.stage == discoverySending {
			fmt.Fprintf(&b, "Delivery pending. Inspect original key %s if interrupted.\n", m.key)
		} else {
			fmt.Fprintln(&b, "Enlarge terminal to 40×20. Esc cancels without sending.")
		}
		return tea.NewView(b.String())
	}
	fmt.Fprintln(&b, "Project: "+terminalSafe(m.project))
	fmt.Fprintln(&b)
	switch m.stage {
	case discoveryLoading:
		fmt.Fprintln(&b, "Reading native session metadata. No activation messages are sent and no agents are enrolled.")
	case discoverySelecting:
		fmt.Fprintln(&b, "Native status is unverified. Discovery does not identify or enroll agents.")
		if len(m.data.Warnings) > 0 {
			fmt.Fprintf(&b, "%d adapter warning(s); --json includes details.\n", len(m.data.Warnings))
		}
		if len(m.data.Sessions) == 0 {
			fmt.Fprintln(&b, "No sessions found in this project. No messages were sent.")
		}
		fmt.Fprintln(&b, m.picker.View())
		fmt.Fprintln(&b, "Enter previews message · / filters · r refreshes · q closes")
	case discoveryPreparing:
		fmt.Fprintln(&b, "Preparing the exact recipient and message preview…")
	case discoveryMessage:
		fmt.Fprintln(&b, m.view.View())
		fmt.Fprintln(&b, "c Copy message · s Review Codex send · b Back · q Close")
	case discoveryReview:
		fmt.Fprintln(&b, m.view.View())
		fmt.Fprintln(&b, "↑/↓ scroll · b Back · Send this exact message? [y/N]")
	case discoverySending:
		fmt.Fprintln(&b, "Queueing the reviewed activation message. Waiting for the receipt…")
	case discoveryDone:
		if m.err != nil {
			fmt.Fprintln(&b, terminalSafe(m.err.Error()))
		} else if m.record.Key != "" {
			fmt.Fprintf(&b, "Delivery: %s · %s · %s\n", m.record.State, m.record.Adapter, m.record.Session)
			fmt.Fprintln(&b, "Queued is separate from identification and enrollment.")
		} else {
			fmt.Fprintln(&b, "Closed. No activation message was queued.")
		}
		if m.key != "" && m.sendStarted {
			fmt.Fprintln(&b, "Inspect: `"+terminalSafe(activationLookupCommand(m.a.home, m.key))+"`")
		}
		fmt.Fprintln(&b, "Next: `"+terminalSafe(installNextCommand(m.a.home, "rollout status"))+"`")
	}
	if m.notice != "" {
		fmt.Fprintln(&b, "\n"+terminalSafe(m.notice))
	}
	return tea.NewView(lipgloss.NewStyle().Padding(1, 1).Width(max(20, m.width-2)).Render(b.String()))
}
func (a *app) discoverInteractive(project string, sources []agentsessions.Source) error {
	m := newRolloutDiscover(a, project, sources)
	defer m.close()
	opts := []tea.ProgramOption{tea.WithContext(a.ctx), tea.WithInput(a.in), tea.WithOutput(a.out), tea.WithoutSignalHandler()}
	if !a.colors {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}
	_, e := tea.NewProgram(m, opts...).Run()
	if e != nil && m.sendStarted {
		return a.activationUnknown(m.key, e)
	}
	if e != nil {
		return e
	}
	return m.err
}
