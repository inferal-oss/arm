package manager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
)

// These selectors pin a registered session within the existing same-user
// authority. They are attribution, not separate agent authentication tokens.
type AgentMessageRequest struct {
	ID           string           `json:"id"`
	Session      string           `json:"session"`
	Registration string           `json:"registration"`
	Key          string           `json:"idempotency_key"`
	Audience     string           `json:"audience,omitempty"`
	Body         string           `json:"body"`
	Lease        string           `json:"lease,omitempty"`
	ReplyTo      string           `json:"reply_to,omitempty"`
	Targets      []agents.Contact `json:"targets"`
}

func agentMessageKey(id, registration, key string) string {
	data, _ := json.Marshal([]string{id, registration, key})
	digest := sha256.Sum256(data)
	return "agent-" + hex.EncodeToString(digest[:])
}

func (m *Manager) agentMessageSourceLocked(id, session, registration string) (agents.Contact, error) {
	if id == "" || registration == "" || session == "" {
		return agents.Contact{}, model.Err("invalid_request", "provide the exact sender contact, session and registration")
	}
	return m.inboxContact(id, session, registration)
}

func (m *Manager) AgentMessage(ctx context.Context, p AgentMessageRequest) (Message, error) {
	if p.Key == "" || len(p.Key) > 128 || p.Body == "" || len(p.Body) > 16384 {
		return Message{}, model.Err("invalid_request", "provide a key of 1 to 128 bytes and message text of 1 to 16384 bytes")
	}
	m.mu.Lock()
	source, err := m.agentMessageSourceLocked(p.ID, p.Session, p.Registration)
	if err == nil && p.ReplyTo != "" {
		if p.Audience != "" || p.Targets != nil {
			err = model.Err("invalid_request", "a reply uses the recorded sender; do not provide a new recipient")
		} else {
			var original Message
			original, err = m.message(p.ReplyTo)
			if err == nil {
				p.Audience, p.Targets = "operator", []agents.Contact{}
				if original.SenderContact != nil {
					p.Audience = original.SenderContact.ID
					p.Targets = []agents.Contact{*original.SenderContact}
				}
				err = m.validateAgentReplyLocked(original, source, p.Audience, p.Targets)
				if p.Lease == "" {
					p.Lease = original.Lease
				}
			}
		}
	}
	m.mu.Unlock()
	if err != nil {
		return Message{}, err
	}
	if p.Audience == "" || p.Audience == "broadcast" || p.Targets == nil || (p.Audience != "operator" && (len(p.Targets) != 1 || p.Targets[0].ID != p.Audience)) || (p.Audience == "operator" && len(p.Targets) != 0) {
		return Message{}, model.Err("invalid_request", "agent messages require one exact recipient or the operator inbox; broadcasts are not supported")
	}
	return m.sendMessage(ctx, MessageRequest{Key: agentMessageKey(source.ID, source.Registration, p.Key), Audience: p.Audience, Body: p.Body, Lease: p.Lease, ReplyTo: p.ReplyTo, Targets: p.Targets, source: &source, agentKey: p.Key})
}

// Caller holds m.mu. Reading a message ID alone never authorizes replying as a
// recipient; the durable inbox entry must name this exact registration.
func (m *Manager) validateAgentReplyLocked(original Message, source agents.Contact, audience string, targets []agents.Contact) error {
	var found int
	err := m.db.QueryRow(`SELECT 1 FROM agent_inbox WHERE recipient=? AND registration=? AND message=?`, source.ID, source.Registration, original.ID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Err("forbidden", "the original message was not addressed to this sender registration")
	}
	if err != nil {
		return err
	}
	if original.SenderContact == nil {
		if audience != "operator" || len(targets) != 0 {
			return model.Err("invalid_request", "this message replies through the operator inbox")
		}
	} else if audience != original.SenderContact.ID || len(targets) != 1 || targets[0] != *original.SenderContact {
		return model.Err("contact_changed", "reply must target the original exact sender; never replace it with a current namesake")
	}
	return nil
}

func (m *Manager) LookupAgentMessage(id, session, registration, key string) (MessageLookup, error) {
	if key == "" || len(key) > 128 {
		return MessageLookup{}, model.Err("invalid_request", "provide the original message key, at most 128 bytes")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := m.agentMessageSourceLocked(id, session, registration)
	if err != nil {
		return MessageLookup{}, err
	}
	result, err := m.lookupMessageLocked(agentMessageKey(id, registration, key))
	if err != nil || !result.Found {
		return result, err
	}
	source := result.Message.SenderContact
	if source == nil || source.ID != id || source.Session != session || source.Registration != registration {
		return MessageLookup{}, model.Err("forbidden", "message belongs to another sender registration")
	}
	return result, nil
}

// Native CLI responses and ack text enter the sender's durable inbox without
// starting another native agent turn. This avoids automatic agent reply loops.
// An explicit agent reply command can separately request native delivery.
func (m *Manager) agentPollReplyLocked(original Message, responder agents.Contact, body, id string, at time.Time) (Message, []agents.Contact) {
	target := *original.SenderContact
	reply := Message{ID: id, Owner: m.owner, OwnerUsername: m.ownerUsername, Sender: "agent:" + responder.ID, SenderContact: &responder, Audience: target.ID, ReplyTo: original.ID, Body: body, Lease: original.Lease, Created: at, Deliveries: map[string]agents.Delivery{}}
	delivery := agents.Delivery{State: "available", Mode: "poll", Registration: target.Registration}
	current, err := m.contact(target.ID)
	if err != nil || current != target {
		delivery.State = "superseded"
		delivery.Error = "original sender registration or settings changed; reply preserved without retargeting"
		reply.Deliveries[target.ID] = delivery
		return reply, nil
	}
	reply.Deliveries[target.ID] = delivery
	return reply, []agents.Contact{target}
}

func (m *Manager) saveAgentNativeReply(msg *Message, responder agents.Contact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delivery := msg.Deliveries[responder.ID]
	reply, targets := m.agentPollReplyLocked(*msg, responder, delivery.Reply, local.ID(), time.Now().UTC())
	if msg.NativeReplies == nil {
		msg.NativeReplies = map[string]string{}
	}
	msg.NativeReplies[responder.ID] = reply.ID
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(msg)
	if err == nil {
		_, err = tx.Exec(`UPDATE messages SET data=? WHERE id=?`, string(raw), msg.ID)
	}
	if err == nil {
		err = event(tx, "message.delivery", msg.ID, msg)
	}
	if err == nil {
		err = insertMessage(tx, reply, targets)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		m.notifyInbox()
	}
	return err
}
