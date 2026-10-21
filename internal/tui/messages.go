package tui

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/model"
)

type messageComposer struct {
	request manager.MessageRequest
	review  bool
	unknown bool
}
type messageResultMsg struct {
	operation    uint64
	request      manager.MessageRequest
	message      manager.Message
	err          error
	priorUnknown bool
}
type lookupResultMsg struct {
	operation uint64
	key       string
	found     bool
	message   manager.Message
	err       error
}

func (m *dashboardModel) beginCompose(broadcast bool) tea.Cmd {
	if !m.connected || m.pendingMessage != nil {
		m.notice = "Reconnect or inspect the pending message before composing another."
		return nil
	}
	m.feedFocus = false
	m.feedOpen = false
	m.messageOperation++
	m.composer = &messageComposer{request: manager.MessageRequest{Key: local.ID()}}
	if broadcast {
		return m.composeRecipient("broadcast")
	}
	m.inputMode = "recipient"
	m.input.SetValue("")
	return m.input.Focus()
}
func (m *dashboardModel) composeRecipient(audience string) tea.Cmd {
	if m.composer == nil {
		return nil
	}
	audience = strings.TrimSpace(audience)
	targets := make([]agents.Contact, 0)
	for _, c := range m.data.contacts {
		if audience == "broadcast" || audience == c.ID {
			targets = append(targets, c)
		}
	}
	if len(targets) == 0 {
		m.notice = "Choose a currently registered agent ID; no recipients matched."
		return nil
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	m.composer.request.Audience = audience
	m.composer.request.Targets = targets
	m.inputMode = "body"
	m.input.SetValue(m.composer.request.Body)
	return m.input.Focus()
}
func (m *dashboardModel) composeBody(body string) tea.Cmd {
	if m.composer == nil {
		return nil
	}
	if strings.TrimSpace(body) == "" || len(body) > 16384 {
		m.notice = "Message must contain 1 to 16384 bytes."
		return nil
	}
	m.composer.request.Body = body
	m.composer.review = true
	m.viewport.GotoTop()
	return nil
}
func (m *dashboardModel) replyMessage() tea.Cmd {
	if m.view != 3 || len(m.data.messages) == 0 || m.selected >= len(m.data.messages) {
		return nil
	}
	message := m.data.messages[m.selected]
	if message.Audience != "operator" {
		m.notice = "Reply is available for messages sent to the operator."
		return nil
	}
	cmd := m.beginCompose(false)
	if m.composer == nil {
		return cmd
	}
	m.composer.request.Lease = message.Lease
	m.composer.request.ReplyTo = message.ID
	return m.composeRecipient(message.Sender)
}
func (m *dashboardModel) composeKey(key string) tea.Cmd {
	if scrollReview(&m.viewport, key) {
		return nil
	}
	if m.messageBusy != "" {
		return nil
	}
	if key == "e" && !m.composer.review {
		m.composer.request.Key = local.ID()
		m.inputMode = "recipient"
		m.input.SetValue(m.composer.request.Audience)
		return m.input.Focus()
	}
	if key == "esc" || key == "n" {
		if m.composer.unknown {
			m.notice = "Unknown request retained: i inspect, y explicit same-key retry, or q exit and print the exact request."
			return nil
		}
		m.composer = nil
		return nil
	}
	if m.composer.unknown && key == "i" && m.connected {
		if m.pendingMessage == nil {
			return nil
		}
		request := *m.pendingMessage
		m.messageOperation++
		operation := m.messageOperation
		m.messageBusy = "lookup"
		session, ctx := m.session, m.ctx
		return func() tea.Msg {
			var result struct {
				Found   bool            `json:"found"`
				Message manager.Message `json:"message"`
			}
			err := session.Call(ctx, "message_lookup", map[string]string{"idempotency_key": request.Key}, &result)
			return lookupResultMsg{operation: operation, key: request.Key, found: result.Found, message: result.Message, err: err}
		}
	}
	if key != "y" || !m.composer.review || !m.connected {
		return nil
	}
	request := m.composer.request
	priorUnknown := m.composer.unknown
	m.messageOperation++
	operation := m.messageOperation
	m.messageBusy = "send"
	m.pendingMessage = &request
	m.composer.unknown = false
	m.notice = "Sending reviewed message; no automatic retry."
	session, ctx := m.session, m.ctx
	return func() tea.Msg {
		var result manager.Message
		err := session.Call(ctx, "message_send", request, &result)
		return messageResultMsg{operation: operation, request: request, message: result, err: err, priorUnknown: priorUnknown}
	}
}
func (m *dashboardModel) acceptMessageResult(msg messageResultMsg) tea.Cmd {
	if msg.operation != m.messageOperation || m.messageBusy != "send" || m.pendingMessage == nil {
		return nil
	}
	if !reflect.DeepEqual(msg.request, *m.pendingMessage) {
		m.messageBusy = ""
		m.retainUnknownMessage("Send response did not match the reviewed request. Inspect the retained key before another action.")
		return nil
	}
	m.messageBusy = ""
	m.dirty = true
	if msg.err == nil {
		if !reviewedMessageMatches(msg.request, msg.message) {
			m.retainUnknownMessage("Send response did not identify the reviewed message. Inspect the retained key before another action.")
			return nil
		}
		m.notice = "Message recorded: " + msg.message.ID
		m.pendingMessage = nil
		m.composer = nil
		return nil
	}
	var api *model.Error
	if errors.As(msg.err, &api) && !msg.priorUnknown && definiteMessageRejection(api.Code) {
		m.pendingMessage = nil
		m.notice = "Message rejected: " + api.Error() + ". Draft retained; press e to edit and review current recipients."
		m.composer = &messageComposer{request: msg.request}
		return nil
	}
	m.retainUnknownMessage("Message outcome unknown. Inspect its key before retrying; only an explicit retry reuses this exact request/key.")
	return nil
}

func reviewedMessageMatches(request manager.MessageRequest, message manager.Message) bool {
	return request.Key != "" && message.Key == request.Key && message.ID != "" && message.Body == request.Body && message.Audience == request.Audience && message.Lease == request.Lease && message.ReplyTo == request.ReplyTo
}
func (m *dashboardModel) retainUnknownMessage(notice string) {
	if m.pendingMessage == nil {
		return
	}
	m.composer = &messageComposer{request: *m.pendingMessage, review: true, unknown: true}
	m.notice = notice
}

func definiteMessageRejection(code string) bool {
	switch code {
	case "invalid_request", "contact_changed", "not_found", "invalid_contact":
		return true
	}
	return false
}
func (m *dashboardModel) acceptLookup(msg lookupResultMsg) tea.Cmd {
	if msg.operation != m.messageOperation || m.messageBusy != "lookup" || m.pendingMessage == nil {
		return nil
	}
	m.messageBusy = ""
	if msg.key != m.pendingMessage.Key {
		m.retainUnknownMessage("Lookup response did not match the retained key. The original outcome remains unknown.")
		return nil
	}
	if msg.err != nil {
		m.notice = "Inspection failed: " + msg.err.Error()
		return nil
	}
	if msg.found {
		if !reviewedMessageMatches(*m.pendingMessage, msg.message) {
			m.retainUnknownMessage("Lookup returned a different or incomplete message. The original outcome remains unknown; inspect the retained key.")
			return nil
		}
		m.notice = "Message found: " + msg.message.ID + ". Latest delivery receipts are in its record."
		m.pendingMessage = nil
		m.composer = nil
		m.record, _ = newRecord(msg.message, "Message delivery")
		m.viewport.GotoTop()
		m.dirty = true
	} else {
		m.notice = "No message found at this instant. An in-flight original may still commit. y explicitly retries the exact same request/key; no duplicate native delivery is attempted."
	}
	return nil
}
func (m *dashboardModel) messageReviewText() string {
	c := m.composer
	if c == nil {
		return ""
	}
	var out strings.Builder
	heading := "COMPOSE MESSAGE"
	if c.review {
		heading = "REVIEW MESSAGE"
	}
	out.WriteString(heading + "\n\nAudience: " + safe(c.request.Audience) + "\nKey: " + safe(c.request.Key) + "\n\n")
	for _, target := range c.request.Targets {
		fmt.Fprintf(&out, "%s · %s · %s\nSession: %s\nRegistration: %s\nWorking directory: %s\nConfig: %s\nRemote endpoint: %s\nIdle: %t\n\n", safe(target.ID), safe(target.Adapter), safe(target.Delivery), safe(target.Session), safe(target.Registration), safe(target.CWD), safe(target.ConfigDir), safe(target.Remote), target.Idle)
	}
	if c.request.ReplyTo != "" {
		out.WriteString("Reply to: " + safe(c.request.ReplyTo) + "\n")
	}
	if c.request.Lease != "" {
		out.WriteString("Lease: " + safe(c.request.Lease) + "\n")
	}
	out.WriteString("\nBody:\n" + safe(c.request.Body) + "\n\n")
	if m.messageBusy == "lookup" {
		out.WriteString("INSPECTING · wait for this lookup before retrying or inspecting again. Original request/key remain retained.")
	} else if m.messageBusy == "send" {
		out.WriteString("SENDING · no automatic retries. Closing leaves delivery outcome uncertain.")
	} else if c.unknown {
		out.WriteString("UNKNOWN OUTCOME · i inspect key · y explicitly retry exact request/key\nAn absent lookup result does not prove the first request cannot commit.")
	} else if c.review {
		out.WriteString("y send exactly this request · n/Esc keep unsent\nNative delivery may inject into an active agent session. Poll recipients receive a durable inbox entry.")
	} else {
		out.WriteString("e edit draft and refresh recipient review · n/Esc discard draft")
	}
	return out.String()
}
