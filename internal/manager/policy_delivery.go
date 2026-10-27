package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/local"
)

type policyDelivery struct {
	Incident string         `json:"incident"`
	Target   agents.Contact `json:"target"`
}

// A durable inbox is always the fallback. Native delivery is attempted at most
// once per warning: an interrupted or failed CLI may already have delivered.
// Never replay an uncertain invocation into the agent's conversation.
func (m *Manager) policyDeliveryLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-m.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		id, item, body, ok := m.claimPolicyDelivery()
		if !ok {
			continue
		}
		deliveryCtx, done := context.WithTimeout(ctx, 15*time.Second)
		d := agents.Deliver(deliveryCtx, item.Target, fmt.Sprintf("ARM message %s\n%s", id, body))
		done()
		d.Mode, d.Registration = item.Target.Delivery, item.Target.Registration
		_ = m.finishPolicyDelivery(id, d, "done")
	}
}

func (m *Manager) claimPolicyDelivery() (string, policyDelivery, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var item policyDelivery
	if m.closing {
		return "", item, "", false
	}
	var id string
	var b []byte
	if err := m.db.QueryRow(`SELECT message,data FROM policy_outbox WHERE state='queued' ORDER BY rowid LIMIT 1`).Scan(&id, &b); err != nil {
		return "", item, "", false
	}
	if err := json.Unmarshal(b, &item); err != nil {
		return "", item, "", false
	}
	i, err := m.incident(item.Incident)
	if err != nil {
		return "", item, "", false
	}
	l, err := m.get(i.Lease)
	if err != nil {
		return "", item, "", false
	}
	var msg Message
	if err := m.db.QueryRow(`SELECT data FROM messages WHERE id=?`, id).Scan(&b); err != nil {
		return "", item, "", false
	}
	if err := json.Unmarshal(b, &msg); err != nil {
		return "", item, "", false
	}
	c, contactErr := m.contact(item.Target.ID)
	valid := contactErr == nil && c == item.Target && i.State == "warning" && l.State == "active" && m.jobs[l.ID] != nil && len(i.Messages) > 0 && i.Messages[len(i.Messages)-1] == id && i.Deadline != nil && time.Now().Before(*i.Deadline)
	if !valid {
		d := agents.Delivery{State: "superseded", Mode: item.Target.Delivery, Registration: item.Target.Registration, Error: "Warning is no longer current or live recipient authority is unavailable; historical inbox entry retained"}
		_ = m.finishPolicyDeliveryLocked(id, d, "skipped")
		return "", item, "", false
	}
	if _, err := m.db.Exec(`UPDATE policy_outbox SET state='attempting' WHERE message=? AND state='queued'`, id); err != nil {
		return "", item, "", false
	}
	return id, item, msg.Body, true
}

func (m *Manager) finishPolicyDelivery(id string, d agents.Delivery, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.finishPolicyDeliveryLocked(id, d, state)
}
func (m *Manager) finishPolicyDeliveryLocked(id string, d agents.Delivery, state string) error {
	var b []byte
	if err := m.db.QueryRow(`SELECT data FROM messages WHERE id=?`, id).Scan(&b); err != nil {
		return err
	}
	var msg Message
	if err := json.Unmarshal(b, &msg); err != nil {
		return err
	}
	var raw []byte
	if err := m.db.QueryRow(`SELECT data FROM policy_outbox WHERE message=?`, id).Scan(&raw); err != nil {
		return err
	}
	var item policyDelivery
	if err := json.Unmarshal(raw, &item); err != nil {
		return err
	}
	msg.Deliveries[item.Target.ID] = d
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldState string
	if err = tx.QueryRow(`SELECT state FROM policy_outbox WHERE message=?`, id).Scan(&oldState); err != nil {
		return err
	}
	if oldState != "queued" && oldState != "attempting" {
		return nil
	}
	if _, err = tx.Exec(`UPDATE messages SET data=? WHERE id=?`, string(b), id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE policy_outbox SET state=? WHERE message=?`, state, id); err != nil {
		return err
	}
	if err = event(tx, "message.delivery", id, msg); err != nil {
		return err
	}
	if d.State == "responded" && d.Reply != "" {
		reply := Message{ID: local.ID(), Owner: m.owner, OwnerUsername: m.ownerUsername, Sender: item.Target.ID, ReplyTo: id, Audience: "operator", Body: d.Reply, Lease: msg.Lease, Created: time.Now().UTC(), Deliveries: map[string]agents.Delivery{}}
		if err = insertMessage(tx, reply, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Manager) recoverPolicyOutbox() error {
	// recoverDeliveries already marked pending attempts unknown. Queued entries
	// were never invoked and can be examined for supersession by the worker.
	_, err := m.db.Exec(`UPDATE policy_outbox SET state='unknown' WHERE state='attempting'`)
	return err
}
