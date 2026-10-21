package manager

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
)

type InboxRequest struct {
	ID           string        `json:"id"`
	Session      string        `json:"session"`
	Registration string        `json:"registration,omitempty"`
	After        string        `json:"after,omitempty"`
	Wait         time.Duration `json:"wait_ns,omitempty"`
	Pending      bool          `json:"pending,omitempty"`
}
type InboxItem struct {
	Cursor  string  `json:"cursor"`
	Message Message `json:"message"`
}
type InboxPage struct {
	Host         string      `json:"host"`
	ID           string      `json:"id"`
	Session      string      `json:"session"`
	Registration string      `json:"registration"`
	Cursor       string      `json:"cursor"`
	Items        []InboxItem `json:"items"`
	More         bool        `json:"more"`
}
type ReceiptRequest struct {
	ID           string `json:"id"`
	Session      string `json:"session"`
	Registration string `json:"registration"`
	Message      string `json:"message"`
	Acknowledge  bool   `json:"acknowledge"`
	Text         string `json:"text,omitempty"`
}
type inboxCursor struct {
	Host         string `json:"host"`
	Registration string `json:"registration"`
	Sequence     int64  `json:"sequence"`
}

func cursor(host, registration string, seq int64) string {
	b, _ := json.Marshal(inboxCursor{host, registration, seq})
	return base64.RawURLEncoding.EncodeToString(b)
}

func (m *Manager) contact(id string) (agents.Contact, error) {
	var c agents.Contact
	var b []byte
	err := m.db.QueryRow(`SELECT data FROM agents WHERE id=?`, id).Scan(&b)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	return c, err
}
func (m *Manager) Contact(id string) (agents.Contact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.contact(id)
	if errors.Is(err, sql.ErrNoRows) {
		err = model.Err("not_found", "agent contact is not registered")
	}
	return c, err
}
func (m *Manager) migrateContacts() error {
	cs, err := m.Contacts()
	if err != nil {
		return err
	}
	for _, c := range cs {
		if c.Registration == "" {
			c.Registration = local.ID()
			if c.Delivery == "" {
				c.Delivery = "native"
			}
			b, err := json.Marshal(c)
			if err != nil {
				return err
			}
			tx, err := m.db.Begin()
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE agents SET data=? WHERE id=?`, string(b), c.ID); err == nil {
				err = event(tx, "agent.registration_migrated", c.ID, c)
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				tx.Rollback()
				return err
			}
		}
	}
	return nil
}

// Caller holds m.mu, so observing a page and its wakeup channel is atomic.
func (m *Manager) notifyInbox() { close(m.inboxChanged); m.inboxChanged = make(chan struct{}) }
func (m *Manager) inboxContact(id, session, registration string) (agents.Contact, error) {
	c, err := m.contact(id)
	if errors.Is(err, sql.ErrNoRows) {
		return c, model.Err("not_found", "agent contact is not registered")
	}
	if err != nil {
		return c, err
	}
	if session == "" || session != c.Session {
		return c, model.Err("contact_changed", "the exact registered session is required; recheck the contact identity")
	}
	if registration != "" && registration != c.Registration {
		return c, model.Err("contact_changed", "contact registration changed; explicitly select the new recipient")
	}
	return c, nil
}
func (m *Manager) createMessage(msg Message, targets []agents.Contact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return model.Err("backend_stopping", "backend is shutting down")
	}
	for _, c := range targets {
		current, err := m.contact(c.ID)
		if err != nil {
			return err
		}
		if current != c {
			return model.Err("contact_changed", "recipient changed during message creation; retry explicitly")
		}
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = insertMessage(tx, msg, targets); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	m.notifyInbox()
	return nil
}

// The caller validates contacts and holds the manager lock. Incident warnings
// use this same insert inside their state transition transaction.
func insertMessage(tx *sql.Tx, msg Message, targets []agents.Contact) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO messages(id,data) VALUES(?,?)`, msg.ID, string(b)); err != nil {
		return err
	}
	for _, c := range targets {
		if _, err = tx.Exec(`INSERT INTO agent_inbox(recipient,registration,message,receipt) VALUES(?,?,?,'{}')`, c.ID, c.Registration, msg.ID); err != nil {
			return err
		}
	}
	return event(tx, "message.created", msg.ID, msg)
}
func (m *Manager) hydrateReceipts(msg *Message) error {
	for id, d := range msg.Deliveries {
		if d.Registration != "" {
			var raw []byte
			err := m.db.QueryRow(`SELECT receipt FROM agent_inbox WHERE recipient=? AND registration=? AND message=?`, id, d.Registration, msg.ID).Scan(&raw)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			var r agents.Receipt
			if err = json.Unmarshal(raw, &r); err != nil {
				return err
			}
			d.Receipt = &r
			msg.Deliveries[id] = d
		}
	}
	return nil
}
func (m *Manager) inboxPage(ctx context.Context, p InboxRequest) (InboxPage, <-chan struct{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := InboxPage{Host: m.host, ID: p.ID, Session: p.Session, Items: []InboxItem{}}
	if m.closing {
		return result, nil, model.Err("backend_stopping", "backend is shutting down")
	}
	c, err := m.inboxContact(p.ID, p.Session, p.Registration)
	if err != nil {
		return result, nil, err
	}
	result.Registration = c.Registration
	var after int64
	if p.After != "" {
		if len(p.After) > 1024 {
			return result, nil, model.Err("invalid_cursor", "inbox cursor is too long")
		}
		b, err := base64.RawURLEncoding.DecodeString(p.After)
		var v inboxCursor
		if err != nil || json.Unmarshal(b, &v) != nil || v.Host != m.host || v.Registration != c.Registration || v.Sequence < 0 {
			return result, nil, model.Err("invalid_cursor", "cursor belongs to another host/registration or is malformed")
		}
		after = v.Sequence
		var last int64
		if err = m.db.QueryRow(`SELECT COALESCE(MAX(sequence),0) FROM agent_inbox`).Scan(&last); err != nil {
			return result, nil, err
		}
		if after > last {
			return result, nil, model.Err("invalid_cursor", "cursor is ahead of this inbox history")
		}
	}
	query := `SELECT i.sequence,m.data,i.receipt FROM agent_inbox i JOIN messages m ON m.id=i.message WHERE i.recipient=? AND i.registration=? AND i.sequence>?`
	if p.Pending {
		query += ` AND json_extract(i.receipt,'$.acknowledged_at') IS NULL`
	}
	query += ` ORDER BY i.sequence LIMIT 65`
	rows, err := m.db.QueryContext(ctx, query, c.ID, c.Registration, after)
	if err != nil {
		return result, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var raw, receipt []byte
		if err = rows.Scan(&seq, &raw, &receipt); err != nil {
			return result, nil, err
		}
		var msg Message
		var r agents.Receipt
		if err = json.Unmarshal(raw, &msg); err != nil {
			return result, nil, err
		}
		if err = json.Unmarshal(receipt, &r); err != nil {
			return result, nil, err
		}
		if len(result.Items) == 64 {
			result.More = true
			break
		}
		after = seq
		d := msg.Deliveries[c.ID]
		d.Receipt = &r
		msg.Deliveries = map[string]agents.Delivery{c.ID: d}
		result.Items = append(result.Items, InboxItem{Cursor: cursor(m.host, c.Registration, seq), Message: msg})
	}
	if err = rows.Err(); err != nil {
		return result, nil, err
	}
	result.Cursor = cursor(m.host, c.Registration, after)
	return result, m.inboxChanged, nil
}
func (m *Manager) AgentInbox(ctx context.Context, p InboxRequest) (InboxPage, error) {
	if p.Wait < 0 || p.Wait > 20*time.Second {
		return InboxPage{}, model.Err("invalid_request", "inbox wait must be between zero and 20 seconds")
	}
	ctx, cancel := context.WithTimeout(ctx, p.Wait+3*time.Second)
	defer cancel()
	select {
	case m.inboxSlots <- struct{}{}:
		defer func() { <-m.inboxSlots }()
	default:
		return InboxPage{}, model.Err("inbox_busy", "too many active inbox requests; retry later")
	}
	timer := time.NewTimer(p.Wait)
	defer timer.Stop()
	for {
		page, changed, err := m.inboxPage(ctx, p)
		if err != nil || len(page.Items) > 0 || p.Wait == 0 || page.More {
			return page, err
		}
		p.After = page.Cursor
		p.Registration = page.Registration
		select {
		case <-ctx.Done():
			return page, ctx.Err()
		case <-m.stop:
			return page, model.Err("backend_stopping", "backend stopped")
		case <-timer.C:
			return page, nil
		case <-changed:
		}
	}
}
func (m *Manager) AgentReceipt(p ReceiptRequest) (agents.Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var r agents.Receipt
	if m.closing {
		return r, model.Err("backend_stopping", "backend is shutting down")
	}
	if len(p.Text) > 4096 || (!p.Acknowledge && p.Text != "") {
		return r, model.Err("invalid_request", "acknowledgement text is limited to 4096 bytes and requires acknowledge")
	}
	c, err := m.inboxContact(p.ID, p.Session, p.Registration)
	if err != nil {
		return r, err
	}
	var raw []byte
	err = m.db.QueryRow(`SELECT receipt FROM agent_inbox WHERE recipient=? AND registration=? AND message=?`, c.ID, c.Registration, p.Message).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return r, model.Err("not_found", "message was not addressed to this registration")
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(raw, &r); err != nil {
		return r, err
	}
	if r.Acknowledged != nil {
		if p.Acknowledge && p.Text != r.Text {
			return r, model.Err("receipt_conflict", "acknowledgement was already recorded with different text")
		}
		return r, nil
	}
	if r.Received != nil && !p.Acknowledge {
		return r, nil
	}
	now := time.Now().UTC()
	if r.Received == nil {
		r.Received = &now
	}
	if p.Acknowledge {
		r.Acknowledged = &now
		r.Text = p.Text
		if p.Text != "" {
			r.ReplyID = local.ID()
		}
	}
	tx, err := m.db.Begin()
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	b, err := json.Marshal(r)
	if err != nil {
		return r, err
	}
	if _, err = tx.Exec(`UPDATE agent_inbox SET receipt=? WHERE recipient=? AND registration=? AND message=?`, string(b), c.ID, c.Registration, p.Message); err != nil {
		return r, err
	}
	if r.ReplyID != "" {
		var original []byte
		var msg Message
		if err = tx.QueryRow(`SELECT data FROM messages WHERE id=?`, p.Message).Scan(&original); err != nil {
			return r, err
		}
		if err = json.Unmarshal(original, &msg); err != nil {
			return r, err
		}
		reply := Message{ID: r.ReplyID, Sender: c.ID, ReplyTo: p.Message, Audience: "operator", Body: p.Text, Lease: msg.Lease, Created: now, Deliveries: map[string]agents.Delivery{}}
		b, _ := json.Marshal(reply)
		if _, err = tx.Exec(`INSERT INTO messages(id,data) VALUES(?,?)`, reply.ID, string(b)); err != nil {
			return r, err
		}
		if err = event(tx, "message.received", reply.ID, reply); err != nil {
			return r, err
		}
	}
	if err = event(tx, "message.receipt", p.Message, map[string]any{"recipient": c.ID, "registration": c.Registration, "receipt": r}); err != nil {
		return r, err
	}
	if err = tx.Commit(); err != nil {
		return r, err
	}
	m.notifyInbox()
	return r, nil
}
