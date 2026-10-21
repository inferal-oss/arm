package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

// MessageRequest records the exact recipient configuration reviewed by an
// operator. Targets is an explicit array (empty for the operator inbox), never
// a request to resolve mutable contact names after confirmation.
type MessageRequest struct {
	Key      string           `json:"idempotency_key"`
	Audience string           `json:"audience"`
	Body     string           `json:"body"`
	Lease    string           `json:"lease,omitempty"`
	ReplyTo  string           `json:"reply_to,omitempty"`
	Targets  []agents.Contact `json:"targets"`
}

func (m *Manager) SendMessage(ctx context.Context, request MessageRequest) (Message, error) {
	if request.Key == "" || len(request.Key) > 128 || request.Audience == "" || request.Body == "" || len(request.Body) > 16384 || request.Targets == nil {
		return Message{}, model.Err("invalid_request", "a key (at most 128 bytes), audience, body (1 to 16384 bytes), and explicit reviewed targets array are required")
	}
	// Canonical target ordering makes the same reviewed set an exact retry.
	request.Targets = slices.Clone(request.Targets)
	slices.SortFunc(request.Targets, func(a, b agents.Contact) int { return strings.Compare(a.ID, b.ID) })
	for i, target := range request.Targets {
		if target.ID == "" || (i > 0 && target.ID == request.Targets[i-1].ID) {
			return Message{}, model.Err("invalid_request", "reviewed targets must have distinct nonempty contact IDs")
		}
	}
	msg, created, err := m.createReviewedMessage(request)
	if err != nil || !created {
		return msg, err
	}
	return m.deliverMessage(ctx, msg, request.Targets)
}

func (m *Manager) message(id string) (Message, error) {
	var data []byte
	var msg Message
	if err := m.db.QueryRow(`SELECT data FROM messages WHERE id=?`, id).Scan(&data); err != nil {
		return msg, err
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return msg, err
	}
	err := m.hydrateReceipts(&msg)
	return msg, err
}

func (m *Manager) createReviewedMessage(request MessageRequest) (Message, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var msg Message
	if m.closing {
		return msg, false, model.Err("backend_stopping", "backend is shutting down")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return msg, false, err
	}
	var previous, messageID string
	err = m.db.QueryRow(`SELECT request,message FROM message_operations WHERE key=?`, request.Key).Scan(&previous, &messageID)
	if err == nil {
		if previous != string(raw) {
			return msg, false, model.Err("idempotency_conflict", "message key was used for a different reviewed request")
		}
		msg, err = m.message(messageID)
		return msg, false, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return msg, false, err
	}
	if request.Lease != "" {
		if _, err = m.get(request.Lease); err != nil {
			return msg, false, err
		}
	}
	if request.ReplyTo != "" {
		original, err := m.message(request.ReplyTo)
		if err != nil {
			return msg, false, model.Err("invalid_request", "reply_to must identify an existing operator-inbox message")
		}
		if original.Audience != "operator" || original.Sender != request.Audience {
			return msg, false, model.Err("invalid_request", "reply audience must match the original message sender")
		}
	}
	rows, err := m.db.Query(`SELECT data FROM agents ORDER BY id`)
	if err != nil {
		return msg, false, err
	}
	targets := make([]agents.Contact, 0)
	for rows.Next() {
		var data []byte
		var target agents.Contact
		if err = rows.Scan(&data); err == nil {
			err = json.Unmarshal(data, &target)
		}
		if err != nil {
			rows.Close()
			return msg, false, err
		}
		if request.Audience != "operator" && (request.Audience == "broadcast" || request.Audience == target.ID) {
			targets = append(targets, target)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return msg, false, err
	}
	if !slices.Equal(request.Targets, targets) {
		return msg, false, model.Err("contact_changed", "recipient identity, delivery settings or broadcast membership changed; review the current targets before sending")
	}
	if request.Audience != "operator" && request.Audience != "broadcast" && len(targets) != 1 {
		return msg, false, model.Err("not_found", "agent contact is not registered")
	}
	sender := "operator"
	if request.Audience == "operator" {
		sender = "local-client"
	}
	msg = Message{ID: local.ID(), Sender: sender, Audience: request.Audience, Body: request.Body, Lease: request.Lease, ReplyTo: request.ReplyTo, Key: request.Key, Created: time.Now().UTC(), Deliveries: make(map[string]agents.Delivery)}
	for _, target := range targets {
		state := "pending"
		if target.Delivery == "poll" {
			state = "available"
		}
		msg.Deliveries[target.ID] = agents.Delivery{State: state, Mode: target.Delivery, Registration: target.Registration}
	}
	tx, err := m.db.Begin()
	if err != nil {
		return msg, false, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO message_operations(key,request,message) VALUES(?,?,?)`, request.Key, string(raw), msg.ID); err != nil {
		return msg, false, err
	}
	if err = insertMessage(tx, msg, targets); err != nil {
		return msg, false, err
	}
	if err = tx.Commit(); err != nil {
		return msg, false, err
	}
	m.notifyInbox()
	return msg, true, nil
}
