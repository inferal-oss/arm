package manager

import (
	"context"
	"time"

	"getarm.org/internal/agents"
	"getarm.org/internal/guardian"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
)

// GuardianManaged lists live supervised process roots. The host guardian
// excludes them and their descendants: lease policy governs that work.
func (m *Manager) GuardianManaged() []model.ProcessIdentity {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []model.ProcessIdentity{}
	for _, j := range m.jobs {
		if j != nil && j.identity != nil {
			out = append(out, *j.identity)
		}
	}
	return out
}

// GuardianContacts lists registered local agent contacts.
func (m *Manager) GuardianContacts() []guardian.Contact {
	contacts, err := m.Contacts()
	if err != nil {
		return nil
	}
	out := make([]guardian.Contact, 0, len(contacts))
	for _, c := range contacts {
		if c.Remote == "" {
			out = append(out, guardian.Contact{ID: c.ID, Adapter: c.Adapter, CWD: c.CWD})
		}
	}
	return out
}

// GuardianNotify posts an operator inbox message from the host guardian.
func (m *Manager) GuardianNotify(body string) error {
	return m.createMessage(Message{ID: local.ID(), Owner: m.owner, OwnerUsername: m.ownerUsername, Sender: "arm-host-guardian", Audience: "operator", Created: time.Now().UTC(), Body: body, Deliveries: map[string]agents.Delivery{}}, nil)
}

// GuardianAsk sends a request to one registered agent through its normal
// delivery channel. The key binds one request per guardian action.
func (m *Manager) GuardianAsk(contactID, key, body string) error {
	c, err := m.Contact(contactID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = m.SendMessage(ctx, MessageRequest{Key: key, Audience: c.ID, Body: body, Targets: []agents.Contact{c}})
	return err
}
