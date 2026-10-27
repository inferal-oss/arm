package manager

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/model"
)

func TestOperatorInboxAndInterruptedDeliveryRecovery(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, e := Open(home, model.Resources{CPU: 1})
	if e != nil {
		t.Fatal(e)
	}
	msg, e := m.Message(context.Background(), "operator", "Please inspect my build", "")
	if e != nil {
		t.Fatal(e)
	}
	pending := Message{ID: "interrupted", Audience: "fixture", Body: "warning", Created: time.Now().UTC(), Deliveries: map[string]agents.Delivery{"fixture": {State: "pending"}}}
	if e = m.saveMessage(pending, "message.created"); e != nil {
		t.Fatal(e)
	}
	m.Close()
	m, e = Open(home, model.Resources{CPU: 1})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	messages, e := m.Messages()
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, got := range messages {
		if got.ID == msg.ID {
			found = true
		}
		if got.ID == pending.ID && got.Deliveries["fixture"].State != "unknown" {
			t.Fatal("interrupted delivery was silently retried or left pending")
		}
	}
	if !found {
		t.Fatal("operator message was not durable")
	}
}
