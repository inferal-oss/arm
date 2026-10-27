package manager

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/agents"
	"getarm.org/internal/model"
	"github.com/google/uuid"
)

func TestReviewedMessageConcurrentRetriesAndReceipts(t *testing.T) {
	m := newManager(t)
	a, b := pollContact(t, m, "a"), pollContact(t, m, "b")
	r := MessageRequest{Key: "one-send", Audience: "broadcast", Body: "Reviewed fanout", Targets: []agents.Contact{b, a}}
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for range 20 {
		wg.Go(func() {
			msg, err := m.SendMessage(t.Context(), r)
			if err != nil {
				t.Error(err)
			}
			ids <- msg.ID
		})
	}
	wg.Wait()
	close(ids)
	var id string
	for got := range ids {
		if id == "" {
			id = got
		}
		if got != id {
			t.Fatal("retry created another message")
		}
	}
	for _, c := range []agents.Contact{a, b} {
		inbox, err := m.AgentInbox(t.Context(), inboxRequest(c))
		if err != nil || len(inbox.Items) != 1 || inbox.Items[0].Message.ID != id {
			t.Fatalf("fanout: %+v %v", inbox, err)
		}
	}
	ack := receiptRequest(a, id)
	ack.Acknowledge = true
	ack.Text = "Handled"
	if _, err := m.AgentReceipt(ack); err != nil {
		t.Fatal(err)
	}
	r.Targets = slices.Clone(r.Targets)
	slices.Reverse(r.Targets)
	retry, err := m.SendMessage(t.Context(), r)
	if err != nil || retry.ID != id || retry.Key != r.Key || retry.Deliveries[a.ID].Receipt.Acknowledged == nil {
		t.Fatalf("retry lost receipt: %+v %v", retry, err)
	}
	r.Body = "changed body"
	if _, err = m.SendMessage(t.Context(), r); errorCode(err) != "idempotency_conflict" {
		t.Fatal("changed body reused key", err)
	}
	msgs, _ := m.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected one send and one receipt reply, got %d", len(msgs))
	}
}

func TestReviewedMessageRejectsChangedRecipientsAndMembership(t *testing.T) {
	m := newManager(t)
	a := pollContact(t, m, "a")
	r := MessageRequest{Key: "changed-contact", Audience: a.ID, Body: "Exact session", Targets: []agents.Contact{a}}
	changed := a
	changed.Session = uuid.NewString()
	if _, err := m.RegisterContact(changed); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SendMessage(t.Context(), r); errorCode(err) != "contact_changed" {
		t.Fatal("retargeted changed session", err)
	}
	current, _ := m.Contacts()
	r = MessageRequest{Key: "changed-broadcast", Audience: "broadcast", Body: "Exact membership", Targets: current}
	pollContact(t, m, "b")
	if _, err := m.SendMessage(t.Context(), r); errorCode(err) != "contact_changed" {
		t.Fatal("broadcast silently expanded", err)
	}
	r = MessageRequest{Key: "policy-change", Audience: current[0].ID, Body: "Exact policy", Targets: current}
	policy := current[0]
	policy.Idle = !policy.Idle
	if _, err := m.RegisterContact(policy); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SendMessage(t.Context(), r); errorCode(err) != "contact_changed" {
		t.Fatal("changed policy accepted", err)
	}
	msgs, _ := m.Messages()
	if len(msgs) != 0 {
		t.Fatal("rejected sends left messages")
	}
	var n int
	if err := m.db.QueryRow(`SELECT count(*) FROM message_operations`).Scan(&n); err != nil || n != 0 {
		t.Fatal("rejected send consumed key", n, err)
	}
}

func TestReviewedMessageTransactionRollbackAndReplyAudience(t *testing.T) {
	m := newManager(t)
	a := pollContact(t, m, "a")
	b := pollContact(t, m, "b")
	r := MessageRequest{Key: "atomic-send", Audience: a.ID, Body: "Atomic", Targets: []agents.Contact{a}}
	if _, err := m.db.Exec(`CREATE TRIGGER fail_message BEFORE INSERT ON events WHEN NEW.type='message.created' BEGIN SELECT RAISE(ABORT,'fixture rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SendMessage(t.Context(), r); err == nil {
		t.Fatal("failure injection ignored")
	}
	for _, table := range []string{"messages", "message_operations", "agent_inbox"} {
		var n int
		if err := m.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial %s: %d %v", table, n, err)
		}
	}
	if _, err := m.db.Exec(`DROP TRIGGER fail_message`); err != nil {
		t.Fatal(err)
	}
	msg, err := m.SendMessage(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	ack := receiptRequest(a, msg.ID)
	ack.Acknowledge = true
	ack.Text = "Need guidance"
	receipt, err := m.AgentReceipt(ack)
	if err != nil {
		t.Fatal(err)
	}
	reply := MessageRequest{Key: "operator-reply", Audience: b.ID, Body: "Reply", ReplyTo: receipt.ReplyID, Targets: []agents.Contact{b}}
	if _, err = m.SendMessage(t.Context(), reply); errorCode(err) != "invalid_request" {
		t.Fatal("reply retargeted", err)
	}
	reply.Audience = a.ID
	reply.Targets = []agents.Contact{a}
	response, err := m.SendMessage(t.Context(), reply)
	if err != nil || response.ReplyTo != receipt.ReplyID {
		t.Fatalf("linked reply: %+v %v", response, err)
	}
}

func fakeReviewedNative(t *testing.T, m *Manager) (agents.Contact, string, string) {
	t.Helper()
	root := t.TempDir()
	log := filepath.Join(root, "calls")
	gate := filepath.Join(root, "gate")
	t.Setenv("ARM_SEND_LOG", log)
	t.Setenv("ARM_SEND_GATE", gate)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := "#!/bin/sh\nprintf 'invoked\\n' >> \"$ARM_SEND_LOG\"\nwhile [ ! -f \"$ARM_SEND_GATE\" ]; do sleep 0.02; done\n"
	if err := os.WriteFile(filepath.Join(root, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c, err := m.RegisterContact(agents.Contact{ID: "native-fixture", Adapter: "codex", Session: uuid.NewString(), CWD: root, Delivery: "native"})
	if err != nil {
		t.Fatal(err)
	}
	return c, log, gate
}

func TestReviewedMessageRetriesNeverRepeatNativeInvocation(t *testing.T) {
	m := newManager(t)
	c, log, gate := fakeReviewedNative(t, m)
	r := MessageRequest{Key: "native-once", Audience: c.ID, Body: "Exact native send", Targets: []agents.Contact{c}}
	type result struct {
		msg Message
		err error
	}
	done := make(chan result, 1)
	go func() { msg, err := m.SendMessage(t.Context(), r); done <- result{msg, err} }()
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0600) })
	eventually(t, func() bool { data, _ := os.ReadFile(log); return strings.Count(string(data), "invoked") == 1 })
	for range 10 {
		retry, err := m.SendMessage(t.Context(), r)
		if err != nil || retry.Deliveries[c.ID].State != "pending" {
			t.Fatalf("pending retry: %+v %v", retry, err)
		}
	}
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || result.msg.Deliveries[c.ID].State != "queued" {
			t.Fatalf("native delivery: %+v %v", result.msg, result.err)
		}
		retry, err := m.SendMessage(t.Context(), r)
		if err != nil || retry.ID != result.msg.ID || retry.Deliveries[c.ID].State != "queued" {
			t.Fatal("completed retry changed delivery", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native delivery stalled")
	}
	data, _ := os.ReadFile(log)
	if strings.Count(string(data), "invoked") != 1 {
		t.Fatal("native invocation repeated")
	}
}

func TestReviewedMessageInterruptedDeliveryRecoveryNeverResends(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	c, log, _ := fakeReviewedNative(t, m)
	r := MessageRequest{Key: "interrupted-send", Audience: c.ID, Body: "Persist before invoking", Targets: []agents.Contact{c}}
	msg, created, err := m.createReviewedMessage(r)
	if err != nil || !created {
		t.Fatal("durable create failed", err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	retry, err := m.SendMessage(t.Context(), r)
	if err != nil || retry.ID != msg.ID || retry.Deliveries[c.ID].State != "unknown" {
		t.Fatalf("recovery: %+v %v", retry, err)
	}
	if _, err = os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("recovery invoked recipient", err)
	}
}
