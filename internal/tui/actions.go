package tui

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/model"
)

type detailMsg struct {
	generation uint64
	lease      model.Lease
	err        error
}
type leaseReview struct {
	request model.LeaseAction
	lease   model.Lease
	text    string
}
type leaseResultMsg struct {
	request model.LeaseAction
	lease   model.Lease
	err     error
}

func (m *dashboardModel) loadDetail(id string) tea.Cmd {
	if !m.connected {
		return nil
	}
	m.feedFocus = false
	m.feedOpen = false
	m.detailGeneration++
	generation, session, ctx := m.detailGeneration, m.session, m.ctx
	m.review = nil
	m.notice = "Loading lease…"
	return func() tea.Msg {
		var lease model.Lease
		err := session.Call(ctx, "inspect", map[string]string{"id": id}, &lease)
		return detailMsg{generation, lease, err}
	}
}
func (m *dashboardModel) acceptDetail(msg detailMsg) tea.Cmd {
	if msg.generation != m.detailGeneration {
		return nil
	}
	if msg.err != nil {
		m.notice = msg.err.Error()
		return nil
	}
	m.detail = &msg.lease
	if m.unknownLease == msg.lease.ID && !m.pending {
		m.unknownLease = ""
	}
	m.review = nil
	m.notice = ""
	m.viewport.GotoTop()
	return nil
}
func consuming(state string) bool {
	switch state {
	case "queued", "active", "ending", "needs_attention", "provisioning", "reclaiming":
		return true
	}
	return false
}
func reviewedConsumers(lease model.Lease, leases []model.Lease) []model.LeaseRevision {
	out := make([]model.LeaseRevision, 0)
	for _, item := range leases {
		if consuming(item.State) && (item.Request.StorageLease == lease.ID || item.Request.PortLease == lease.ID) {
			out = append(out, model.LeaseRevision{ID: item.ID, Revision: item.Revision})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

var durationParts = regexp.MustCompile(`([0-9]+(?:\.[0-9]{1,9})?)([smhd])`)

func parseTTL(text string) (time.Duration, error) {
	if len(text) > 80 {
		return 0, fmt.Errorf("duration must be at most 80 characters")
	}
	text = strings.TrimSpace(text)
	parts := durationParts.FindAllStringSubmatchIndex(text, -1)
	offset := 0
	var total time.Duration
	for _, p := range parts {
		if p[0] != offset {
			return 0, fmt.Errorf("use a duration such as 15m, 2h, 1d or 1h30m")
		}
		unit := text[p[4]:p[5]]
		mult := time.Second
		switch unit {
		case "m":
			mult = time.Minute
		case "h":
			mult = time.Hour
		case "d":
			mult = 24 * time.Hour
		}
		// ParseDuration provides exact nanosecond conversion; keep each part
		// below seven days before multiplication to prevent overflow.
		number := text[p[2]:p[3]]
		whole, err := strconv.ParseFloat(number, 64)
		if err != nil || whole > float64(7*24*time.Hour)/float64(mult) {
			return 0, fmt.Errorf("duration must be greater than zero and at most 7 days")
		}
		part, err := time.ParseDuration(number + "s")
		if err != nil {
			return 0, err
		}
		// Convert seconds via quotient/remainder without float rounding.
		v := part/time.Second*mult + (part%time.Second)*(mult/time.Second)
		// Zero components occur in Go duration strings such as 10m0s;
		// the final total below must still be positive and bounded.
		if v < 0 || total > 7*24*time.Hour-v {
			return 0, fmt.Errorf("duration must be greater than zero and at most 7 days")
		}
		total += v
		offset = p[1]
	}
	if offset != len(text) || total <= 0 || total > 7*24*time.Hour {
		return 0, fmt.Errorf("duration must be greater than zero and at most 7 days")
	}
	return total, nil
}
func (m *dashboardModel) startReview(action string) tea.Cmd {
	if !m.connected || m.pending || m.detail == nil {
		return nil
	}
	if m.unknownLease != "" {
		m.notice = "Inspect the uncertain lease with r before reviewing another action."
		return nil
	}
	l := *m.detail
	if l.State != "active" && !(l.State == "queued" && action == "cancel") {
		m.notice = "This lease has no available action in its displayed state. Refresh it."
		return nil
	}
	if action == "renew" {
		// Clock-backed entitlement is checked by Manager; calendar expiry is audit data.
		if l.Clock == nil && (l.Expires == nil || !l.Expires.After(time.Now())) {
			m.notice = "Lease is expired; refresh it."
			return nil
		}
		m.inputMode = "ttl"
		m.input.SetValue("15m")
		return m.input.Focus()
	}
	m.review = &leaseReview{request: model.LeaseAction{ID: l.ID, Revision: l.Revision, Action: action, Consumers: reviewedConsumers(l, m.data.status.Leases)}, lease: l, text: leaseConsequences(l)}
	m.viewport.GotoTop()
	return nil
}
func (m *dashboardModel) reviewTTL(text string) tea.Cmd {
	if m.detail == nil || !m.connected {
		return nil
	}
	ttl, err := parseTTL(text)
	if err != nil {
		m.notice = err.Error()
		return nil
	}
	expires := time.Now().Add(ttl)
	if m.detail.Clock == nil && (m.detail.Expires == nil || !expires.After(*m.detail.Expires)) {
		m.notice = "Choose a duration from now that extends the current expiry."
		return nil
	}
	m.review = &leaseReview{request: model.LeaseAction{ID: m.detail.ID, Revision: m.detail.Revision, Action: "renew", TTL: ttl}, lease: *m.detail, text: "Duration starts when ARM accepts the request; it is not added to the current expiry. Waiting work, memory pressure, pool limits or attached lease deadlines may deny extension."}
	m.viewport.GotoTop()
	return nil
}
func leaseConsequences(l model.Lease) string {
	var text string
	if l.State == "queued" {
		text = "Remove this queued request. It will not start or receive resources."
	} else if l.Request.Kind == "storage" {
		text = "End this storage lease and reclaim its managed files. Save needed outputs first. Attached supervised jobs will stop; ownership and activity checks still apply. Unresolved allocations stay charged."
		if l.Artifact != nil {
			text += "\nManaged location: " + safe(l.Artifact.Path)
		}
	} else if l.Request.Kind == "ports" {
		text = "End this port lease, stop attached supervised jobs and reclaim ARM-owned sockets. Unresolved sockets stay charged."
	} else if l.PID != 0 || l.Process != nil {
		text = "Stop the ARM-supervised process group. ARM requests termination and can force termination after its grace period. Capacity stays charged until stopping is confirmed."
	} else {
		text = "End the reservation and return its claims. Unrelated processes and arbitrary files are outside this action."
	}
	if l.Request.StorageLease != "" {
		text += "\nAttached storage retains its own lifetime: " + safe(l.Request.StorageLease)
	}
	if l.Request.PortLease != "" {
		text += "\nAttached port lease retains its own lifetime: " + safe(l.Request.PortLease)
	}
	return text
}
func (m *dashboardModel) reviewKey(key string) tea.Cmd {
	if scrollReview(&m.viewport, key) {
		return nil
	}
	if m.pending {
		return nil
	}
	if key == "esc" || key == "n" {
		m.review = nil
		return nil
	}
	if key != "y" || !m.connected {
		return nil
	}
	request := m.review.request
	if request.Action == "renew" && m.review.lease.Clock == nil && (m.review.lease.Expires == nil || !m.review.lease.Expires.After(time.Now())) {
		m.review = nil
		m.notice = "Lease expired while reviewing; refresh it."
		return nil
	}
	m.pending = true
	m.unknownLease = request.ID
	m.notice = "Request in flight. No automatic retry."
	session, ctx := m.session, m.ctx
	return func() tea.Msg {
		var l model.Lease
		err := session.Call(ctx, "lease_action", request, &l)
		return leaseResultMsg{request, l, err}
	}
}
func (m *dashboardModel) acceptLeaseResult(msg leaseResultMsg) tea.Cmd {
	m.pending = false
	m.review = nil
	m.dirty = true
	if msg.err != nil {
		var api *model.Error
		if errors.As(msg.err, &api) && definiteLeaseRejection(api.Code) {
			m.unknownLease = ""
			m.notice = "Request rejected: " + api.Error() + ". Refresh and review before trying again."
		} else {
			m.notice = "Outcome unknown for lease " + msg.request.ID + ". Inspect current state before another action. No request was retried."
		}
		return nil
	}
	m.unknownLease = ""
	m.detail = &msg.lease
	m.notice = "Request accepted; current state: " + msg.lease.State
	if msg.lease.State == "ending" || msg.lease.State == "reclaiming" || msg.lease.State == "needs_attention" {
		m.notice += ". Capacity remains charged until cleanup/stopping is confirmed."
	}
	return nil
}

func definiteLeaseRejection(code string) bool {
	switch code {
	case "invalid_request", "revision_conflict", "not_found", "backend_stopping", "backend_draining", "lease_inactive", "renewal_denied", "needs_attention", "operation_busy", "too_many_requests", "forbidden":
		return true
	}
	return false
}
