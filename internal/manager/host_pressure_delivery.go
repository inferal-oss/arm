package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/pressure"
)

type hostPressureDelivery struct {
	Target   agents.Contact    `json:"target"`
	Epoch    string            `json:"epoch"`
	Reason   pressure.Resource `json:"reason"`
	Incident uint64            `json:"incident"`
	Lease    string            `json:"lease"`
}

func (m *Manager) hostPressureDeliveryLoop() {
	ctx, cancel := context.WithCancel(context.Background())
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-m.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	// Manager.Close joins this loop. Join its stop watcher before the loop can
	// return, including when a future exit path is added independently of stop.
	defer func() { cancel(); <-watcherDone }()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		id, item, body, ok := m.claimHostPressureDelivery()
		if !ok {
			continue
		}
		// A drain may begin after claim releases Manager.mu. The outbox row
		// remains durable; an attempt not yet dispatched is superseded.
		m.mu.Lock()
		draining := m.draining || m.closing
		m.mu.Unlock()
		if draining {
			d := agents.Delivery{State: "superseded", Mode: item.Target.Delivery, Registration: item.Target.Registration,
				Error: "Backend began draining before native delivery; historical inbox entry retained"}
			_ = m.finishHostPressureDelivery(id, d, "skipped")
			continue
		}
		attempt, done := context.WithTimeout(ctx, 15*time.Second)
		d := agents.DeliverWithStartFence(attempt, item.Target, fmt.Sprintf("ARM message %s\n%s", id, body), func(start func() error) (bool, error) {
			return m.hostPressureStartFence(id, item, start)
		})
		done()
		d.Mode, d.Registration = item.Target.Delivery, item.Target.Registration
		state := "done"
		if d.State == "superseded" {
			state = "skipped"
		}
		_ = m.finishHostPressureDelivery(id, d, state)
	}
}

func (m *Manager) hostPressureStartFence(id string, item hostPressureDelivery, start func() error) (bool, error) {
	m.pressureLaunchMu.Lock()
	defer m.pressureLaunchMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.draining || m.closing || !m.currentHostPressureDeliveryLocked(id, item, "attempting") {
		return false, nil
	}
	// Hold Manager.mu only through command start. Contact, lease, incident and
	// outbox mutations use this same lock, so none can cross this boundary.
	return true, start()
}

// Caller holds m.mu. The saved outbox item is only a candidate: replay,
// replacement, release and incident recovery must be checked again at Start.
func (m *Manager) currentHostPressureDeliveryLocked(id string, item hostPressureDelivery, state string) bool {
	if id == "" || item.Epoch != m.pressureEpoch || item.Target.ID == "" || item.Target.Delivery != "native" || item.Lease == "" {
		return false
	}
	var outboxState string
	var raw []byte
	if err := m.db.QueryRow(`SELECT state,data FROM host_pressure_outbox WHERE message=?`, id).Scan(&outboxState, &raw); err != nil || outboxState != state {
		return false
	}
	var saved hostPressureDelivery
	if json.Unmarshal(raw, &saved) != nil || saved != item {
		return false
	}
	var msg Message
	if err := m.db.QueryRow(`SELECT data FROM messages WHERE id=?`, id).Scan(&raw); err != nil || json.Unmarshal(raw, &msg) != nil {
		return false
	}
	delivery, present := msg.Deliveries[item.Target.ID]
	if msg.ID != id || msg.Sender != "arm-host-pressure" || msg.Audience != item.Target.ID || msg.Lease != item.Lease || !present ||
		delivery.State != "pending" || delivery.Mode != "native" || delivery.Registration != item.Target.Registration {
		return false
	}
	c, err := m.contact(item.Target.ID)
	if err != nil || c != item.Target {
		return false
	}
	e := m.pressureState.Resources[item.Reason]
	if !e.Active || e.Incident != item.Incident || item.Incident == 0 {
		return false
	}
	l, err := m.get(item.Lease)
	if err != nil || (l.State != "active" && l.State != "provisioning") || l.Host != m.host || l.Owner != m.owner || l.Request.Agent != c.ID {
		return false
	}
	remaining, err := m.leaseRemaining(l)
	return err == nil && remaining > 0
}

func (m *Manager) claimHostPressureDelivery() (string, hostPressureDelivery, string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var item hostPressureDelivery
	if m.closing || m.draining {
		return "", item, "", false
	}
	var id string
	var raw []byte
	if err := m.db.QueryRow(`SELECT message,data FROM host_pressure_outbox WHERE state='queued' ORDER BY rowid LIMIT 1`).Scan(&id, &raw); err != nil {
		return "", item, "", false
	}
	if json.Unmarshal(raw, &item) != nil {
		return "", item, "", false
	}
	if !m.currentHostPressureDeliveryLocked(id, item, "queued") {
		d := agents.Delivery{State: "superseded", Mode: item.Target.Delivery, Registration: item.Target.Registration,
			Error: "Pressure incident or exact recipient is no longer current; historical inbox entry retained"}
		_ = m.finishHostPressureDeliveryLocked(id, d, "skipped")
		return "", item, "", false
	}
	var msg Message
	if err := m.db.QueryRow(`SELECT data FROM messages WHERE id=?`, id).Scan(&raw); err != nil || json.Unmarshal(raw, &msg) != nil {
		return "", item, "", false
	}
	if _, err := m.db.Exec(`UPDATE host_pressure_outbox SET state='attempting' WHERE message=? AND state='queued'`, id); err != nil {
		return "", item, "", false
	}
	return id, item, msg.Body, true
}

func (m *Manager) finishHostPressureDelivery(id string, d agents.Delivery, state string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.finishHostPressureDeliveryLocked(id, d, state)
}

func (m *Manager) finishHostPressureDeliveryLocked(id string, d agents.Delivery, state string) error {
	var raw []byte
	if err := m.db.QueryRow(`SELECT data FROM host_pressure_outbox WHERE message=?`, id).Scan(&raw); err != nil {
		return err
	}
	var item hostPressureDelivery
	if err := json.Unmarshal(raw, &item); err != nil {
		return err
	}
	if err := m.db.QueryRow(`SELECT data FROM messages WHERE id=?`, id).Scan(&raw); err != nil {
		return err
	}
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}
	msg.Deliveries[item.Target.ID] = d
	encoded, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var previous string
	if err = tx.QueryRow(`SELECT state FROM host_pressure_outbox WHERE message=?`, id).Scan(&previous); err != nil {
		return err
	}
	if previous != "queued" && previous != "attempting" {
		return nil
	}
	if _, err = tx.Exec(`UPDATE messages SET data=? WHERE id=?`, string(encoded), id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE host_pressure_outbox SET state=? WHERE message=?`, state, id); err != nil {
		return err
	}
	if err = event(tx, "message.delivery", id, msg); err != nil {
		return err
	}
	if d.State == "responded" && d.Reply != "" {
		reply := Message{ID: local.ID(), Owner: m.owner, OwnerUsername: m.ownerUsername, Sender: item.Target.ID, ReplyTo: id,
			Audience: "operator", Body: d.Reply, Lease: msg.Lease, Created: time.Now().UTC(), Deliveries: map[string]agents.Delivery{}}
		if err = insertMessage(tx, reply, nil); err != nil {
			return err
		}
	}
	return tx.Commit()
}
