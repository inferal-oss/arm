package cli

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"getarm.org/internal/activation"
	"getarm.org/internal/agentsessions"
	"getarm.org/internal/rollout"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
)

// A saved reference is only a selector. Each plan must establish the current
// native generation in this exact home/directory and the saved ARM project.
func (a *app) checkedActivationPlan(ctx context.Context, checked pickerChecked, project string) (activation.Plan, error) {
	if err := ctx.Err(); err != nil {
		return activation.Plan{}, err
	}
	s := checked.Session
	if !s.Valid() || !pickerValidID(project) {
		return activation.Plan{}, fmt.Errorf("invalid checked chat or project")
	}
	if s.Ref.Adapter != "codex" {
		return activation.Plan{}, fmt.Errorf("active Claude delivery is unsupported; open the chat and use the manual instructions")
	}
	d, err := (rollout.Store{Home: a.home}).CheckContext(ctx, rollout.Query{CWD: s.Directory, Adapter: s.Ref.Adapter, Session: s.Ref.ID})
	if err != nil {
		return activation.Plan{}, err
	}
	if d.Project == nil || d.Project.ID != project {
		return activation.Plan{}, fmt.Errorf("checked chat no longer resolves to the reviewed project")
	}
	if !d.Eligible {
		return activation.Plan{}, fmt.Errorf("not eligible: %s. Open the chat to self-identify if permitted, then explicitly review an invitation if required; no policy was changed", d.Why)
	}
	owned := *a
	owned.ctx = ctx
	sources, err := owned.sessionSources("codex", s.Ref.Home, "", s.Directory)
	if err != nil {
		return activation.Plan{}, err
	}
	for index, source := range sources {
		codex, ok := source.(agentsessions.Codex)
		if !ok {
			return activation.Plan{}, fmt.Errorf("unsupported checked native source")
		}
		codex.IncludeAppServer = true
		sources[index] = codex
	}
	p, err := owned.activationPlan(project, "codex", s.Ref.ID, sources, true)
	if err != nil {
		return activation.Plan{}, err
	}
	if p.Session.Adapter != s.Ref.Adapter || p.Session.ID != s.Ref.ID || p.Session.Home != s.Ref.Home || p.Session.CWD != s.Directory || p.Project != project {
		return activation.Plan{}, fmt.Errorf("checked chat home, directory or identity changed; refresh and review again")
	}
	if p.PolicyRevision != d.Project.Revision {
		return activation.Plan{}, fmt.Errorf("policy changed during verification; review again")
	}
	if d.Session != nil && !d.Session.Started.Equal(p.Session.Started) {
		return activation.Plan{}, fmt.Errorf("native original start differs from the immutable ARM observation; verify the chat identity manually")
	}
	return p, nil
}

type checkedActivationEntry struct {
	checked             pickerChecked
	project             string
	plan                activation.Plan
	key                 string
	record              activation.Record
	err                 error
	verified, attempted bool
}
type checkedActivationPrepared struct {
	index int
	plan  activation.Plan
	err   error
}
type checkedActivationSent struct {
	index  int
	record activation.Record
	err    error
}
type checkedActivationStage int

const (
	checkedVerifying checkedActivationStage = iota
	checkedReview
	checkedSending
	checkedResults
)

type checkedActivationModel struct {
	a                           *app
	selection                   pickerSelection
	entries                     []checkedActivationEntry
	stage                       checkedActivationStage
	width, height, cursor, next int
	view                        viewport.Model
	content, notice             string
	cancel                      context.CancelFunc
	verifyCtx, sendCtx          context.Context
	verifyCancel, sendCancel    context.CancelFunc
	stopAfterCurrent            bool
	finished                    bool
	// Fixture seams are local to this model. Production uses the existing
	// exact-recipient plan and durable activation ledger, never a saved ID alone.
	plan  func(context.Context, pickerChecked, string) (activation.Plan, error)
	send  func(context.Context, activation.Plan, string, pickerChecked) (activation.Record, error)
	tasks struct {
		sync.Mutex
		closed   bool
		wg       sync.WaitGroup
		outcomes map[int]checkedActivationSent
	}
}

func newCheckedActivation(a *app, selection pickerSelection) (*checkedActivationModel, error) {
	selection = clonePickerSelection(selection)
	if len(selection.Checked) == 0 || len(selection.Checked) > agentsessions.Limit {
		return nil, fmt.Errorf("a bounded checked-chat snapshot is required")
	}
	projects := map[string]string{}
	for _, setting := range selection.Settings {
		if projects[setting.Group] != "" || !pickerValidID(setting.Project) {
			return nil, fmt.Errorf("ambiguous checked project settings")
		}
		projects[setting.Group] = setting.Project
	}
	wanted := map[agentsessions.SavedSessionRef]pickerChecked{}
	for _, group := range selection.Groups {
		for _, session := range group.Sessions {
			if _, duplicate := wanted[session.Ref]; duplicate {
				return nil, fmt.Errorf("ambiguous checked chat reference")
			}
			wanted[session.Ref] = pickerChecked{Group: group.Key, Session: session}
		}
	}
	ctx, cancel := context.WithCancel(a.ctx)
	owned := *a
	owned.ctx = ctx
	v := viewport.New(viewport.WithWidth(72), viewport.WithHeight(14))
	v.SoftWrap = true
	m := &checkedActivationModel{a: &owned, selection: selection, width: 80, height: 30, view: v, cancel: cancel}
	m.verifyCtx, m.verifyCancel = context.WithTimeout(ctx, 2*time.Minute)
	m.tasks.outcomes = map[int]checkedActivationSent{}
	seen := map[agentsessions.SavedSessionRef]bool{}
	for _, checked := range selection.Checked {
		if !checked.Session.Valid() || projects[checked.Group] == "" || wanted[checked.Session.Ref] != checked || seen[checked.Session.Ref] {
			m.verifyCancel()
			cancel()
			return nil, fmt.Errorf("checked chat context changed; refresh the picker")
		}
		seen[checked.Session.Ref] = true
		m.entries = append(m.entries, checkedActivationEntry{checked: checked, project: projects[checked.Group]})
	}
	m.plan = owned.checkedActivationPlan
	m.send = func(ctx context.Context, p activation.Plan, key string, checked pickerChecked) (activation.Record, error) {
		return (activation.Store{Home: owned.home}).Send(ctx, p, key, func() (activation.Plan, error) {
			return owned.checkedActivationPlan(ctx, checked, p.Project)
		})
	}
	return m, nil
}

func (m *checkedActivationModel) task(fn func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		m.tasks.Lock()
		if m.tasks.closed {
			m.tasks.Unlock()
			return nil
		}
		m.tasks.wg.Add(1)
		m.tasks.Unlock()
		defer m.tasks.wg.Done()
		return fn()
	}
}
func (m *checkedActivationModel) close() {
	m.finished = true
	m.tasks.Lock()
	m.tasks.closed = true
	m.tasks.Unlock()
	m.cancel()
	m.verifyCancel()
	if m.sendCancel != nil {
		m.sendCancel()
	}
	m.tasks.wg.Wait()
	// A terminal/context failure may prevent Update from seeing the receipt.
	// Join the original task and preserve its actual outcome before reporting.
	m.tasks.Lock()
	defer m.tasks.Unlock()
	for index, outcome := range m.tasks.outcomes {
		m.entries[index].record, m.entries[index].err = outcome.record, outcome.err
	}
}
func (m *checkedActivationModel) Init() tea.Cmd { return m.prepare(0) }
func (m *checkedActivationModel) finish() tea.Cmd {
	m.finished = true
	m.tasks.Lock()
	m.tasks.closed = true
	m.tasks.Unlock()
	m.cancel()
	return tea.Quit
}
func (m *checkedActivationModel) prepare(index int) tea.Cmd {
	if m.finished {
		return nil
	}
	if err := m.verifyCtx.Err(); err != nil {
		for remaining := index; remaining < len(m.entries); remaining++ {
			m.entries[remaining].err = fmt.Errorf("verification stopped: %w", err)
		}
		index = len(m.entries)
	}
	m.next = index
	if index == len(m.entries) {
		m.verifyCancel()
		m.stage = checkedReview
		m.setContent(m.reviewEntry())
		return nil
	}
	entry, plan, parent := m.entries[index], m.plan, m.verifyCtx
	return m.task(func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		p, err := plan(ctx, entry.checked, entry.project)
		return checkedActivationPrepared{index, p, err}
	})
}
func (m *checkedActivationModel) ready() int {
	n := 0
	for _, entry := range m.entries {
		if entry.verified && entry.err == nil {
			n++
		}
	}
	return n
}
func (m *checkedActivationModel) sendNext(index int) tea.Cmd {
	if m.finished {
		return nil
	}
	if m.sendCtx.Err() != nil {
		m.stopAfterCurrent = true
		m.notice = "Activation batch deadline/cancellation reached; remaining chats were not sent."
	}
	for index < len(m.entries) && !m.entries[index].verified {
		index++
	}
	if m.stopAfterCurrent || index == len(m.entries) {
		m.sendCancel()
		m.stage = checkedResults
		m.setContent(m.results())
		return nil
	}
	m.next = index
	m.entries[index].attempted = true
	entry, send, parent := m.entries[index], m.send, m.sendCtx
	return m.task(func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 40*time.Second)
		defer cancel()
		r, err := send(ctx, entry.plan, entry.key, entry.checked)
		outcome := checkedActivationSent{index, r, err}
		m.tasks.Lock()
		m.tasks.outcomes[index] = outcome
		m.tasks.Unlock()
		return outcome
	})
}
func (m *checkedActivationModel) setContent(s string) {
	m.content = s
	m.view.SetContent(ansi.Wrap(s, max(20, m.width-8), ""))
	m.view.GotoTop()
}
func (m *checkedActivationModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if m.finished {
		return m, nil
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.view.SetWidth(max(20, msg.Width-8))
		m.view.SetHeight(max(5, msg.Height-12))
		m.view.SetContent(ansi.Wrap(m.content, m.view.Width(), ""))
		return m, nil
	case checkedActivationPrepared:
		if m.stage != checkedVerifying || msg.index != m.next {
			return m, nil
		}
		entry := &m.entries[msg.index]
		entry.plan, entry.err = msg.plan, msg.err
		entry.verified = msg.err == nil && msg.plan.Delivery == "queue"
		if entry.verified {
			entry.key = uuid.NewString()
		}
		return m, m.prepare(msg.index + 1)
	case checkedActivationSent:
		if m.stage != checkedSending || msg.index != m.next {
			return m, nil
		}
		m.entries[msg.index].record, m.entries[msg.index].err = msg.record, msg.err
		// An uncertain outcome stops the batch. Never retry or mint a new key.
		if msg.record.State == "pending" || msg.record.State == "unknown" || m.a.ctx.Err() != nil {
			m.stopAfterCurrent = true
		}
		return m, m.sendNext(msg.index + 1)
	case tea.KeyPressMsg:
		key := msg.String()
		if m.stage == checkedSending {
			if key == "esc" || key == "q" || key == "ctrl+c" {
				m.stopAfterCurrent = true
				m.notice = "Stopping after the current receipt. Remaining chats will not be sent."
			}
			return m, nil
		}
		if key == "esc" || key == "q" || key == "ctrl+c" {
			return m, m.finish()
		}
		if m.width < 40 || m.height < 20 {
			return m, nil
		}
		if m.stage == checkedReview {
			switch key {
			case "left", "right":
				if key == "left" {
					m.cursor = max(0, m.cursor-1)
				} else {
					m.cursor = min(len(m.entries)-1, m.cursor+1)
				}
				m.setContent(m.reviewEntry())
				return m, nil
			case "y", "Y":
				if m.ready() == 0 {
					return m, nil
				}
				m.stage = checkedSending
				m.sendCtx, m.sendCancel = context.WithTimeout(m.a.ctx, 5*time.Minute)
				return m, m.sendNext(0)
			case "enter", "n", "N":
				return m, m.finish()
			}
		}
	}
	var cmd tea.Cmd
	if m.stage == checkedReview || m.stage == checkedResults {
		m.view, cmd = m.view.Update(message)
	}
	return m, cmd
}

func (m *checkedActivationModel) reviewEntry() string {
	entry := m.entries[m.cursor]
	var b strings.Builder
	fmt.Fprintf(&b, "Checked chat %d of %d · %s\nProject: %s\n", m.cursor+1, len(m.entries), terminalSafe(entry.checked.Session.Name), terminalSafe(entry.project))
	if !entry.verified {
		reason := "current exact-session queue delivery is unsupported"
		if entry.err != nil {
			reason = entry.err.Error()
		}
		fmt.Fprintf(&b, "BLOCKED: %s\n\n", terminalSafe(reason))
		guide := &projectOnboardModel{home: m.a.home, selection: m.selection}
		b.WriteString(guide.activationGuide(m.cursor))
		return b.String()
	}
	p := entry.plan
	fmt.Fprintf(&b, "Recipient: %s · %s\nDirectory: %s\nClient home: %s\nNative generation: %s\nPolicy revision: %d\nNative CLI: %s\nExecutable SHA256: %s\nDelivery key: %s\nReview: %s\n\nExact message:\n%s", p.Session.Adapter, p.Session.ID, terminalSafe(p.Session.CWD), terminalSafe(p.Session.Home), p.Session.Generation, p.PolicyRevision, terminalSafe(p.Binary), p.BinarySHA, entry.key, p.Review, activationDisplayMessage(p.Message))
	return b.String()
}
func (m *checkedActivationModel) results() string {
	var b strings.Builder
	for _, entry := range m.entries {
		state := "not sent"
		if entry.record.State != "" {
			state = entry.record.State
		} else if entry.attempted && entry.err != nil {
			state = "refused before queue"
		} else if entry.attempted {
			state = "outcome unavailable; inspect original key"
		} else if entry.err != nil {
			state = "blocked"
		}
		fmt.Fprintf(&b, "%s · %s · %s\n", terminalSafe(entry.checked.Session.Name), entry.checked.Session.Ref.ID, state)
		if entry.err != nil {
			fmt.Fprintf(&b, "  %s\n", terminalSafe(entry.err.Error()))
		}
		if entry.attempted {
			fmt.Fprintf(&b, "  Inspect: %s\n", terminalSafe(activationLookupCommand(m.a.home, entry.key)))
		}
	}
	fmt.Fprintln(&b, "\nQueued means an activation message was queued, not that the chat has enrolled. Inspect rollout status for acknowledgment. Unchecked chats received nothing. Pending/unknown outcomes must not be retried with a new key.")
	return b.String()
}
func (m *checkedActivationModel) result(err error) error {
	for _, entry := range m.entries {
		if entry.attempted && (entry.record.State == "pending" || entry.record.State == "unknown" || err != nil && entry.record.Key == "" && entry.err == nil) {
			cause := entry.err
			if cause == nil {
				cause = err
			}
			return m.a.activationUnknown(entry.key, cause)
		}
	}
	if err == nil {
		for _, entry := range m.entries {
			if entry.attempted && entry.err != nil {
				return entry.err
			}
		}
	}
	return err
}
func (m *checkedActivationModel) View() tea.View {
	var b strings.Builder
	fmt.Fprintln(&b, "ARM · Activate checked chats")
	if m.width < 40 || m.height < 20 {
		fmt.Fprintln(&b, "Enlarge terminal to 40×20. Esc stops remaining work.")
		if m.stage == checkedSending {
			fmt.Fprintln(&b, "Inspect: "+terminalSafe(activationLookupCommand(m.a.home, m.entries[m.next].key)))
		}
	} else {
		switch m.stage {
		case checkedVerifying:
			fmt.Fprintf(&b, "Verifying checked chat %d of %d…\nNo messages have been sent. Esc cancels.\n", m.next+1, len(m.entries))
			fmt.Fprintln(&b, "Verification budget: 2 minutes total, up to 20 seconds per chat.")
		case checkedReview:
			fmt.Fprintf(&b, "%d verified recipients · %d blocked\n", m.ready(), len(m.entries)-m.ready())
			fmt.Fprintln(&b, m.view.View())
			fmt.Fprintln(&b, "←/→ checked chat · ↑/↓ scroll")
			fmt.Fprintln(&b, "Queueing requests activation; each agent must identify and acknowledge. New-sessions/Observe may require a separate invitation.")
			if m.ready() > 0 {
				fmt.Fprintf(&b, "Queue reviewed messages to %d verified checked chats? [y/N]\n", m.ready())
			} else {
				fmt.Fprintln(&b, "No supported recipients. Esc closes; manual instructions are above.")
			}
		case checkedSending:
			fmt.Fprintf(&b, "Queueing checked chat %d of %d. Waiting for its receipt…\n", m.next+1, len(m.entries))
			fmt.Fprintln(&b, "Inspect: "+terminalSafe(activationLookupCommand(m.a.home, m.entries[m.next].key)))
			fmt.Fprintln(&b, "Esc stops after the current receipt. No automatic retries.")
			fmt.Fprintln(&b, "Delivery budget: 5 minutes total, up to 40 seconds per chat.")
		case checkedResults:
			fmt.Fprintln(&b, m.view.View())
			fmt.Fprintln(&b, "↑/↓ scroll · Esc done")
		}
	}
	if m.notice != "" {
		fmt.Fprintln(&b, terminalSafe(m.notice))
	}
	return tea.NewView(lipgloss.NewStyle().Padding(1, 1).Width(max(20, m.width-2)).Render(b.String()))
}
func (a *app) activateCheckedSessions(selection pickerSelection) error {
	m, err := newCheckedActivation(a, selection)
	if err != nil {
		return err
	}
	opts := []tea.ProgramOption{tea.WithContext(a.ctx), tea.WithInput(a.in), tea.WithOutput(a.out), tea.WithoutSignalHandler()}
	if !a.colors {
		opts = append(opts, tea.WithColorProfile(colorprofile.ASCII))
	}
	_, err = tea.NewProgram(m, opts...).Run()
	m.close()
	// Keep receipts/recovery commands visible after leaving the full-screen UI,
	// including when cancellation prevented its last result message rendering.
	fmt.Fprint(a.out, m.results())
	return m.result(err)
}
