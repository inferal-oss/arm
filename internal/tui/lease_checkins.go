package tui

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/yrashk/arm/internal/model"
)

const leaseCheckinCapability = "local-lease-reviews-v1"

type leaseCheckins struct {
	generation                                   uint64
	lease                                        model.Lease
	policy                                       model.LeaseReviewPolicy
	pending, history                             model.LeaseReviewPage
	loading, sending, finished, editing, confirm bool
	enabled                                      bool
	interval, contention, timeout                string
	note                                         string
	request                                      *model.LeaseReviewConfigure
}
type checkinsReadMsg struct {
	ui               *leaseCheckins
	session          transport
	generation       uint64
	lease            model.Lease
	policy           model.LeaseReviewPolicy
	pending, history model.LeaseReviewPage
	err              error
}
type checkinsResultMsg struct {
	ui      *leaseCheckins
	request model.LeaseReviewConfigure
	policy  model.LeaseReviewPolicy
	err     error
}

func hasLeaseCheckins(s model.Status) bool {
	for _, c := range s.Capabilities {
		if c == leaseCheckinCapability {
			return true
		}
	}
	return false
}
func (m *dashboardModel) openCheckins() tea.Cmd {
	if m.detail == nil || !m.connected || m.pending || m.checkinPending {
		return nil
	}
	if !hasLeaseCheckins(m.data.status) {
		m.notice = "Agent check-ins are unavailable on this backend."
		return nil
	}
	ui := &leaseCheckins{lease: *m.detail}
	m.checkins = ui
	m.viewport.GotoTop()
	return m.readCheckins(false)
}
func (m *dashboardModel) readCheckins(next bool) tea.Cmd {
	ui := m.checkins
	if ui == nil || ui.loading || ui.sending || m.checkinPending || !m.connected {
		return nil
	}
	ui.loading = true
	ui.generation++
	generation, session, ctx := ui.generation, m.session, m.ctx
	after := int64(0)
	if next {
		after = ui.history.Cursor
	}
	return func() tea.Msg {
		msg := checkinsReadMsg{ui: ui, session: session, generation: generation}
		msg.err = session.Call(ctx, "inspect", map[string]string{"id": ui.lease.ID}, &msg.lease)
		if msg.err == nil {
			msg.err = session.Call(ctx, "lease_review_policy", map[string]string{"lease": ui.lease.ID}, &msg.policy)
		}
		if msg.err == nil {
			msg.err = session.Call(ctx, "lease_review_list", model.LeaseReviewQuery{Lease: ui.lease.ID, Pending: true}, &msg.pending)
		}
		if msg.err == nil {
			msg.err = session.Call(ctx, "lease_review_list", model.LeaseReviewQuery{Lease: ui.lease.ID, After: after}, &msg.history)
		}
		return msg
	}
}
func (m *dashboardModel) acceptCheckins(msg checkinsReadMsg) tea.Cmd {
	if m.checkins != msg.ui || m.session != msg.session || msg.generation != msg.ui.generation {
		return nil
	}
	ui := msg.ui
	ui.loading = false
	if msg.err != nil {
		ui.note = safe(msg.err.Error())
		ui.finished = true
		return nil
	}
	ui.lease = msg.lease
	ui.policy = msg.policy
	ui.pending = msg.pending
	ui.history = msg.history
	ui.finished = false
	ui.editing = false
	ui.confirm = false
	ui.note = ""
	if msg.history.SchedulerError != "" {
		ui.note = "Check-in scheduling needs attention: " + safe(msg.history.SchedulerError) + ". Lease deadlines still apply."
	}
	m.viewport.GotoTop()
	return nil
}
func checkinDraft(ui *leaseCheckins) (model.LeaseReviewConfigure, error) {
	g := ui.policy.Generation
	q := model.LeaseReviewConfigure{Lease: ui.lease.ID, Revision: ui.lease.Revision, Generation: &g, Enabled: ui.enabled}
	if !q.Enabled {
		return q, nil
	}
	if ui.lease.State != "active" || ui.lease.Request.Agent == "" {
		return q, fmt.Errorf("check-ins require an active lease attributed to a registered agent")
	}
	var err error
	if q.Interval, err = parseTTL(ui.interval); err != nil {
		return q, err
	}
	if q.ReplyTimeout, err = parseTTL(ui.timeout); err != nil {
		return q, err
	}
	if strings.TrimSpace(ui.contention) != "0s" && strings.TrimSpace(ui.contention) != "" {
		if q.ContentionInterval, err = parseTTL(ui.contention); err != nil {
			return q, err
		}
	}
	if q.Interval < 15*time.Second || q.ReplyTimeout < 5*time.Second || q.ReplyTimeout > time.Hour || (q.ContentionInterval != 0 && (q.ContentionInterval < 15*time.Second || q.ContentionInterval > q.Interval)) {
		return q, fmt.Errorf("interval 15s–7d; reply window 5s–1h; contention 0s or 15s–regular interval")
	}
	return q, nil
}
func (m *dashboardModel) checkinKey(k string) tea.Cmd {
	ui := m.checkins
	if ui == nil {
		return nil
	}
	if k == "esc" || k == "n" {
		if !ui.sending {
			if ui.confirm {
				ui.confirm = false
				ui.request = nil
			} else if ui.editing {
				ui.editing = false
			} else {
				ui.generation++
				m.checkins = nil
			}
			m.viewport.GotoTop()
		}
		return nil
	}
	if ui.sending || m.checkinPending {
		return nil
	}
	if k == "r" {
		return m.readCheckins(false)
	}
	if ui.loading || ui.finished || !m.connected {
		return nil
	}
	if ui.confirm {
		if k == "y" {
			return m.sendCheckinSettings()
		}
		scrollReview(&m.viewport, k)
		return nil
	}
	if ui.editing {
		switch k {
		case " ", "space":
			ui.enabled = !ui.enabled
		case "1", "2", "3":
			m.inputMode = "checkin:" + k
			value := ui.interval
			if k == "2" {
				value = ui.contention
			}
			if k == "3" {
				value = ui.timeout
			}
			m.input.SetValue(value)
			return m.input.Focus()
		case "y":
			q, err := checkinDraft(ui)
			if err != nil {
				ui.note = safe(err.Error())
				return nil
			}
			ui.request = &q
			ui.confirm = true
			ui.note = ""
			m.viewport.GotoTop()
		}
		return nil
	}
	switch k {
	case "e":
		ui.editing = true
		ui.enabled = ui.policy.Enabled
		ui.interval = ui.policy.Interval.String()
		ui.contention = ui.policy.ContentionInterval.String()
		ui.timeout = ui.policy.ReplyTimeout.String()
		if ui.policy.Interval == 0 {
			ui.interval = "10m"
		}
		if ui.policy.ReplyTimeout == 0 {
			ui.timeout = "1m"
		}
		ui.note = ""
		m.viewport.GotoTop()
	case "p":
		if ui.history.More {
			return m.readCheckins(true)
		}
	case "i":
		m.record, _ = newRecord(struct {
			Policy           model.LeaseReviewPolicy
			Pending, History model.LeaseReviewPage
		}{ui.policy, ui.pending, ui.history}, "Agent check-ins")
		m.viewport.GotoTop()
	default:
		scrollReview(&m.viewport, k)
	}
	return nil
}
func (m *dashboardModel) sendCheckinSettings() tea.Cmd {
	ui := m.checkins
	if ui == nil || ui.request == nil || !ui.confirm || ui.sending || m.checkinPending || !m.connected || ui.finished {
		return nil
	}
	q := *ui.request
	generation := *q.Generation
	q.Generation = &generation
	ui.sending = true
	m.checkinPending = true
	session, ctx := m.session, m.ctx
	return func() tea.Msg {
		var p model.LeaseReviewPolicy
		err := session.Call(ctx, "lease_review_configure", q, &p)
		return checkinsResultMsg{ui: ui, request: q, policy: p, err: err}
	}
}
func (m *dashboardModel) acceptCheckinResult(msg checkinsResultMsg) tea.Cmd {
	m.checkinPending = false
	msg.ui.sending = false
	msg.ui.finished = true
	msg.ui.confirm = false
	msg.ui.editing = false
	if msg.err != nil {
		var api *model.Error
		if errors.As(msg.err, &api) && (definiteLeaseRejection(api.Code) || api.Code == "policy_conflict" || api.Code == "review_capacity") {
			msg.ui.note = "Settings rejected: " + safe(api.Error()) + ". Refresh the lease and policy before another change."
		} else {
			msg.ui.note = "Outcome may be unknown: " + safe(msg.err.Error()) + ". Refresh the lease and policy before another change; no automatic retry."
		}
	} else {
		msg.ui.note = "Settings accepted. Refresh the lease and policy before another change."
	}
	// Retain a visible receipt even if the modal was closed or connection changed.
	m.notice = msg.ui.note
	return nil
}
func (m *dashboardModel) checkinText() string {
	ui := m.checkins
	if ui == nil {
		return ""
	}
	width := max(10, min(m.mainWidth(), 88)-8)
	heading := fmt.Sprintf("Lease %s · revision %d · policy generation %d\n", safe(ui.lease.ID), ui.lease.Revision, ui.policy.Generation)
	principle := "Ask the owning agent whether these resources are still needed. Replies and silence never renew, release or resize a lease."
	if ui.loading {
		return heading + "\nLoading durable check-in records…"
	}
	if ui.confirm && ui.request != nil {
		q := ui.request
		action := "Disable future questions; retain conversation history."
		if q.Enabled {
			action = fmt.Sprintf("Enable questions every %s; contention interval %s; reply window %s.", reviewDuration(q.Interval), reviewDuration(q.ContentionInterval), reviewDuration(q.ReplyTimeout))
		}
		body := heading + "\n" + action + "\n\n" + principle + "\n\nDelivery: exact registered owner through the durable ARM inbox. No lease expiry or resource claims change."
		if q.Enabled && (reviewDurationIsRounded(q.Interval) || reviewDurationIsRounded(q.ContentionInterval) || reviewDurationIsRounded(q.ReplyTimeout)) {
			body += fmt.Sprintf("\n\nExact draft durations: regular %s · contention %s · reply %s.", q.Interval, q.ContentionInterval, q.ReplyTimeout)
		}
		if ui.sending {
			return body + "\n\nREQUEST IN FLIGHT · no retries · closing may leave outcome unknown"
		}
		return body + "\n\n" + keycap("y", "Save reviewed settings", "") + "  " + keycap("n/Esc", "Keep unchanged", "quiet")
	}
	if ui.editing {
		label := "Off"
		if ui.enabled {
			label = "Enabled"
		}
		return heading + "\n" + window("CHECK-IN SETTINGS", fmt.Sprintf("[Space] %s\n[1] Regular interval        %s\n[2] While work is waiting   %s\n[3] Agent reply window      %s", label, safe(ui.interval), safe(ui.contention), safe(ui.timeout)), width, 0, true, "accent") + "\n\n" + principle + "\n\n" + safe(ui.note) + "\n\n" + keycap("y", "Review settings", "") + "  " + keycap("Esc", "Keep unchanged", "quiet")
	}
	var out strings.Builder
	out.WriteString(heading + "\n" + principle + "\n\n")
	state := "OFF · optional, off by default"
	if ui.policy.Enabled {
		state = fmt.Sprintf("ENABLED · every %s · reply window %s", reviewDuration(ui.policy.Interval), reviewDuration(ui.policy.ReplyTimeout))
		if ui.policy.ContentionInterval > 0 {
			state += " · " + reviewDuration(ui.policy.ContentionInterval) + " while work waits"
		}
	}
	next := "Unscheduled"
	if ui.policy.Enabled && !ui.policy.Next.IsZero() {
		next = date(&ui.policy.Next)
	}
	out.WriteString(window("POLICY", state+"\nNext scheduled: "+next, width, 0, false, "accent"))
	if ui.note != "" {
		out.WriteString("\n" + safe(ui.note) + "\n")
	}
	if len(ui.pending.Items) == 0 {
		out.WriteString("\nNo reply currently requested.\n")
	}
	for _, r := range ui.pending.Items {
		out.WriteString("\n" + checkinRecord(r, width))
	}
	out.WriteString("\n\n" + strong.Render("Conversation history") + "\n")
	for _, r := range ui.history.Items {
		if r.State != "pending" {
			out.WriteString("\n" + checkinRecord(r, width))
		}
	}
	if ui.history.More {
		out.WriteString("\n[p] Next history page · current page contains complete records\n")
	}
	out.WriteString("\n[e] Configure · [r] Refresh lease and policy · [i] Complete records · Esc lease details")
	return out.String()
}
func checkinRecord(r model.LeaseReview, width int) string {
	label := map[string]string{"pending": "AWAITING AGENT", "replied": "AGENT REPLIED", "timed_out": "NO REPLY", "superseded": "CONTEXT CHANGED"}[r.State]
	var out strings.Builder
	fmt.Fprintf(&out, "Owner: %s\nCreated: %s\nReply by: %s\n", safe(r.Agent), date(&r.Created), date(&r.Deadline))
	for _, c := range r.Context {
		fmt.Fprintf(&out, "\nWhy: %s\nLease: %s · revision %d\nClaims: %g CPU · %s memory · %s disk\nCaptured remaining: %s\nWaiting requests: %d with overlapping resource kinds\nUsage: CPU %s · memory %s · stale %t · complete %t\n", safe(c.Purpose), safe(c.Lease), c.Revision, c.Resources.CPU, byteCount(fmt.Sprint(c.Resources.Memory)), byteCount(fmt.Sprint(c.Resources.Disk)), reviewDuration(c.Remaining), c.QueuedDemand, safe(c.Usage.CPUQuality), safe(c.Usage.MemoryQuality), c.Usage.Stale, c.Usage.Complete)
		cpu := "unknown"
		if c.Usage.CPUCores != nil {
			cpu = fmt.Sprintf("%g cores", *c.Usage.CPUCores)
		}
		memory := "unknown"
		if c.Usage.MemoryBytes != nil {
			memory = byteCount(fmt.Sprint(*c.Usage.MemoryBytes))
		}
		fmt.Fprintf(&out, "Observed: %s CPU · %s memory · at %s\n", cpu, memory, date(c.Usage.Observed))
		for _, claim := range c.Pools {
			fmt.Fprintf(&out, "Custom claim: %s\n", safe(fmt.Sprint(claim)))
		}
		for _, claim := range c.Ports {
			fmt.Fprintf(&out, "Port claim: %s\n", safe(fmt.Sprint(claim)))
		}
	}
	if r.Reply != nil {
		fmt.Fprintf(&out, "\nAgent intent: %s\nExplanation: %s\nConversation only; use lease controls to review a real change.\n", safe(r.Reply.Intent), safe(r.Reply.Explanation))
	}
	if r.Detail != "" {
		out.WriteString("\n" + safe(r.Detail))
	}
	return window(label, ansi.Wrap(out.String(), max(1, width-4), ""), width, 0, false, map[bool]string{true: "warning", false: "blue"}[r.State == "pending"])
}
func (m *dashboardModel) checkinControls() []mouseControl {
	ui := m.checkins
	if ui.sending {
		return nil
	}
	if ui.loading {
		return []mouseControl{{"esc", "Back"}}
	}
	if ui.finished {
		return []mouseControl{{"r", "Refresh"}, {"esc", "Back"}}
	}
	if ui.confirm {
		return []mouseControl{{"y", "Save settings"}, {"n", "Keep unchanged"}, {"pgdown", "↓"}}
	}
	if ui.editing {
		return []mouseControl{{" ", "Enable/off"}, {"1", "Interval"}, {"2", "Contention"}, {"3", "Reply window"}, {"y", "Review"}, {"esc", "Back"}}
	}
	return []mouseControl{{"e", "Configure"}, {"r", "Refresh"}, {"p", "Next page"}, {"i", "Record"}, {"esc", "Back"}}
}
