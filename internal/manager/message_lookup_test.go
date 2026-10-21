package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/model"
)

func TestMessageLookupDurableReceiptsAndMissing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	for _, key := range []string{"", strings.Repeat("x", 129)} {
		if _, err = m.LookupMessage(key); errorCode(err) != "invalid_request" {
			t.Fatal("invalid key", err)
		}
	}
	missing, err := m.LookupMessage("not-created")
	if err != nil || missing.Found || missing.Message != nil {
		t.Fatal(missing, err)
	}
	c := pollContact(t, m, "lookup-agent")
	msg, err := m.SendMessage(t.Context(), MessageRequest{Key: "durable-lookup", Audience: c.ID, Body: "Read-only lookup", Targets: []agents.Contact{c}})
	if err != nil {
		t.Fatal(err)
	}
	receipt := receiptRequest(c, msg.ID)
	receipt.Acknowledge = true
	receipt.Text = "Acknowledged"
	if _, err = m.AgentReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.LookupMessage("durable-lookup")
	if err != nil || !got.Found || got.Message.ID != msg.ID || got.Message.Deliveries[c.ID].Receipt.Acknowledged == nil {
		t.Fatalf("lookup lost durable receipt: %+v %v", got, err)
	}
}

func TestMessageLookupPendingNativeNeverDelivers(t *testing.T) {
	m := newManager(t)
	c, log, _ := fakeReviewedNative(t, m)
	r := MessageRequest{Key: "lookup-pending", Audience: c.ID, Body: "Persist only", Targets: []agents.Contact{c}}
	msg, created, err := m.createReviewedMessage(r)
	if err != nil || !created {
		t.Fatal(err)
	}
	for range 10 {
		got, err := m.LookupMessage(r.Key)
		if err != nil || !got.Found || got.Message.ID != msg.ID || got.Message.Deliveries[c.ID].State != "pending" {
			t.Fatal(got, err)
		}
	}
	if _, err = os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("lookup invoked native CLI", err)
	}
}

func TestMessageLookupDoesNotReportDatabaseFailureAsMissing(t *testing.T) {
	m := newManager(t)
	if _, err := m.db.Exec(`DROP TABLE message_operations`); err != nil {
		t.Fatal(err)
	}
	if result, err := m.LookupMessage("unavailable-journal"); err == nil || result.Found || result.Message != nil {
		t.Fatal("journal failure reported as missing", result, err)
	}
}
