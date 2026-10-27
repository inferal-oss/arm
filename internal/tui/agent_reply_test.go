package tui

import (
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/manager"
)

func TestOperatorReplyToAgentMessagePinsRecordedSender(t *testing.T) {
	m, transport := fixtureDashboard(t)
	contact := agents.Contact{ID: "source", Session: "session", Registration: "original", Adapter: "generic", Delivery: "poll", CWD: "/work"}
	m.view = 3
	m.data.contacts = []agents.Contact{contact}
	m.data.messages = []manager.Message{{ID: "incoming", Sender: "agent:source", SenderContact: &contact, Audience: "operator", Body: "Question", Lease: "lease"}}
	m.replyMessage()
	if m.composer == nil || m.composer.request.Audience != contact.ID || m.composer.request.ReplyTo != "incoming" || len(m.composer.request.Targets) != 1 || m.composer.request.Targets[0] != contact || m.composer.request.Lease != "lease" {
		t.Fatalf("reply lost exact sender: %+v", m.composer)
	}
	if len(transport.operations) != 0 {
		t.Fatal("opening reply sent a message")
	}
	m.composer = nil
	m.data.contacts[0].Registration = "replacement"
	m.replyMessage()
	if m.composer != nil || !strings.Contains(m.notice, "cannot be retargeted") {
		t.Fatalf("replacement reply offered: %+v %s", m.composer, m.notice)
	}
}
