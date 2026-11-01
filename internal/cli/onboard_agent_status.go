package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/agentstatus"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

type onboardStatusPreviewMsg struct {
	generation int
	plan       agentstatus.OnboardingPlan
	err        error
}
type onboardStatusAppliedMsg struct {
	generation int
	record     agentstatus.OnboardingRecord
	err        error
}
type onboardStatusSetup struct {
	change  agentstatus.OnboardingChange
	plan    agentstatus.OnboardingPlan
	skipped bool
	mu      sync.Mutex
	closed  bool
	wg      sync.WaitGroup
	outcome *onboardStatusAppliedMsg
	preview func(context.Context, agentstatus.OnboardingChange) (agentstatus.OnboardingPlan, error)
	apply   func(context.Context, agentstatus.OnboardingChange, string) (agentstatus.OnboardingRecord, error)
}

func (s *onboardStatusSetup) task(fn func() tea.Msg) tea.Cmd {
	return func() tea.Msg {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil
		}
		s.wg.Add(1)
		s.mu.Unlock()
		defer s.wg.Done()
		return fn()
	}
}
func (s *onboardStatusSetup) join() { s.mu.Lock(); s.closed = true; s.mu.Unlock(); s.wg.Wait() }
func (s *onboardStatusSetup) last() *onboardStatusAppliedMsg {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outcome == nil {
		return nil
	}
	v := *s.outcome
	return &v
}

func (m *projectOnboardModel) offerStatusSetup() {
	ids := map[string]bool{}
	for _, p := range m.plan.Projects {
		ids[p.After.ID] = true
	}
	selected := make([]string, 0, len(ids))
	for id := range ids {
		selected = append(selected, id)
	}
	sort.Strings(selected)
	store := agentstatus.OnboardingStore{Home: m.home}
	reader := statusPolicyReader(m.home)
	m.statusSetup = &onboardStatusSetup{change: agentstatus.OnboardingChange{Version: 1, Key: local.ID(), Projects: selected, Setup: true}, preview: func(ctx context.Context, c agentstatus.OnboardingChange) (agentstatus.OnboardingPlan, error) {
		return store.Plan(ctx, reader, c)
	}, apply: func(ctx context.Context, c agentstatus.OnboardingChange, review string) (agentstatus.OnboardingRecord, error) {
		return store.Apply(ctx, reader, c, review)
	}}
	m.stage = pickerStatusOffer
}
func (m *projectOnboardModel) afterStatusSetup() tea.Cmd {
	m.cancelOperation()
	if len(m.selection.Checked) > 0 {
		m.stage = pickerOffer
		return nil
	}
	return m.finish(false)
}
func (m *projectOnboardModel) statusSetupResult(terminal error) error {
	if m.statusSetup == nil {
		return nil
	}
	m.cancelOperation()
	m.statusSetup.join()
	outcome := m.statusSetup.last()
	if outcome == nil {
		return nil
	}
	if outcome.err != nil {
		return statusSetupError(m.home, m.statusSetup.change.Key, outcome.err)
	}
	if (terminal != nil || m.ctx.Err() != nil) && outcome.record.Key != "" {
		return model.Err("status_onboarding_interrupted", fmt.Sprintf("Project policy was saved; status setup recorded %s under original key %s. Inspect: %s", outcome.record.State, outcome.record.Key, statusSetupRecovery(m.home, outcome.record.Key)))
	}
	return nil
}
func (m *projectOnboardModel) statusSetupSummary() string {
	if !m.applied || m.statusSetup == nil {
		return ""
	}
	s := m.statusSetup
	if outcome := s.last(); outcome != nil {
		state := outcome.record.State
		if state == "" {
			state = "not confirmed"
		}
		return fmt.Sprintf("Project policy saved. Status setup: %s. Original key: %s. Inspect: %s", state, s.change.Key, statusSetupRecovery(m.home, s.change.Key))
	}
	if s.skipped {
		return "Project policy saved. Status setup skipped; no provider or agent was enabled."
	}
	return "Project policy saved. Status setup was not applied."
}
func (m *projectOnboardModel) updateStatusSetup(message tea.Msg) (bool, tea.Cmd) {
	s := m.statusSetup
	switch msg := message.(type) {
	case onboardStatusPreviewMsg:
		if s == nil || m.stage != pickerStatusPreparing || msg.generation != m.generation {
			return true, nil
		}
		m.cancelOperation()
		if msg.err != nil {
			m.stage = pickerStatusOffer
			m.notice = "Project policy saved; status setup preview unavailable: " + msg.err.Error()
			return true, nil
		}
		s.plan = msg.plan
		m.stage = pickerStatusReview
		raw, _ := json.MarshalIndent(msg.plan, "", "  ")
		lines := strings.Split(string(raw), "\n")
		for i := range lines {
			lines[i] = terminalSafe(lines[i])
		}
		m.setReview("Separate status setup review. These are actual saved policy IDs, revisions and complete boundary snapshots. Provider configuration, paid calls, native activation and agent enrollment remain separate.\n\n" + strings.Join(lines, "\n"))
		return true, nil
	case onboardStatusAppliedMsg:
		if s == nil || m.stage != pickerStatusApplying || msg.generation != m.generation {
			return true, nil
		}
		s.mu.Lock()
		s.outcome = &msg
		s.mu.Unlock()
		m.cancelOperation()
		if msg.err != nil {
			m.err = statusSetupError(m.home, s.change.Key, msg.err)
			return true, m.finish(false)
		}
		m.notice = m.statusSetupSummary()
		return true, m.afterStatusSetup()
	}
	if s == nil || m.stage != pickerStatusOffer && m.stage != pickerStatusPreparing && m.stage != pickerStatusReview && m.stage != pickerStatusApplying {
		return false, nil
	}
	key, ok := message.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	value := key.String()
	if m.stage == pickerStatusApplying {
		if value == "esc" || value == "q" || value == "ctrl+c" {
			m.notice = "Status publication owns its original outcome. Policy remains saved; inspect the original status key if interrupted."
		}
		return true, nil
	}
	if value == "ctrl+c" {
		s.skipped = true
		return true, m.finish(false)
	}
	if m.width < 40 || m.height < 20 {
		if value == "esc" {
			s.skipped = true
			return true, m.afterStatusSetup()
		}
		return true, nil
	}
	if m.stage == pickerStatusOffer {
		if value == "y" || value == "Y" {
			ctx, generation := m.operation()
			m.stage = pickerStatusPreparing
			m.notice = ""
			return true, s.task(func() tea.Msg {
				plan, err := s.preview(ctx, s.change)
				return onboardStatusPreviewMsg{generation, plan, err}
			})
		}
		if value == "enter" || value == "n" || value == "N" || value == "esc" || value == "q" {
			s.skipped = true
			return true, m.afterStatusSetup()
		}
		return true, nil
	}
	if m.stage == pickerStatusPreparing {
		if value == "esc" || value == "q" {
			m.cancelOperation()
			m.generation++
			s.skipped = true
			return true, m.afterStatusSetup()
		}
		return true, nil
	}
	if m.stage == pickerStatusReview {
		if value == "y" || value == "Y" {
			ctx, generation := m.operation()
			m.stage = pickerStatusApplying
			plan := s.plan
			return true, s.task(func() tea.Msg {
				var record agentstatus.OnboardingRecord
				err := ctx.Err()
				if err == nil {
					record, err = s.apply(ctx, plan.Change, plan.Review)
				}
				outcome := onboardStatusAppliedMsg{generation, record, err}
				s.mu.Lock()
				s.outcome = &outcome
				s.mu.Unlock()
				return outcome
			})
		}
		if value == "b" {
			m.stage = pickerStatusOffer
			return true, nil
		}
		if value == "enter" || value == "n" || value == "N" || value == "esc" || value == "q" {
			s.skipped = true
			return true, m.afterStatusSetup()
		}
	}
	return false, nil
}
func (m *projectOnboardModel) statusSetupView() string {
	var b strings.Builder
	switch m.stage {
	case pickerStatusOffer:
		fmt.Fprintf(&b, "Project policy saved. Set up agent status pages for %d saved projects? [y/N]\n", len(m.statusSetup.change.Projects))
		fmt.Fprintln(&b, "Separate local setup preferences; no agent requests or model calls. Provider configuration stays separate.")
	case pickerStatusPreparing:
		fmt.Fprintln(&b, "Reading actual saved policy and preparing the separate status review…")
	case pickerStatusReview:
		fmt.Fprintln(&b, m.review.View())
		fmt.Fprintln(&b, "↑/↓ details · b back · Save status setup? [y/N]")
	case pickerStatusApplying:
		fmt.Fprintln(&b, "Saving separate status preferences. Project policy is already saved. Waiting for the original result…")
	}
	return b.String()
}
