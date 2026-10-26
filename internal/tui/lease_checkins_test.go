package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/inferal-oss/arm/internal/model"
)

func checkinFixture(t *testing.T) (*dashboardModel, *testTransport) {
	t.Helper()
	m, s := fixtureDashboard(t)
	l := activeLease("lease")
	l.Request.Agent = "owner"
	m.detail = &l
	m.data.status.Capabilities = []string{leaseCheckinCapability}
	s.reply = func(op string, arg, result any) error {
		switch op {
		case "inspect":
			*result.(*model.Lease) = l
		case "lease_review_policy":
			*result.(*model.LeaseReviewPolicy) = model.LeaseReviewPolicy{Lease: l.ID, Generation: 3}
		case "lease_review_list":
			*result.(*model.LeaseReviewPage) = model.LeaseReviewPage{}
		case "lease_review_configure":
			*result.(*model.LeaseReviewPolicy) = model.LeaseReviewPolicy{Lease: l.ID, Generation: 4}
		}
		return nil
	}
	return m, s
}
func openTestCheckin(t *testing.T, m *dashboardModel) {
	t.Helper()
	cmd := press(m, "h")
	if cmd == nil {
		t.Fatal("no read command")
	}
	m.Update(cmd())
	if m.checkins.loading {
		t.Fatal("read not accepted")
	}
}
func TestLeaseCheckinDraftBoundsAndExactPolicyGeneration(t *testing.T) {
	l := activeLease("a")
	l.Request.Agent = "owner"
	ui := &leaseCheckins{lease: l, policy: model.LeaseReviewPolicy{Generation: 5}, enabled: true, interval: "10m", contention: "0s", timeout: "1m"}
	q, e := checkinDraft(ui)
	if e != nil || q.Revision != l.Revision || *q.Generation != 5 || q.Interval != 10*time.Minute {
		t.Fatal(q, e)
	}
	for _, entry := range []struct{ interval, contention, timeout string }{{"14s", "0s", "1m"}, {"10m", "11m", "1m"}, {"10m", "1s", "1m"}, {"10m", "0s", "4s"}, {"10m", "0s", "2h"}} {
		ui.interval = entry.interval
		ui.contention = entry.contention
		ui.timeout = entry.timeout
		if _, e := checkinDraft(ui); e == nil {
			t.Fatal("accepted invalid", entry)
		}
	}
	ui.enabled = false
	if _, e := checkinDraft(ui); e != nil {
		t.Fatal("disabling should not require obsolete duration", e)
	}
}
func TestLeaseCheckinReviewDeclineAndSaveUseExactPair(t *testing.T) {
	m, s := checkinFixture(t)
	openTestCheckin(t, m)
	if len(s.operations) != 4 {
		t.Fatal(s.operations)
	}
	press(m, "e")
	press(m, " ")
	press(m, "y")
	if m.checkins.request == nil || *m.checkins.request.Generation != 3 || m.checkins.request.Revision != 7 || !m.checkins.request.Enabled {
		t.Fatal(m.checkins)
	}
	press(m, "n")
	if len(s.operations) != 4 || m.checkins.confirm {
		t.Fatal("decline mutated")
	}
	press(m, "y")
	cmd := press(m, "y")
	if cmd == nil || !m.checkinPending {
		t.Fatal("not pending")
	}
	if press(m, "y") != nil {
		t.Fatal("duplicate submit")
	}
	m.Update(cmd())
	if len(s.operations) != 5 || s.operations[4] != "lease_review_configure" || !m.checkins.finished {
		t.Fatal(s.operations)
	}
	if press(m, "e") != nil || press(m, "y") != nil {
		t.Fatal("stale configuration reused")
	}
	for _, op := range s.operations {
		if op == "lease_action" || op == "lease_review_reply" {
			t.Fatal("conversational UI acquired agent/resource authority")
		}
	}
}
func TestLeaseCheckinStaleReadAndConnectionDoNotReplaceCurrentDialog(t *testing.T) {
	m, s := checkinFixture(t)
	cmd := press(m, "h")
	old := m.checkins
	press(m, "esc")
	if m.checkins != nil {
		t.Fatal("not closed")
	}
	m.Update(cmd())
	if m.checkins != nil {
		t.Fatal("stale read reopened")
	}
	cmd = press(m, "h")
	m.session = newTestTransport()
	m.Update(cmd())
	if m.checkins == nil || !m.checkins.loading {
		t.Fatal("foreign connection accepted")
	}
	m.session = s
	m.checkins = old
}
func TestLeaseCheckinUnknownOutcomeNoRetryAndFreshLeasePolicyRequired(t *testing.T) {
	m, s := checkinFixture(t)
	openTestCheckin(t, m)
	old := s.reply
	s.reply = func(op string, a, b any) error {
		if op == "lease_review_configure" {
			return errors.New("lost response")
		}
		return old(op, a, b)
	}
	press(m, "e")
	press(m, "y")
	cmd := press(m, "y")
	m.connected = false
	m.Update(cmd())
	if !m.checkins.finished || !strings.Contains(m.notice, "unknown") || m.checkinPending {
		t.Fatal(m.notice)
	}
	before := len(s.operations)
	press(m, "y")
	press(m, "e")
	if len(s.operations) != before {
		t.Fatal("retried")
	}
	m.connected = true
	cmd = press(m, "r")
	m.Update(cmd())
	if m.checkins.finished || m.checkins.confirm || m.checkins.editing {
		t.Fatal("refresh retained stale form")
	}
	if s.operations[before] != "inspect" || s.operations[before+1] != "lease_review_policy" {
		t.Fatal(s.operations)
	}
}
func TestLeaseCheckinUnsupportedBackendAndReadErrorsRemainReadOnly(t *testing.T) {
	m, s := checkinFixture(t)
	m.data.status.Capabilities = nil
	if press(m, "h") != nil || len(s.operations) != 0 {
		t.Fatal("unsupported read")
	}
	m.data.status.Capabilities = []string{leaseCheckinCapability}
	s.reply = func(string, any, any) error { return errors.New("read denied") }
	cmd := press(m, "h")
	m.Update(cmd())
	if !m.checkins.finished || !strings.Contains(m.checkins.note, "denied") {
		t.Fatal(m.checkins)
	}
	press(m, "e")
	press(m, "y")
	if len(s.operations) != 1 {
		t.Fatal("read failure mutated")
	}
}
func TestLeaseCheckinSnapshotFailureDoesNotBreakCriticalDashboardData(t *testing.T) {
	s := newTestTransport()
	s.reply = func(op string, a, b any) error {
		switch op {
		case "status":
			*b.(*model.Status) = model.Status{Host: "local", Capabilities: []string{leaseCheckinCapability}}
		case "lease_review_list":
			return errors.New("optional failed")
		}
		return nil
	}
	data, err := readSnapshot(context.Background(), s)
	if err != nil || data.status.Host != "local" || data.reviewError != "optional failed" {
		t.Fatal(data, err)
	}
}
func TestLeaseCheckinPresentationEscapesAndStaysInsideViewport(t *testing.T) {
	m, _ := checkinFixture(t)
	openTestCheckin(t, m)
	now := time.Now()
	m.checkins.pending.Items = []model.LeaseReview{{Agent: "bad\x1b]52;secret\a", State: "pending", Created: now, Deadline: now.Add(time.Minute), Context: []model.LeaseReviewContext{{Purpose: "build\x1b[31m", Resources: model.Resources{CPU: 2}, Usage: model.LeaseReviewUsage{CPUQuality: "measured", MemoryQuality: "unknown", Stale: true}}}}}
	text := ansi.Strip(m.checkinText())
	if strings.Contains(text, "\x1b") || !strings.Contains(text, `\u001b]52`) {
		t.Fatal("unsafe terminal bytes", text)
	}
	for _, size := range [][2]int{{130, 35}, {80, 24}, {55, 18}} {
		m.width, m.height = size[0], size[1]
		m.syncViewport()
		view := ansi.Strip(m.View().Content)
		lines := strings.Split(view, "\n")
		if len(lines) != m.height {
			t.Fatal("height", size, len(lines))
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > m.width {
				t.Fatal("width", size, line)
			}
		}
	}
	controls := m.controls()
	found := false
	for _, c := range controls {
		if c.key == "e" && c.label == "Configure" {
			found = true
		}
	}
	if !found {
		t.Fatal("mouse/keyboard footer missing")
	}
}

func TestLeaseCheckinDisabledScheduleShowsUnscheduled(t *testing.T) {
	m, _ := checkinFixture(t)
	openTestCheckin(t, m)
	text := ansi.Strip(m.checkinText())
	if !strings.Contains(text, "Next scheduled: Unscheduled") || strings.Contains(text, "LMT") || strings.Contains(text, "0001") {
		t.Fatal("disabled policy displays invented schedule", text)
	}
	m.checkins.policy.Enabled = true
	m.checkins.policy.Next = time.Time{}
	if !strings.Contains(ansi.Strip(m.checkinText()), "Next scheduled: Unscheduled") {
		t.Fatal("zero next displayed as date")
	}
	m.checkins.policy.Next = time.Now().Add(time.Minute)
	if strings.Contains(ansi.Strip(m.checkinText()), "Next scheduled: Unscheduled") {
		t.Fatal("valid scheduled policy hidden")
	}
}
