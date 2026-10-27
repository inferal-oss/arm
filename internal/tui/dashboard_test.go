package tui

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/agents"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

type testTransport struct {
	mu         sync.Mutex
	operations []string
	params     []any
	reply      func(string, any, any) error
	events     chan model.Event
	telemetry  chan model.MonitorPage
	done       chan struct{}
}

func newTestTransport() *testTransport {
	return &testTransport{events: make(chan model.Event, 10), telemetry: make(chan model.MonitorPage, 10), done: make(chan struct{})}
}
func (s *testTransport) Call(_ context.Context, operation string, params, result any) error {
	s.mu.Lock()
	s.operations = append(s.operations, operation)
	s.params = append(s.params, params)
	s.mu.Unlock()
	if s.reply != nil {
		return s.reply(operation, params, result)
	}
	return nil
}
func (s *testTransport) Events() <-chan model.Event          { return s.events }
func (s *testTransport) Telemetry() <-chan model.MonitorPage { return s.telemetry }
func (s *testTransport) Done() <-chan struct{}               { return s.done }
func (s *testTransport) Err() error                          { return errors.New("fixture disconnected") }
func (s *testTransport) Close() {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}
func fixtureDashboard(t *testing.T) (*dashboardModel, *testTransport) {
	t.Helper()
	m := newDashboard(t.Context(), pendingHome(t))
	s := newTestTransport()
	m.session = s
	m.connected = true
	m.connecting = false
	m.data.status = model.Status{Host: "localhost", Owner: "501", OwnerUsername: "alice"}
	return m, s
}
func activeLease(id string) model.Lease {
	expires := time.Now().Add(time.Hour)
	return model.Lease{ID: id, Revision: 7, Owner: "501", OwnerUsername: "alice", Host: "localhost", State: "active", Expires: &expires, Request: model.Request{Reason: "build output", Resources: model.Resources{CPU: 2}}}
}
func press(m *dashboardModel, text string) tea.Cmd {
	if len(text) == 1 {
		return m.key(tea.KeyPressMsg{Code: rune(text[0])})
	}
	switch text {
	case "esc":
		return m.key(tea.KeyPressMsg{Code: tea.KeyEscape})
	case "enter":
		return m.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	}
	panic(text)
}

func TestDashboardLayoutKeepsViewportFeedAndUntrustedTextSafe(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.width = 130
	m.height = 32
	m.view = 2
	m.data.status.Leases = []model.Lease{activeLease("a")}
	m.data.status.Leases[0].Request.Reason = "bad\x1b]52;c;secret\a\nreason"
	m.addEvent(model.Event{Type: "lease.created", Entity: "a", At: time.Now()})
	m.resize()
	m.syncViewport()
	text := ansi.Strip(m.View().Content)
	for _, want := range []string{"1 Monitor", "2 Resources", "3 Leases", "LIVE FEED", "Why", "Local operator"} {
		if want == "Why" {
			continue
		}
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "\x1b") || !strings.Contains(text, `\u001b]52`) {
		t.Fatal("untrusted terminal control was not escaped", text)
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 32 {
		t.Fatal("layout escaped viewport", len(lines))
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 130 {
			t.Fatal("line exceeds viewport", ansi.StringWidth(line), line)
		}
	}
	m.width = 60
	m.height = 18
	m.resize()
	m.syncViewport()
	press(m, "f")
	m.syncViewport()
	if !strings.Contains(ansi.Strip(m.View().Content), "LIVE FEED") {
		t.Fatal("compact feed unavailable")
	}
	m.width = 20
	m.height = 8
	if !strings.Contains(m.View().Content, "Enlarge") {
		t.Fatal("small terminal has no bounded fallback")
	}
}
func TestDashboardReviewedLeaseDeclineAndExactScope(t *testing.T) {
	m, s := fixtureDashboard(t)
	parent := activeLease("parent")
	parent.Request.Kind = "storage"
	parent.Artifact = &model.Artifact{Path: "/managed/output"}
	m.detail = &parent
	child := activeLease("child")
	child.Revision = 3
	child.Request.StorageLease = parent.ID
	m.data.status.Leases = []model.Lease{parent, child}
	press(m, "x")
	if m.review == nil || len(m.review.request.Consumers) != 1 || m.review.request.Consumers[0].Revision != 3 {
		t.Fatal("review missed exact dependent scope")
	}
	if !strings.Contains(m.leaseReviewText(), "/managed/output") || !strings.Contains(m.leaseReviewText(), "child") {
		t.Fatal("review omitted destructive scope")
	}
	press(m, "n")
	if len(s.operations) != 0 || m.review != nil {
		t.Fatal("declined review mutated")
	}
	press(m, "x")
	cmd := press(m, "y")
	if cmd == nil || !m.pending {
		t.Fatal("confirmation did not create one request")
	}
	if press(m, "y") != nil {
		t.Fatal("in-flight confirmation duplicated")
	}
	s.reply = func(operation string, p, result any) error {
		if operation != "lease_action" {
			t.Fatal(operation)
		}
		q := p.(model.LeaseAction)
		if q.Revision != 7 || q.Consumers[0].ID != "child" {
			t.Fatal(q)
		}
		return model.Err("revision_conflict", "changed")
	}
	m.Update(cmd())
	if m.pending || !strings.Contains(m.notice, "revision_conflict") {
		t.Fatal("stale rejection not explained", m.notice)
	}
	if len(s.operations) != 1 {
		t.Fatal("request retried")
	}
}
func TestDashboardLeaseUnknownRequiresFreshInspectAndCloseRace(t *testing.T) {
	m, s := fixtureDashboard(t)
	l := activeLease("lease")
	m.detail = &l
	s.reply = func(operation string, p, result any) error {
		if operation == "inspect" {
			*result.(*model.Lease) = l
			return nil
		}
		return errors.New("response lost")
	}
	press(m, "x")
	cmd := press(m, "y")
	m.Update(cmd())
	if m.unknownLease != "lease" {
		t.Fatal("uncertain action lost")
	}
	if press(m, "x") != nil || m.review != nil {
		t.Fatal("unknown action allowed fresh mutation without inspection")
	}
	cmd = press(m, "r")
	m.Update(cmd())
	if m.unknownLease != "" {
		t.Fatal("fresh inspect did not resolve current state")
	}
	m.detail = nil
	cmd = m.loadDetail("lease")
	press(m, "2")
	m.Update(cmd())
	if m.detail != nil {
		t.Fatal("late detail response reopened after navigation")
	}
}
func TestDashboardDurationFromNowAndQueueSemantics(t *testing.T) {
	for text, want := range map[string]time.Duration{"1h30m": 90 * time.Minute, "1d": 24 * time.Hour, "0.5m": 30 * time.Second, "7d": 7 * 24 * time.Hour} {
		got, err := parseTTL(text)
		if err != nil || got != want {
			t.Fatalf("%s: %v %v", text, got, err)
		}
	}
	for _, text := range []string{"", "0s", "-1h", "8d", "999999999999999999999h", "1ns", "1.0000000001s", "1h x"} {
		if _, err := parseTTL(text); err == nil {
			t.Fatal("accepted invalid duration", text)
		}
	}
	m, s := fixtureDashboard(t)
	l := activeLease("lease")
	m.detail = &l
	m.reviewTTL("15m")
	if m.review != nil {
		t.Fatal("renewal shortened lifetime")
	}
	m.reviewTTL("2h")
	if m.review == nil || m.review.request.TTL != 2*time.Hour || !strings.Contains(m.leaseReviewText(), "not added") {
		t.Fatal("renewal lacks duration-from-now semantics")
	}
	m.review = nil
	l.State = "queued"
	m.startReview("release")
	if m.review != nil {
		t.Fatal("queued release offered")
	}
	m.startReview("cancel")
	if m.review == nil || m.review.request.Consumers == nil {
		t.Fatal("queued cancel missing reviewed empty consumers")
	}
	press(m, "n")
	if len(s.operations) != 0 {
		t.Fatal("review mutated")
	}
}
func TestDashboardMessagesReviewExactContactsDeclineAndUnknownLookup(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 3
	m.data.contacts = []agents.Contact{{ID: "agent", Session: "session", Registration: "generation1", Delivery: "poll", CWD: "/work", Adapter: "generic"}}
	m.beginCompose(true)
	m.inputMode = ""
	m.composeBody("hello")
	if !strings.Contains(m.messageReviewText(), "generation1") || !strings.Contains(m.messageReviewText(), "/work") {
		t.Fatal("missing exact recipient review")
	}
	press(m, "n")
	if len(s.operations) != 0 {
		t.Fatal("decline sent message")
	}
	m.beginCompose(true)
	m.inputMode = ""
	m.composeBody("hello")
	m.data.contacts[0].Registration = "generation2"
	s.reply = func(operation string, p, result any) error {
		if operation == "message_lookup" {
			raw, _ := json.Marshal(map[string]bool{"found": false})
			return json.Unmarshal(raw, result)
		}
		q := p.(manager.MessageRequest)
		if q.Targets[0].Registration != "generation1" {
			t.Fatal("review snapshot silently changed")
		}
		return errors.New("lost response")
	}
	cmd := press(m, "y")
	m.Update(cmd())
	if m.pendingMessage == nil || !m.composer.unknown {
		t.Fatal("unknown message request lost")
	}
	key := m.pendingMessage.Key
	cmd = press(m, "i")
	m.Update(cmd())
	if m.pendingMessage.Key != key || !strings.Contains(m.notice, "may still commit") {
		t.Fatal("absent lookup wrongly definitive")
	}
	cmd = press(m, "y")
	m.Update(cmd())
	if m.pendingMessage.Key != key || len(s.operations) != 3 {
		t.Fatal("explicit retry changed request key or automatic retry occurred")
	}
}
func TestDashboardRefreshPreservesSelectionAndScrolling(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.view = 2
	m.data.status.Leases = []model.Lease{activeLease("a"), activeLease("b")}
	m.selected = 1
	data := m.data
	data.status.Leases = []model.Lease{activeLease("new"), activeLease("a"), activeLease("b")}
	m.applySnapshot(data)
	if m.selected != 2 {
		t.Fatal("refresh moved reading selection")
	}
	m.view = 5
	m.data.status.Capabilities = make([]string, 100)
	for i := range m.data.status.Capabilities {
		m.data.status.Capabilities[i] = "capability"
	}
	m.syncViewport()
	press(m, "j")
	if m.viewport.YOffset() == 0 {
		t.Fatal("viewport cannot scroll live content")
	}
	old := m.session
	m.Update(streamMsg{session: old, err: errors.New("lost")})
	if m.connected {
		t.Fatal("lost transport remained live")
	}
	m.Update(refreshedMsg{session: newTestTransport(), data: snapshot{status: model.Status{Host: "other"}}})
	if m.data.status.Host == "other" {
		t.Fatal("stale refresh replaced new session")
	}
}
func TestMonitorMergeExplicitGapsAndBoundedBuffers(t *testing.T) {
	previous := []model.MonitorPoint{{Sequence: 1}}
	page := model.MonitorPage{Epoch: "e", Cursor: 2, History: []model.MonitorPoint{{Sequence: 2}}}
	if got := mergeMonitor(previous, page, "e"); len(got) != 2 {
		t.Fatal("history lost without gap")
	}
	page.Gap = true
	if got := mergeMonitor(previous, page, "e"); len(got) != 1 || got[0].Sequence != 2 {
		t.Fatal("coalesced gap fabricated continuity")
	}
	m, _ := fixtureDashboard(t)
	for i := range 300 {
		m.addEvent(model.Event{Sequence: int64(i), Type: "event"})
	}
	if len(m.events) != 200 {
		t.Fatal("unbounded event feed")
	}
	page.Cursor = math.MaxInt64
	page.History = []model.MonitorPoint{{Sequence: math.MaxInt64}}
	if len(mergeMonitor(nil, page, "e")) != 1 {
		t.Fatal("cursor overflow")
	}
}

func TestDashboardUncertainMessageErrorsAndStaleLookupsKeepExactRequest(t *testing.T) {
	for _, code := range []string{"too_many_requests", "backend_stopping", "internal_error", "idempotency_conflict"} {
		t.Run(code, func(t *testing.T) {
			m, _ := fixtureDashboard(t)
			request := manager.MessageRequest{Key: "exact-key", Audience: "agent", Body: "retained", Targets: []agents.Contact{{ID: "agent", Registration: "generation"}}}
			m.messageOperation = 1
			m.messageBusy = "send"
			m.pendingMessage = &request
			m.acceptMessageResult(messageResultMsg{operation: 1, request: request, err: model.Err(code, "fixture")})
			if m.pendingMessage == nil || m.pendingMessage.Key != request.Key || !m.composer.unknown {
				t.Fatal("uncertain backend error discarded key", code)
			}
			m.acceptLookup(lookupResultMsg{key: "other-key", found: true, message: manager.Message{ID: "other"}})
			if m.pendingMessage == nil {
				t.Fatal("stale lookup cleared different pending operation")
			}
			m.messageOperation = 2
			m.messageBusy = "send"
			m.acceptMessageResult(messageResultMsg{operation: 2, request: request, err: model.Err("contact_changed", "fixture"), priorUnknown: true})
			if m.pendingMessage == nil {
				t.Fatal("retry rejection incorrectly erased original uncertainty")
			}
		})
	}
}
func TestDashboardResourceConsumerDrilldownPreservesWhoAndWhy(t *testing.T) {
	m, s := fixtureDashboard(t)
	m.view = 1
	m.data.status.Resources = []model.ResourceView{{ID: "cpu", Kind: "cpu", Unit: "logical-core-equivalents", BudgetTotal: "4", Reserved: "1", Available: "3", Users: []model.ResourceUse{{Lease: "build", Owner: "501", OwnerUsername: "alice", Reason: "compile needed binary", Quantity: "1"}}}}
	cpuPresentationFixture(m, 4, 1, 3)
	press(m, "enter")
	if m.resourceID != "cpu" {
		t.Fatal("resource did not open")
	}
	text := m.resourceDetailText()
	for _, want := range []string{"alice (UID 501)", "compile needed binary", "build", "To lease: 3 cores"} {
		if !strings.Contains(text, want) {
			t.Fatal("resource omitted attribution/budget", want, text)
		}
	}
	s.reply = func(op string, p, result any) error { *result.(*model.Lease) = activeLease("build"); return nil }
	cmd := press(m, "enter")
	m.Update(cmd())
	if m.detail == nil || m.detail.ID != "build" {
		t.Fatal("consumer drilldown failed")
	}
	press(m, "esc")
	if m.detail != nil || m.resourceID != "cpu" {
		t.Fatal("close failed to restore resource")
	}
}

func TestDashboardCompactFeedCannotHideReviewOrStructuredRecord(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.width = 70
	m.height = 25
	m.resize()
	m.feedOpen = true
	m.feedFocus = true
	l := activeLease("lease")
	m.detail = &l
	m.startReview("release")
	m.syncViewport()
	if text := ansi.Strip(m.View().Content); !strings.Contains(text, "REVIEW RELEASE") {
		t.Fatal("feed hid lease review", text)
	}
	m.review = nil
	m.detail = nil
	m.record, _ = newRecord(map[string]string{"key": "visible record"}, "Record")
	m.syncViewport()
	if text := ansi.Strip(m.View().Content); !strings.Contains(text, "visible record") {
		t.Fatal("feed hid structured record", text)
	}
	m.record = nil
	m.composer = &messageComposer{review: true, request: manager.MessageRequest{Audience: "agent", Body: "review body"}}
	m.syncViewport()
	if !strings.Contains(m.displayContent(), "REVIEW MESSAGE") {
		t.Fatal("feed hid message confirmation")
	}
}
