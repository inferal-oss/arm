package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

type Message struct {
	ID         string                     `json:"id"`
	Sender     string                     `json:"sender"`
	ReplyTo    string                     `json:"reply_to,omitempty"`
	Audience   string                     `json:"audience"`
	Body       string                     `json:"body"`
	Lease      string                     `json:"lease,omitempty"`
	Created    time.Time                  `json:"created_at"`
	Deliveries map[string]agents.Delivery `json:"deliveries"`
}

func (m *Manager) Register(c agents.Contact) error {
	if e := c.Validate(); e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, _ := json.Marshal(c)
	tx, e := m.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`INSERT INTO agents VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, c.ID, string(b)); e != nil {
		return e
	}
	if e = event(tx, "agent.registered", c.ID, c); e != nil {
		return e
	}
	return tx.Commit()
}
func (m *Manager) Contacts() ([]agents.Contact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, e := m.db.Query(`SELECT data FROM agents ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []agents.Contact{}
	for rows.Next() {
		var b []byte
		var c agents.Contact
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &c); e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (m *Manager) saveMessage(msg Message, kind string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, e := json.Marshal(msg)
	if e != nil {
		return e
	}
	tx, e := m.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`INSERT INTO messages VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, msg.ID, string(b)); e != nil {
		return e
	}
	if e = event(tx, kind, msg.ID, msg); e != nil {
		return e
	}
	return tx.Commit()
}
func (m *Manager) Message(ctx context.Context, audience, body, lease string) (Message, error) {
	sender := "operator"
	if audience == "operator" {
		sender = "local-client"
	}
	msg := Message{ID: local.ID(), Sender: sender, Audience: audience, Body: body, Lease: lease, Created: time.Now().UTC(), Deliveries: map[string]agents.Delivery{}}
	if body == "" || len(body) > 16384 {
		return msg, model.Err("invalid_request", "message body must contain 1 to 16384 bytes")
	}
	if lease != "" {
		if _, e := m.Inspect(lease); e != nil {
			return msg, e
		}
	}
	cs, e := m.Contacts()
	if e != nil {
		return msg, e
	}
	targets := []agents.Contact{}
	if audience != "operator" {
		for _, c := range cs {
			if audience == "broadcast" || audience == c.ID {
				targets = append(targets, c)
				msg.Deliveries[c.ID] = agents.Delivery{State: "pending"}
			}
		}
		if audience != "broadcast" && len(targets) == 0 {
			return msg, model.Err("not_found", "agent contact is not registered")
		}
	}
	if e = m.saveMessage(msg, "message.created"); e != nil {
		return msg, e
	}
	for _, c := range targets {
		msg.Deliveries[c.ID] = agents.Deliver(ctx, c, fmt.Sprintf("ARM message %s\n%s", msg.ID, body))
		if e = m.saveMessage(msg, "message.delivery"); e != nil {
			return msg, e
		}
		if delivery := msg.Deliveries[c.ID]; delivery.State == "responded" && delivery.Reply != "" {
			reply := Message{ID: local.ID(), Sender: c.ID, ReplyTo: msg.ID, Audience: "operator", Body: delivery.Reply, Lease: lease, Created: time.Now().UTC(), Deliveries: map[string]agents.Delivery{}}
			if e = m.saveMessage(reply, "message.received"); e != nil {
				return msg, e
			}
		}
	}
	return msg, nil
}

func (m *Manager) recoverDeliveries() error {
	rows, e := m.db.Query(`SELECT data FROM messages`)
	if e != nil {
		return e
	}
	pending := []Message{}
	for rows.Next() {
		var b []byte
		var msg Message
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return e
		}
		if e = json.Unmarshal(b, &msg); e != nil {
			rows.Close()
			return e
		}
		changed := false
		for id, d := range msg.Deliveries {
			if d.State == "pending" {
				msg.Deliveries[id] = agents.Delivery{State: "unknown", Error: "delivery was interrupted; verify the recipient before resending"}
				changed = true
			}
		}
		if changed {
			pending = append(pending, msg)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, msg := range pending {
		if e = m.saveMessage(msg, "message.delivery_unknown"); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) Messages() ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, e := m.db.Query(`SELECT data FROM messages ORDER BY rowid DESC LIMIT 100`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var b []byte
		var msg Message
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &msg); e != nil {
			return nil, e
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}
