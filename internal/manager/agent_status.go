package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"getarm.org/internal/agents"
	"getarm.org/internal/model"
)

type StatusContribution struct {
	ID      string
	Body    string
	Created time.Time
}
type StatusEvidence struct {
	Original Message
	Replies  []StatusContribution
}

func (m *Manager) HostIdentity() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.host, nil
}

// The existing inbox/contact signal invalidates a snapshot; it proves no exit.
func (m *Manager) StatusContactSnapshot(target agents.Contact) (<-chan struct{}, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.contact(target.ID)
	return m.inboxChanged, err == nil && current == target
}

// AgentStatusEvidence reads only indexed original/reply operation keys. It does
// not scan the message table or treat display labels/body text as sender proof.
func (m *Manager) AgentStatusEvidence(ctx context.Context, key, requestID string, target agents.Contact) (StatusEvidence, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	var out StatusEvidence
	var raw, messageID string
	err := m.db.QueryRowContext(ctx, `SELECT substr(request,1,65537),message FROM message_operations WHERE key=?`, key).Scan(&raw, &messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var request struct {
		MessageRequest
		Source *agents.Contact `json:"sender_contact"`
	}
	if len(raw) > 65536 || json.Unmarshal([]byte(raw), &request) != nil || request.Source != nil || request.Audience != target.ID || len(request.Targets) != 1 || request.Targets[0] != target {
		return out, model.Err("status_evidence_mismatch", "original status message does not match the exact operator-reviewed recipient")
	}
	read := func(id string) (Message, error) {
		var text string
		var msg Message
		if e := m.db.QueryRowContext(ctx, `SELECT substr(data,1,131073) FROM messages WHERE id=?`, id).Scan(&text); e != nil {
			return msg, e
		}
		if len(text) > 131072 || json.Unmarshal([]byte(text), &msg) != nil {
			return msg, model.Err("status_evidence_bounds", "original message evidence is malformed or exceeds its bound")
		}
		return msg, nil
	}
	out.Original, err = read(messageID)
	if err != nil {
		return out, err
	}
	if out.Original.Sender != "operator" || out.Original.SenderContact != nil || out.Original.Audience != target.ID {
		return out, model.Err("status_evidence_mismatch", "original sender or audience differs")
	}
	if err = m.hydrateReceipts(&out.Original); err != nil {
		return out, err
	}
	delivery, ok := out.Original.Deliveries[target.ID]
	if !ok || delivery.Registration != target.Registration {
		return out, model.Err("status_evidence_mismatch", "original delivery registration differs")
	}
	if delivery.Reply != "" {
		out.Replies = append(out.Replies, StatusContribution{messageID + ":native", delivery.Reply, out.Original.Created})
	}
	if delivery.Receipt != nil && delivery.Receipt.Acknowledged != nil && delivery.Receipt.Text != "" {
		out.Replies = append(out.Replies, StatusContribution{messageID + ":ack", delivery.Receipt.Text, *delivery.Receipt.Acknowledged})
	}
	var replyID string
	err = m.db.QueryRowContext(ctx, `SELECT message FROM message_operations WHERE key=?`, agentMessageKey(target.ID, target.Registration, "status-reply:"+requestID)).Scan(&replyID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	reply, err := read(replyID)
	if err != nil {
		return out, err
	}
	if reply.ReplyTo != messageID || reply.SenderContact == nil || *reply.SenderContact != target || reply.Audience != "operator" {
		return out, model.Err("status_evidence_mismatch", "reply is not from the original exact recipient to this request")
	}
	out.Replies = append(out.Replies, StatusContribution{reply.ID, reply.Body, reply.Created})
	return out, nil
}
