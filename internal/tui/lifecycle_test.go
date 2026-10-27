package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"getarm.org/internal/agents"
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
)

func unknownMessageFixture(t *testing.T) (*dashboardModel, *testTransport, manager.MessageRequest) {
	t.Helper()
	m, s := fixtureDashboard(t)
	request := manager.MessageRequest{Key: "reviewed-key", Audience: "agent", Body: "exact body", Targets: []agents.Contact{{ID: "agent", Registration: "registration"}}}
	m.pendingMessage = &request
	intent, err := m.prepareDurable("message", m.data.status.Host, request, false)
	if err != nil {
		t.Fatal(err)
	}
	m.messageIntent = intent
	m.composer = &messageComposer{authority: m.data.status.Host, request: request, review: true, unknown: true}
	return m, s, request
}
func recordedMessage(request manager.MessageRequest) manager.Message {
	return manager.Message{ID: "recorded-id", Key: request.Key, Audience: request.Audience, Body: request.Body, Lease: request.Lease, ReplyTo: request.ReplyTo}
}

func TestDashboardSerializesDelayedLookupAndRetryAndIgnoresOldResults(t *testing.T) {
	m, s, request := unknownMessageFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	finished := make(chan tea.Msg, 1)
	s.reply = func(operation string, p, result any) error {
		if operation == "message_lookup" {
			close(started)
			<-release
			raw, _ := json.Marshal(map[string]bool{"found": false})
			return json.Unmarshal(raw, result)
		}
		*result.(*manager.Message) = recordedMessage(p.(manager.MessageRequest))
		return nil
	}
	lookup := press(m, "i")
	go func() { finished <- lookup() }()
	<-started
	lookupOperation := m.messageOperation
	if press(m, "i") != nil || press(m, "y") != nil || m.messageBusy != "lookup" {
		t.Fatal("lookup allowed overlapping lookup/retry")
	}
	close(release)
	m.Update(<-finished)
	if m.pendingMessage == nil || m.messageBusy != "" {
		t.Fatal("absent lookup erased uncertainty")
	}
	retry := press(m, "y")
	retryOperation := m.messageOperation
	oldLookup := lookupResultMsg{operation: lookupOperation, key: request.Key, found: true, message: recordedMessage(request)}
	m.Update(oldLookup)
	if m.pendingMessage == nil || m.messageBusy != "send" {
		t.Fatal("old lookup cleared pending retry")
	}
	oldRetryResult := retry().(messageResultMsg)
	m.Update(oldRetryResult)
	if m.pendingMessage != nil {
		t.Fatal("matching retry response did not settle operation")
	}
	m.data.contacts = []agents.Contact{{ID: "agent", Registration: "new-registration"}}
	m.beginCompose(true)
	m.inputMode = ""
	m.composeBody("new message")
	newSend := press(m, "y")
	newKey := m.pendingMessage.Key
	m.Update(oldLookup)
	m.Update(oldRetryResult)
	if m.pendingMessage == nil || m.pendingMessage.Key != newKey || m.messageBusy != "send" || m.messageOperation == retryOperation {
		t.Fatal("old lookup/retry result erased new request")
	}
	m.Update(newSend())
	if m.pendingMessage != nil {
		t.Fatal("matching new send result did not settle")
	}
}
func TestDashboardMismatchedMessageAndLookupResponsesPreserveUncertainty(t *testing.T) {
	for _, kind := range []string{"send-key", "send-body", "send-request", "lookup-key", "lookup-record-key", "lookup-record-body", "lookup-missing-record"} {
		t.Run(kind, func(t *testing.T) {
			m, _, request := unknownMessageFixture(t)
			m.messageOperation = 9
			message := recordedMessage(request)
			if strings.HasPrefix(kind, "send") {
				m.messageBusy = "send"
				sent := request
				switch kind {
				case "send-key":
					message.Key = "other"
				case "send-body":
					message.Body = "other"
				case "send-request":
					sent.Body = "other"
				}
				m.Update(messageResultMsg{operation: 9, request: sent, message: message})
			} else {
				m.messageBusy = "lookup"
				key := request.Key
				switch kind {
				case "lookup-key":
					key = "other"
				case "lookup-record-key":
					message.Key = "other"
				case "lookup-record-body":
					message.Body = "other"
				case "lookup-missing-record":
					message = manager.Message{}
				}
				m.Update(lookupResultMsg{operation: 9, key: key, found: true, message: message})
			}
			if m.pendingMessage == nil || m.pendingMessage.Key != request.Key || m.pendingMessage.Body != request.Body || m.composer == nil || !m.composer.unknown || m.messageBusy != "" {
				t.Fatal("mismatched response lost reviewed request or left controls stalled", kind)
			}
		})
	}
}
func TestDashboardEditorsReceiveBracketedPasteWithoutConfirming(t *testing.T) {
	for _, mode := range []string{"recipient", "body", "ttl", "filter"} {
		t.Run(mode, func(t *testing.T) {
			m, s := fixtureDashboard(t)
			m.inputMode = mode
			m.input.SetValue("")
			m.input.Focus()
			m.Update(tea.PasteMsg{Content: "pasted value\nwith newline"})
			if got := m.input.Value(); got != "pasted value with newline" {
				t.Fatal("bracketed paste lost", mode, got)
			}
			if m.inputMode != mode || len(s.operations) != 0 {
				t.Fatal("paste submitted input or mutated backend")
			}
		})
	}
}
func TestDashboardReconnectReplacesJournalSnapshotAfterSequenceReset(t *testing.T) {
	m, _ := fixtureDashboard(t)
	m.events = []model.Event{{Sequence: 1, Type: "old-event", Entity: "old-host-lease"}}
	m.Update(connectedMsg{session: newTestTransport(), data: snapshot{status: model.Status{Host: "new-host"}}, events: []model.Event{{Sequence: 1, Type: "new-event", Entity: "new-host-lease"}}})
	if len(m.events) != 1 || m.events[0].Type != "new-event" || strings.Contains(m.feedText(100, false), "old-host") {
		t.Fatal("reconnect mixed or suppressed reset journal", m.events)
	}
}
func TestDashboardLeaseInternalErrorRemainsUnknownAndExpiryPreviewAdvances(t *testing.T) {
	m, _ := fixtureDashboard(t)
	l := activeLease("lease")
	m.detail = &l
	m.unknownLease = l.ID
	m.pending = true
	m.Update(leaseResultMsg{request: model.LeaseAction{ID: l.ID, Action: "release"}, err: model.Err("internal_error", "operation failed after partial effects")})
	if m.unknownLease != l.ID || !strings.Contains(m.notice, "Outcome unknown") {
		t.Fatal("internal lease error wrongly proved rejection")
	}
	if m.startReview("release") != nil || m.review != nil {
		t.Fatal("uncertain lease allowed another action without inspection")
	}
	m.acceptDetail(detailMsg{generation: m.detailGeneration, lease: l})
	m.reviewTTL("2h")
	m.timeNow = time.Now()
	first := m.leaseReviewText()
	m.timeNow = m.timeNow.Add(15 * time.Minute)
	second := m.leaseReviewText()
	if first == second || !strings.Contains(second, m.timeNow.Add(2*time.Hour).Local().Format(time.RFC1123)) {
		t.Fatal("accepted-from-now expiry preview stayed fixed")
	}
	// Plain transport failures keep the same gate too.
	m.Update(leaseResultMsg{request: model.LeaseAction{ID: l.ID}, err: errors.New("lost")})
	if !strings.Contains(m.notice, "Outcome unknown") {
		t.Fatal(m.notice)
	}
}
