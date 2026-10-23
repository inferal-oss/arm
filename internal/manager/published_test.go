package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/published"
	"github.com/yrashk/arm/internal/routing"
	"github.com/yrashk/arm/internal/workspace"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicationContactPolicyChangeWithdrawsSameRegistration(t *testing.T) {
	m, op := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	defer m.Close()
	c := pollContact(t, m, "published")
	q := published.Request{Workspace: op.Workspace, ID: c.ID, Session: c.Session, Registration: c.Registration, Label: "Public label", Delivery: "poll", Key: "publish", Reason: "explicit scope"}
	plan, e := m.AgentPublicationPlan(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	p, e := m.AgentPublicationCreate(t.Context(), q, plan.ReviewSHA)
	if e != nil {
		t.Fatal(e)
	}
	c.Idle = true
	next, e := m.RegisterContact(c)
	if e != nil {
		t.Fatal(e)
	}
	if next.Registration != c.Registration {
		t.Fatal("policy-only registration changed")
	}
	v, e := m.AgentPublicationRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Publications) != 1 || v.Publications[0].State != "withdrawn" || v.Publications[0].Endpoint != p.Endpoint {
		t.Fatal("publication was retargeted", v)
	}
}

func publishFixture(t *testing.T, m *Manager, wid string, id string) (agents.Contact, published.Publication) {
	t.Helper()
	c := pollContact(t, m, id)
	q := published.Request{Workspace: wid, ID: c.ID, Session: c.Session, Registration: c.Registration, Label: "Public label", Delivery: "poll", Key: "publish-" + id, Reason: "explicit scope"}
	plan, e := m.AgentPublicationPlan(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	p, e := m.AgentPublicationCreate(t.Context(), q, plan.ReviewSHA)
	if e != nil {
		t.Fatal(e)
	}
	return c, p
}
func TestPublicationPreviewDoesNotInitializeAndNativeRejected(t *testing.T) {
	m, op := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	defer m.Close()
	c := pollContact(t, m, "preview")
	q := published.Request{Workspace: op.Workspace, ID: c.ID, Session: c.Session, Registration: c.Registration, Label: "Public", Delivery: "poll", Key: "preview", Reason: "explicit scope"}
	for i := 0; i < 2; i++ {
		if _, e := m.AgentPublicationPlan(t.Context(), q); e != nil {
			t.Fatal(e)
		}
	}
	var n int
	if e := m.db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name LIKE 'published_%' OR name LIKE 'routed_agent_%'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("preview initialized", n, e)
	}
	q.Delivery = "native"
	if _, e := m.AgentPublicationPlan(t.Context(), q); errorCode(e) != "unsupported_delivery" {
		t.Fatal("advertised unsupported native delivery", e)
	}
}
func TestPublicationWithdrawalRollsBackWithContactWrite(t *testing.T) {
	m, op := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	defer m.Close()
	c, p := publishFixture(t, m, op.Workspace, "rollback")
	if _, e := m.db.Exec(`CREATE TEMP TRIGGER refuse_agent BEFORE UPDATE ON agents BEGIN SELECT RAISE(ABORT,'fixture write refused'); END`); e != nil {
		t.Fatal(e)
	}
	c.Idle = true
	if _, e := m.RegisterContact(c); e == nil {
		t.Fatal("write should fail")
	}
	v, e := m.AgentPublicationRead(t.Context())
	if e != nil || len(v.Publications) != 1 || v.Publications[0].State != "active" || v.Publications[0].Endpoint != p.Endpoint || len(v.Changes) != 1 {
		t.Fatal("partial withdrawal committed", v, e)
	}
}
func TestPublicationConcurrentReviewAndContactPolicyChange(t *testing.T) {
	m, op := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	defer m.Close()
	c := pollContact(t, m, "concurrent")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 2)
	go func() {
		for i := 0; i < 16; i++ {
			q := published.Request{Workspace: op.Workspace, ID: c.ID, Session: c.Session, Registration: c.Registration, Label: "Concurrent", Delivery: "poll", Key: fmt.Sprintf("publish-%d", i), Reason: "serialized contact changes"}
			p, e := m.AgentPublicationPlan(ctx, q)
			if e != nil {
				done <- e
				return
			}
			_, e = m.AgentPublicationCreate(ctx, q, p.ReviewSHA)
			if e != nil && errorCode(e) != "publication_invalid" {
				done <- e
				return
			}
		}
		done <- nil
	}()
	go func() {
		for i := 0; i < 16; i++ {
			next := c
			next.Idle = i%2 == 0
			if _, e := m.RegisterContact(next); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 2; i++ {
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-ctx.Done():
			t.Fatal("SQLite/Manager.mu/Store lock order failed", ctx.Err())
		}
	}
	if _, e := m.AgentPublicationRead(ctx); e != nil {
		t.Fatal("active publication lost current full contact binding", e)
	}
}
func TestPublishedPollBridgeExactIdentityDedupAckAndNoNativePromotion(t *testing.T) {
	m, op := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	defer m.Close()
	c, p := publishFixture(t, m, op.Workspace, "recipient")
	records, e := m.WorkspaceRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	root := records.Instances[0]
	now := time.Now().UTC()
	remote := workspace.Identity{Host: local.ID(), Fingerprint: strings.Repeat("a", 64)}
	a := workspace.RouteAccess{At: now, Workspace: root.ID, Generation: root.Generation, Local: root.Self, Origin: remote, Destination: root.Self, OriginGeneration: 2, DestinationGeneration: 1, Expires: now.Add(30 * time.Second), Remaining: 30 * time.Second}
	from := routing.Endpoint{Kind: "agent", ID: local.ID(), Generation: local.ID()}
	envelope := routing.Envelope{Schema: 1, Workspace: root.ID, Generation: root.Generation, ID: local.ID(), Key: "incoming", Origin: remote.Host, Target: root.Self.Host, From: from, To: routing.Endpoint(p.Endpoint), OriginPin: remote.Fingerprint, TargetPin: root.Self.Fingerprint, OriginMembership: 2, TargetMembership: 1, Body: "Exact peer body", Created: now, Expires: now.Add(time.Hour)}
	for i := 0; i < 2; i++ {
		tx, e := m.db.BeginTx(t.Context(), nil)
		if e != nil {
			t.Fatal(e)
		}
		in, created, e := routing.AcceptWithDelivery(t.Context(), tx, a, envelope, func(in routing.Inbox, p published.Publication) error {
			return m.agentPollBridge(t.Context(), tx, in, p)
		})
		if e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
		if in.Receipt.State != "accepted" || created != (i == 0) {
			tx.Rollback()
			t.Fatal(in, created)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	inbox, e := m.AgentInbox(t.Context(), inboxRequest(c))
	if e != nil || len(inbox.Items) != 1 {
		t.Fatal(inbox, e)
	}
	msg := inbox.Items[0].Message
	if msg.RoutedOrigin == nil || msg.RoutedOrigin.Origin != remote.Host || msg.RoutedOrigin.From != published.Address(from) || msg.SenderContact != nil || msg.Deliveries[c.ID].Mode != "poll" {
		t.Fatal("original sender or delivery changed", msg)
	}
	q := receiptRequest(c, msg.ID)
	q.Acknowledge = true
	q.Text = "cannot become a native reply"
	if _, e := m.AgentReceipt(q); errorCode(e) != "explicit_routed_reply_required" {
		t.Fatal(e)
	}
	q.Text = ""
	if _, e := m.AgentReceipt(q); e != nil {
		t.Fatal(e)
	}
	v, e := m.WorkspaceRoutingRead(t.Context())
	if e != nil || len(v.Inbox) != 1 || v.Inbox[0].Receipt.State != "acknowledged" {
		t.Fatal(v, e)
	}
	intent, e := m.WorkspaceAgentReplyIntent(t.Context(), root.ID, msg.ID, c.ID, c.Session, c.Registration, "reply", "Exact reply", time.Hour)
	if e != nil || intent.Target != remote.Host || intent.From != envelope.To || intent.To != from || intent.ReplyTo == nil || *intent.ReplyTo != envelope.Reference() {
		t.Fatal(intent, e)
	}
	c.Idle = true
	if _, e := m.RegisterContact(c); e != nil {
		t.Fatal(e)
	}
	if _, e := m.WorkspaceAgentReplyIntent(t.Context(), root.ID, msg.ID, c.ID, c.Session, c.Registration, "reply2", "No namesake", time.Hour); e == nil {
		t.Fatal("withdrawn recipient silently rebound reply")
	}
}

func TestPublicationCapacityRetainsEscapedFinalizationAndExactRetry(t *testing.T) {
	m, op := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	defer m.Close()
	pubs := []published.Publication{}
	for i := 0; i < published.MaxActive; i++ {
		c := pollContact(t, m, fmt.Sprintf("capacity-%d", i))
		q := published.Request{Workspace: op.Workspace, ID: c.ID, Session: c.Session, Registration: c.Registration, Label: strings.Repeat("\"", 128), Delivery: "poll", Key: fmt.Sprintf("capacity-%d", i), Reason: strings.Repeat("\"", 512)}
		plan, e := m.AgentPublicationPlan(t.Context(), q)
		if e != nil {
			t.Fatal(e)
		}
		p, e := m.AgentPublicationCreate(t.Context(), q, plan.ReviewSHA)
		if e != nil {
			t.Fatal(i, e)
		}
		pubs = append(pubs, p)
	}
	c := pollContact(t, m, "overflow")
	q := published.Request{Workspace: op.Workspace, ID: c.ID, Session: c.Session, Registration: c.Registration, Label: "Overflow", Delivery: "poll", Key: "overflow", Reason: "no finalization reserve bypass"}
	plan, e := m.AgentPublicationPlan(t.Context(), q)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.AgentPublicationCreate(t.Context(), q, plan.ReviewSHA); e == nil {
		t.Fatal("active capacity overcommitted")
	}
	for _, p := range pubs {
		w := published.Withdrawal{Key: strings.Repeat("\"", 128), Reason: strings.Repeat("\"", 512), Revision: 1}
		if _, e = m.AgentPublicationWithdraw(t.Context(), p.Scope.Workspace, p.Endpoint, w); e != nil {
			t.Fatal("escaped-byte finalization promise failed", e)
		}
	}
	v, e := m.AgentPublicationRead(t.Context())
	if e != nil || len(v.Publications) != 32 || len(v.Changes) != 64 {
		t.Fatal(v, e)
	}
	p := pubs[0]
	before, _ := json.Marshal(v)
	old, e := m.AgentPublicationCreate(t.Context(), p.Request, p.ReviewSHA)
	if e != nil || old.State != "withdrawn" || old.Endpoint != p.Endpoint {
		t.Fatal(old, e)
	}
	v, e = m.AgentPublicationRead(t.Context())
	after, _ := json.Marshal(v)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("known-key lookup grew evidence", e)
	}
}
