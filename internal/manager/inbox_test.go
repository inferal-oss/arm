package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/model"
)

func pollContact(t *testing.T, m *Manager, id string) agents.Contact {
	t.Helper()
	c := agents.Contact{ID: id, Adapter: "generic", Session: uuid.NewString(), CWD: t.TempDir(), Delivery: "poll"}
	stored, err := m.RegisterContact(c)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}
func inboxRequest(c agents.Contact) InboxRequest {
	return InboxRequest{ID: c.ID, Session: c.Session, Registration: c.Registration}
}
func receiptRequest(c agents.Contact, msg string) ReceiptRequest {
	return ReceiptRequest{ID: c.ID, Session: c.Session, Registration: c.Registration, Message: msg}
}

func TestInboxFanoutReceiptsAndAcknowledgementsAreDistinct(t *testing.T) {
	m := newManager(t)
	a, b := pollContact(t, m, "a"), pollContact(t, m, "b")
	msg, err := m.Message(t.Context(), a.ID, "Reduce concurrency; $(do-not-execute)\nsecond line", "")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Deliveries[a.ID].State != "available" || msg.Deliveries[a.ID].Receipt == nil || msg.Deliveries[a.ID].Receipt.Received != nil {
		t.Fatalf("queued poll claimed receipt: %+v", msg)
	}
	page, err := m.AgentInbox(t.Context(), inboxRequest(a))
	if err != nil || len(page.Items) != 1 || page.Items[0].Message.Body != msg.Body {
		t.Fatalf("inbox data: %+v %v", page, err)
	}
	if page.Items[0].Message.Deliveries[a.ID].Receipt.Received != nil {
		t.Fatal("reading implied receipt")
	}
	pb, err := m.AgentInbox(t.Context(), inboxRequest(b))
	if err != nil || len(pb.Items) != 0 {
		t.Fatal("another recipient received private data", err)
	}
	if _, err = m.AgentReceipt(receiptRequest(b, msg.ID)); err == nil {
		t.Fatal("another recipient acknowledged private message")
	}
	received, err := m.AgentReceipt(receiptRequest(a, msg.ID))
	if err != nil || received.Received == nil || received.Acknowledged != nil {
		t.Fatalf("receipt: %+v %v", received, err)
	}
	ack := receiptRequest(a, msg.ID)
	ack.Acknowledge = true
	ack.Text = "I will reduce concurrency"
	first, err := m.AgentReceipt(ack)
	if err != nil || first.Acknowledged == nil || first.ReplyID == "" {
		t.Fatalf("ack: %+v %v", first, err)
	}
	second, err := m.AgentReceipt(ack)
	if err != nil || !second.Acknowledged.Equal(*first.Acknowledged) || first.ReplyID != second.ReplyID {
		t.Fatalf("ack retry duplicated response: %+v %v", second, err)
	}
	ack.Text = "a different response"
	if _, err = m.AgentReceipt(ack); err == nil {
		t.Fatal("overwrote a recorded acknowledgement")
	}
	msgs, err := m.Messages()
	if err != nil {
		t.Fatal(err)
	}
	replies := 0
	for _, got := range msgs {
		if got.ReplyTo == msg.ID {
			replies++
			if got.Sender != a.ID || got.Body != first.Text {
				t.Fatalf("operator reply identity: %+v", got)
			}
		}
		if got.ID == msg.ID && got.Deliveries[a.ID].Receipt.Acknowledged == nil {
			t.Fatal("operator history omitted acknowledgement")
		}
	}
	if replies != 1 {
		t.Fatalf("operator received %d duplicate replies", replies)
	}
	pending := inboxRequest(a)
	pending.Pending = true
	if p, err := m.AgentInbox(t.Context(), pending); err != nil || len(p.Items) != 0 {
		t.Fatal("acknowledged message remained pending", err)
	}
	broadcast, err := m.Message(t.Context(), "broadcast", "public maintenance warning", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []agents.Contact{a, b} {
		p, err := m.AgentInbox(t.Context(), inboxRequest(c))
		if err != nil {
			t.Fatal(err)
		}
		last := p.Items[len(p.Items)-1].Message
		if last.ID != broadcast.ID || len(last.Deliveries) != 1 || last.Deliveries[c.ID].Registration != c.Registration {
			t.Fatalf("broadcast exposed another registration: %+v", last)
		}
	}
	late := pollContact(t, m, "late")
	if p, err := m.AgentInbox(t.Context(), inboxRequest(late)); err != nil || len(p.Items) != 0 {
		t.Fatal("broadcast was replayed to a recipient not registered when sent", err)
	}
}

func TestInboxCursorPaginationPendingAndIdentityChanges(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "session-bound")
	var sent []Message
	for i := 0; i < 130; i++ {
		msg, err := m.Message(t.Context(), c.ID, fmt.Sprint("message ", i), "")
		if err != nil {
			t.Fatal(err)
		}
		sent = append(sent, msg)
	}
	p := inboxRequest(c)
	seen := map[string]bool{}
	for batches := 0; batches < 4; batches++ {
		page, err := m.AgentInbox(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.Message.ID] {
				t.Fatal("cursor duplicated message")
			}
			seen[item.Message.ID] = true
		}
		p.After = page.Cursor
		if !page.More {
			break
		}
	}
	if len(seen) != len(sent) {
		t.Fatalf("pagination lost messages: %d", len(seen))
	}
	for _, msg := range sent[:100] {
		r := receiptRequest(c, msg.ID)
		r.Acknowledge = true
		if _, err := m.AgentReceipt(r); err != nil {
			t.Fatal(err)
		}
	}
	pending := inboxRequest(c)
	pending.Pending = true
	page, err := m.AgentInbox(t.Context(), pending)
	if err != nil || len(page.Items) != 30 || page.More {
		t.Fatalf("pending page skipped unread messages: %d %v %v", len(page.Items), page.More, err)
	}
	if page.Items[0].Message.ID != sent[100].ID {
		t.Fatal("pending cursor started in acknowledged history")
	}
	invalid := p
	invalid.After = "not-base64-cursor"
	if _, err = m.AgentInbox(t.Context(), invalid); err == nil {
		t.Fatal("accepted corrupt cursor")
	}
	invalid.After = cursor(m.host+"other", c.Registration, 0)
	if _, err = m.AgentInbox(t.Context(), invalid); err == nil {
		t.Fatal("accepted another host's cursor")
	}
	invalid.After = cursor(m.host, c.Registration, 999999)
	if _, err = m.AgentInbox(t.Context(), invalid); err == nil {
		t.Fatal("accepted future cursor")
	}
	repeated, err := m.RegisterContact(c)
	if err != nil || repeated.Registration != c.Registration {
		t.Fatal("repeat registration changed identity", err)
	}
	r := request("attributed")
	r.Agent = c.ID
	l, err := m.Acquire(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := c
	changed.Session = uuid.NewString()
	changed.Registration = "caller-must-not-choose-this"
	if _, err = m.RegisterContact(changed); err == nil {
		t.Fatal("retargeted a contact with outstanding leases")
	}
	if _, err = m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	changed, err = m.RegisterContact(changed)
	if err != nil || changed.Registration == c.Registration || changed.Registration == "caller-must-not-choose-this" {
		t.Fatal("new identity reused registration", err)
	}
	if _, err = m.AgentInbox(t.Context(), p); err == nil {
		t.Fatal("old session followed reused contact name")
	}
	newRequest := inboxRequest(changed)
	newRequest.After = p.After
	if _, err = m.AgentInbox(t.Context(), newRequest); err == nil {
		t.Fatal("new session silently skipped history with old cursor")
	}
	newRequest.After = ""
	if page, err = m.AgentInbox(t.Context(), newRequest); err != nil || len(page.Items) != 0 {
		t.Fatal("new session read old private inbox", err)
	}
}

func TestInboxLongPollCancellationShutdownAndRequestLimit(t *testing.T) {
	m := newManager(t)
	c := pollContact(t, m, "watcher")
	p := inboxRequest(c)
	p.Wait = 20 * time.Second
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		page InboxPage
		err  error
	}
	done := make(chan result, 1)
	go func() { page, err := m.AgentInbox(ctx, p); done <- result{page, err} }()
	eventually(t, func() bool { return len(m.inboxSlots) == 1 })
	msg, err := m.Message(t.Context(), c.ID, "wake this exact watcher", "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil || len(r.page.Items) != 1 || r.page.Items[0].Message.ID != msg.ID {
			t.Fatalf("long poll lost wakeup: %+v", r)
		}
		p.After = r.page.Cursor
	case <-time.After(2 * time.Second):
		t.Fatal("message did not wake watcher")
	}
	go func() { page, err := m.AgentInbox(ctx, p); done <- result{page, err} }()
	eventually(t, func() bool { return len(m.inboxSlots) == 1 })
	cancel()
	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("cancelled watcher succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("watch cancellation did not release request")
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() { _, _ = m.AgentInbox(ctx, p) })
	}
	eventually(t, func() bool { return len(m.inboxSlots) == 64 })
	if _, err = m.AgentInbox(t.Context(), p); err == nil || !strings.Contains(err.Error(), "inbox_busy") {
		t.Fatal("inbox concurrency was unbounded", err)
	}
	cancel()
	wg.Wait()
}

func TestInboxRecoveryPreservesNativeIdentityAndReceipt(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	c := pollContact(t, m, "recovered")
	c.Adapter = "claude"
	c.Delivery = "native"
	c.Idle = true
	c, err = m.RegisterContact(c)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{ID: "interrupted-native", Sender: "operator", Audience: c.ID, Body: "recovered warning", Created: time.Now().UTC(), Deliveries: map[string]agents.Delivery{c.ID: {State: "pending", Mode: "native", Registration: c.Registration}}}
	if err = m.createMessage(msg, []agents.Contact{c}); err != nil {
		t.Fatal(err)
	}
	ack := receiptRequest(c, msg.ID)
	ack.Acknowledge = true
	ack.Text = "native operation is still in progress"
	if _, err = m.AgentReceipt(ack); err != nil {
		t.Fatal(err)
	}
	// A late native result must not overwrite a concurrent inbox receipt.
	if err = m.saveMessage(msg, "message.delivery"); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(home, model.Resources{CPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	page, err := m.AgentInbox(t.Context(), inboxRequest(c))
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("recovery inbox: %+v %v", page, err)
	}
	d := page.Items[0].Message.Deliveries[c.ID]
	if d.State != "unknown" || d.Registration != c.Registration || d.Receipt == nil || d.Receipt.Acknowledged == nil {
		t.Fatalf("recovery lost delivery identity/receipt: %+v", d)
	}
	if _, err = m.AgentReceipt(ack); err != nil {
		t.Fatal("ack retry after restart", err)
	}
}

func TestLegacyContactsGetStableRegistrationWithoutReplayingOldMessages(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	m, err := Open(home, model.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	c := agents.Contact{ID: "legacy", Adapter: "codex", Session: uuid.NewString(), CWD: t.TempDir()}
	raw, _ := json.Marshal(c)
	if _, err = m.db.Exec(`INSERT INTO agents VALUES(?,?)`, c.ID, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err = m.saveMessage(Message{ID: "unbound-old-message", Audience: c.ID, Body: "historical native delivery", Deliveries: map[string]agents.Delivery{c.ID: {State: "queued"}}}, "message.created"); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err = Open(home, model.Resources{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	c, err = m.Contact(c.ID)
	if err != nil || c.Registration == "" || c.Delivery != "native" {
		t.Fatal("legacy registration migration failed", err)
	}
	page, err := m.AgentInbox(t.Context(), inboxRequest(c))
	if err != nil || len(page.Items) != 0 {
		t.Fatal("unverifiable old recipients were guessed from a name", err)
	}
}

func TestInboxRejectsStaleDeliveryPolicyAndContactChangeDuringNativeDelivery(t *testing.T) {
	m := newManager(t)
	c := agents.Contact{ID: "policy-race", Adapter: "claude", Session: uuid.NewString(), CWD: t.TempDir(), Delivery: "native", Idle: true}
	c, err := m.RegisterContact(c)
	if err != nil {
		t.Fatal(err)
	}
	msg := Message{ID: "native-in-flight", Sender: "operator", Audience: c.ID, Body: "warning", Deliveries: map[string]agents.Delivery{c.ID: {State: "pending", Mode: "native", Registration: c.Registration}}}
	if err = m.createMessage(msg, []agents.Contact{c}); err != nil {
		t.Fatal(err)
	}
	changed := c
	changed.Delivery = "poll"
	changed.Idle = false
	if _, err = m.RegisterContact(changed); err == nil {
		t.Fatal("changed idle/delivery declaration while native delivery was still pending")
	}
	if again, err := m.RegisterContact(c); err != nil || again.Registration != c.Registration {
		t.Fatal("idempotent registration was blocked", err)
	}
	d := msg.Deliveries[c.ID]
	d.State = "responded"
	msg.Deliveries[c.ID] = d
	if err = m.saveMessage(msg, "message.delivery"); err != nil {
		t.Fatal(err)
	}
	stored, err := m.RegisterContact(changed)
	if err != nil || stored.Registration != c.Registration {
		t.Fatal("completed delivery prevented same-session policy change", err)
	}
	msg.ID = "stale-policy"
	if err = m.createMessage(msg, []agents.Contact{c}); err == nil {
		t.Fatal("stale native snapshot bypassed the current polling declaration")
	}
	page, err := m.AgentInbox(t.Context(), inboxRequest(stored))
	if err != nil || len(page.Items) != 1 {
		t.Fatal("failed creation left a partial inbox message", err)
	}
}
