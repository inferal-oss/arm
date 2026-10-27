package manager

import (
	"encoding/json"
	"sync"
	"testing"

	"getarm.org/internal/agents"
	"github.com/google/uuid"
)

func reviewedAgentRequest(source, target agents.Contact, key string) AgentMessageRequest {
	return AgentMessageRequest{ID: source.ID, Session: source.Session, Registration: source.Registration, Key: key, Audience: target.ID, Targets: []agents.Contact{target}, Body: "independent review message"}
}

func TestAgentReviewOperatorReplyPinsOriginalSource(t *testing.T) {
	m := newManager(t)
	a := pollContact(t, m, "review-a")
	request := reviewedAgentRequest(a, a, "operator-thread")
	request.Audience = "operator"
	request.Targets = []agents.Contact{}
	msg, err := m.AgentMessage(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := m.SendMessage(t.Context(), MessageRequest{Key: "review-operator-reply", Audience: a.ID, Targets: []agents.Contact{a}, ReplyTo: msg.ID, Body: "operator guidance"})
	if err != nil || reply.ReplyTo != msg.ID || reply.Sender != "operator" {
		t.Fatalf("registered agent's operator thread cannot receive operator reply: %+v %v", reply, err)
	}
	changed := a
	changed.Session = uuid.NewString()
	changed, err = m.RegisterContact(changed)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.SendMessage(t.Context(), MessageRequest{Key: "review-operator-stale-reply", Audience: changed.ID, Targets: []agents.Contact{changed}, ReplyTo: msg.ID, Body: "must not silently replace old source"})
	if err == nil {
		t.Fatal("operator reply silently retargeted replacement source")
	}
}
func TestAgentReviewAckReplyAtomicAndNoNativeLoop(t *testing.T) {
	m := newManager(t)
	a, b := pollContact(t, m, "review-a"), pollContact(t, m, "review-b")
	msg, err := m.AgentMessage(t.Context(), reviewedAgentRequest(a, b, "atomic-ack"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.db.Exec(`CREATE TRIGGER review_fail_reply BEFORE INSERT ON events WHEN NEW.type='message.created' BEGIN SELECT RAISE(ABORT,'review atomic reply failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	ack := receiptRequest(b, msg.ID)
	ack.Acknowledge = true
	ack.Text = "response for original sender"
	if _, err = m.AgentReceipt(ack); err == nil {
		t.Fatal("ignored transactional reply failure")
	}
	snapshot, _ := m.message(msg.ID)
	if snapshot.Deliveries[b.ID].Receipt.Acknowledged != nil {
		t.Fatal("ack committed without reply")
	}
	page, _ := m.AgentInbox(t.Context(), inboxRequest(a))
	if len(page.Items) != 0 {
		t.Fatal("partial sender inbox reply escaped rollback")
	}
	m.db.Exec(`DROP TRIGGER review_fail_reply`)
	receipt, err := m.AgentReceipt(ack)
	if err != nil {
		t.Fatal(err)
	}
	page, err = m.AgentInbox(t.Context(), inboxRequest(a))
	if err != nil || len(page.Items) != 1 || page.Items[0].Message.ID != receipt.ReplyID || page.Items[0].Message.Deliveries[a.ID].Mode != "poll" || page.Items[0].Message.SenderContact == nil || page.Items[0].Message.SenderContact.ID != b.ID {
		t.Fatalf("reply %+v %v", page, err)
	}
	again, err := m.AgentReceipt(ack)
	if err != nil || again.ReplyID != receipt.ReplyID {
		t.Fatalf("retry duplicated reply %+v %v", again, err)
	}
}
func TestAgentReviewReplacementAndRecipientProof(t *testing.T) {
	m := newManager(t)
	a, b, c := pollContact(t, m, "review-a"), pollContact(t, m, "review-b"), pollContact(t, m, "review-c")
	msg, err := m.AgentMessage(t.Context(), reviewedAgentRequest(a, b, "proof"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AgentMessage(t.Context(), AgentMessageRequest{ID: c.ID, Session: c.Session, Registration: c.Registration, Key: "forged-reply", ReplyTo: msg.ID, Body: "I read a global message ID"})
	if errorCode(err) != "forbidden" {
		t.Fatal("message ID alone allowed recipient impersonation", err)
	}
	replacement := a
	replacement.Session = uuid.NewString()
	replacement, err = m.RegisterContact(replacement)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AgentMessage(t.Context(), AgentMessageRequest{ID: b.ID, Session: b.Session, Registration: b.Registration, Key: "never-retarget", ReplyTo: msg.ID, Body: "explicit old-source reply"})
	if errorCode(err) != "contact_changed" {
		t.Fatal("replaced source was targeted", err)
	}
	ack := receiptRequest(b, msg.ID)
	ack.Acknowledge = true
	ack.Text = "preserved old-source reply"
	receipt, err := m.AgentReceipt(ack)
	if err != nil || receipt.ReplyState != "superseded" {
		t.Fatalf("%+v %v", receipt, err)
	}
	page, err := m.AgentInbox(t.Context(), inboxRequest(replacement))
	if err != nil || len(page.Items) != 0 {
		t.Fatal("replacement received old sender's reply", err)
	}
}
func TestAgentReviewConcurrentKeyedSendAndUnexportedSource(t *testing.T) {
	m := newManager(t)
	a, b := pollContact(t, m, "review-a"), pollContact(t, m, "review-b")
	request := reviewedAgentRequest(a, b, "race-dedup")
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for range 12 {
		wg.Go(func() {
			msg, err := m.AgentMessage(t.Context(), request)
			if err != nil {
				t.Error(err)
			}
			ids <- msg.ID
		})
	}
	wg.Wait()
	close(ids)
	id := ""
	for got := range ids {
		if id == "" {
			id = got
		}
		if got != id {
			t.Fatal("concurrent sender key created duplicate")
		}
	}
	// JSON inputs to the existing operator path cannot set unexported sender provenance.
	raw, _ := json.Marshal(map[string]any{"idempotency_key": "review-spoof", "audience": b.ID, "body": "operator message", "targets": []agents.Contact{b}, "source": a, "sender_contact": a, "agent_operation_key": "forged"})
	var operator MessageRequest
	if err := json.Unmarshal(raw, &operator); err != nil {
		t.Fatal(err)
	}
	msg, err := m.SendMessage(t.Context(), operator)
	if err != nil || msg.SenderContact != nil || msg.AgentKey != "" || msg.Sender != "operator" {
		t.Fatalf("JSON source escaped authority %+v %v", msg, err)
	}
}

func TestAgentReviewNativeReplyTransactionAndPollOnlyRouting(t *testing.T) {
	m := newManager(t)
	a := pollContact(t, m, "review-a")
	b, err := m.RegisterContact(agents.Contact{ID: "review-native", Adapter: "claude", Delivery: "native", Idle: true, Session: uuid.NewString(), CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	// Stop at the durable intent boundary; the adapter result is supplied only to
	// exercise SQL settlement, not to claim a real native CLI acceptance run.
	original, created, err := m.createReviewedMessage(MessageRequest{Key: "review-native-intent", Audience: b.ID, Targets: []agents.Contact{b}, Body: "native transaction boundary", source: &a, agentKey: "native-intent"})
	if err != nil || !created {
		t.Fatalf("%+v %v", original, err)
	}
	original.Deliveries[b.ID] = agents.Delivery{State: "responded", Reply: "native returned text", Mode: b.Delivery, Registration: b.Registration}
	_, err = m.db.Exec(`CREATE TRIGGER review_fail_native_reply BEFORE INSERT ON events WHEN NEW.type='message.created' BEGIN SELECT RAISE(ABORT,'review failed native settlement'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.saveAgentNativeReply(&original, b); err == nil {
		t.Fatal("native response ignored reply journal failure")
	}
	persisted, _ := m.message(original.ID)
	if persisted.Deliveries[b.ID].State != "pending" || len(persisted.NativeReplies) != 0 {
		t.Fatal("native response committed without sender inbox")
	}
	page, _ := m.AgentInbox(t.Context(), inboxRequest(a))
	if len(page.Items) != 0 {
		t.Fatal("partial native reply escaped rollback")
	}
	m.db.Exec(`DROP TRIGGER review_fail_native_reply`)
	if err = m.saveAgentNativeReply(&original, b); err != nil {
		t.Fatal(err)
	}
	persisted, _ = m.message(original.ID)
	page, _ = m.AgentInbox(t.Context(), inboxRequest(a))
	if persisted.Deliveries[b.ID].State != "responded" || len(page.Items) != 1 || persisted.NativeReplies[b.ID] != page.Items[0].Message.ID || page.Items[0].Message.Deliveries[a.ID].Mode != "poll" || page.Items[0].Message.Body != "native returned text" {
		t.Fatalf("native settlement %+v inbox %+v", persisted, page)
	}
}
