package database_test

import (
	"context"
	"fmt"
	"getarm.org/internal/agents"
	"getarm.org/internal/database"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/published"
	"getarm.org/internal/routing"
	"getarm.org/internal/workspace"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func publishedProofFixture(t *testing.T, retire bool) string {
	return publishedProofFixtureConcurrent(t, retire, false)
}
func publishedProofFixtureConcurrent(t *testing.T, retire, concurrent bool) string {
	t.Helper()
	ctx := t.Context()
	home, id := workspaceProofFixture(t, "active")
	d, e := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if d != nil {
			d.Close()
		}
	})
	v, e := d.WorkspaceRead(ctx)
	if e != nil {
		t.Fatal(e)
	}
	root := v.Instances[0]
	memberHome := proofFixture(t, false)
	m, e := manager.Open(memberHome, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if m != nil {
			m.Close()
		}
	})
	status, e := m.Status()
	if e != nil {
		t.Fatal(e)
	}
	identity, e := mesh.InitPrepared(memberHome, status.Host, "routing-proof-fixture", "127.0.0.1:23459", "127.0.0.1:23459")
	if e != nil {
		t.Fatal(e)
	}
	identity.Close()
	join, e := m.WorkspacePrepareJoin(ctx, workspace.JoinRequest{Key: "routing-join", Workspace: id, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: root.Self.Host, Reason: "explicit routing proof candidate"})
	if e != nil {
		t.Fatal(e)
	}
	join, e = m.WorkspaceDecide(ctx, id, join.Key, join.Revision, join.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	principal := workspace.Principal{Workspace: id, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: 1, Fingerprint: root.Self.Fingerprint}
	admit, e := d.WorkspacePrepareAdmission(ctx, principal, workspace.AdmissionRequest{Key: "routing-admit", Candidate: join.Review.Source, ConsentHash: join.ReviewHash, HostInbox: true, PolicyRevision: root.PolicyRevision, Reason: "explicit directory message recipient"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.WorkspaceActivate(ctx, principal, admit.Key, proofWorkspaceTransport(func(ctx context.Context, c workspace.Challenge) (workspace.Proof, error) {
		return m.WorkspaceProveJoin(ctx, root.Directory, c)
	})); e != nil {
		t.Fatal(e)
	}
	{
		ticket, e := m.WorkspaceBeginRefresh(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		snapshot, e := d.WorkspaceIssue(ctx, ticket)
		if e != nil {
			t.Fatal(e)
		}
		if e = m.WorkspaceAccept(ctx, root.Directory, snapshot); e != nil {
			t.Fatal(e)
		}
		current, _, e := m.WorkspaceLookup(ctx, id, join.Key)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = m.WorkspaceFinalize(ctx, id, join.Key, current.Revision); e != nil {
			t.Fatal(e)
		}
	}

	publish := func(node *manager.Manager, name string) (agents.Contact, published.Publication) {
		c, e := node.RegisterContact(agents.Contact{ID: name, Adapter: "generic", Delivery: "poll", Session: uuid.NewString(), CWD: t.TempDir()})
		if e != nil {
			t.Fatal(e)
		}
		q := published.Request{Workspace: id, ID: c.ID, Session: c.Session, Registration: c.Registration, Label: "Same public label", Delivery: "poll", Key: "publish", Reason: "actual private poll producer"}
		p, e := node.AgentPublicationPlan(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		v, e := node.AgentPublicationCreate(ctx, q, p.ReviewSHA)
		if e != nil {
			t.Fatal(e)
		}
		return c, v
	}
	dc, _ := publish(d, "source")
	mc, mp := publish(m, "target")
	out, created, e := d.WorkspaceAgentPrepare(ctx, routing.Intent{Workspace: id, Key: "poll-message", Target: status.Host, To: routing.Endpoint(mp.Endpoint), Body: "actual poll link proof", TTL: time.Hour}, dc.ID, dc.Session, dc.Registration)
	if e != nil || !created {
		t.Fatal(out, created, e)
	}
	out, e = d.WorkspaceRoutingBegin(ctx, out)
	if e != nil {
		t.Fatal(e)
	}
	in, e := m.WorkspaceRoutingAccept(ctx, root.Self, out.Envelope)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.WorkspaceRoutingComplete(ctx, out, in.Receipt); e != nil {
		t.Fatal(e)
	}
	page, e := m.AgentInbox(ctx, manager.InboxRequest{ID: mc.ID, Session: mc.Session, Registration: mc.Registration})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	if _, e = m.AgentReceipt(manager.ReceiptRequest{ID: mc.ID, Session: mc.Session, Registration: mc.Registration, Message: page.Items[0].Message.ID, Acknowledge: true}); e != nil {
		t.Fatal(e)
	}
	if concurrent {
		done := make(chan error, 2)
		go func() {
			for i := 0; i < 8; i++ {
				o, _, e := d.WorkspaceAgentPrepare(ctx, routing.Intent{Workspace: id, Key: fmt.Sprintf("concurrent-%d", i), Target: status.Host, To: routing.Endpoint(mp.Endpoint), Body: "serialized routed poll", TTL: time.Hour}, dc.ID, dc.Session, dc.Registration)
				if e != nil {
					done <- e
					return
				}
				o, e = d.WorkspaceRoutingBegin(ctx, o)
				if e != nil {
					done <- e
					return
				}
				in, e := m.WorkspaceRoutingAccept(ctx, root.Self, o.Envelope)
				if e != nil {
					done <- e
					return
				}
				if _, e = d.WorkspaceRoutingComplete(ctx, o, in.Receipt); e != nil {
					done <- e
					return
				}
			}
			done <- nil
		}()
		go func() {
			for i := 0; i < 8; i++ {
				next := mc
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
			case <-time.After(15 * time.Second):
				t.Fatal("RouteTransaction / SQLite / Manager.mu lock order deadlock")
			}
		}
	}
	if retire {
		for _, node := range []*manager.Manager{m, d} {
			v, e := node.WorkspaceRead(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = node.WorkspaceRetire(ctx, id, workspace.MutationRequest{Key: "retire-parent", Revision: v.Instances[0].Revision, Reason: "explicit local retirement"}); e != nil {
				t.Fatal(e)
			}
			r, e := node.WorkspaceRoutingRead(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = node.WorkspaceRoutingRetire(ctx, id, "retire-routing", r.Roots[0].Revision); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	if e = d.Close(); e != nil {
		t.Fatal(e)
	}
	d = nil
	m = nil
	return memberHome
}
func TestPublishedResetProofActualPollAndRetirement(t *testing.T) {
	for _, retire := range []bool{false, true} {
		t.Run(map[bool]string{true: "retired", false: "active"}[retire], func(t *testing.T) {
			home := publishedProofFixture(t, retire)
			v, e := prove(t, home)
			if e != nil {
				t.Fatal(e)
			}
			if !v.Complete || v.Settled != retire || v.Tables["routed_agent_links"] != 1 || v.Tables["published_agents"] != 1 {
				t.Fatal(v)
			}
		})
	}
}
func TestPublishedResetProofRefusesIndependentCorruptions(t *testing.T) {
	for _, damage := range []string{"missing-marker", "partial-group", "unknown-schema", "unknown-index", "missing-journal", "retarget-journal", "missing-link", "missing-inbox", "retarget-link", "native-promotion", "missing-local-message", "orphan-local-context", "changed-private-contact", "forged-ack", "local-ack-erased"} {
		t.Run(damage, func(t *testing.T) {
			home := publishedProofFixture(t, true)
			switch damage {
			case "missing-marker":
				edit(t, home, `DELETE FROM metadata WHERE key='routed_agents_schema'`)
			case "partial-group":
				edit(t, home, `DROP TABLE published_agent_changes`)
			case "unknown-schema":
				edit(t, home, `UPDATE metadata SET value='2' WHERE key='routed_agents_schema'`)
			case "unknown-index":
				edit(t, home, `CREATE INDEX published_extra ON published_agents(data)`)
			case "missing-journal":
				edit(t, home, `DELETE FROM published_agent_changes WHERE sequence=1`)
			case "retarget-journal":
				edit(t, home, `UPDATE published_agent_changes SET data=json_set(data,'$.after.contact.id','namesake') WHERE sequence=1`)
			case "missing-link":
				edit(t, home, `DELETE FROM routed_agent_links`)
			case "missing-inbox":
				edit(t, home, `DELETE FROM agent_inbox`)
			case "retarget-link":
				edit(t, home, `UPDATE routed_agent_links SET data=json_set(data,'$.registration','00000000000000000000000000000000')`)
			case "native-promotion":
				edit(t, home, `UPDATE messages SET data=json_set(data,'$.deliveries.target.mode','native')`)
			case "missing-local-message":
				edit(t, home, `DELETE FROM messages`)
			case "orphan-local-context":
				edit(t, home, `UPDATE messages SET data=json_set(data,'$.routed_origin.message_id','00000000000000000000000000000000')`)
			case "changed-private-contact":
				edit(t, home, `UPDATE published_agents SET data=json_set(data,'$.contact.idle',1)`)
			case "local-ack-erased":
				edit(t, home, `UPDATE agent_inbox SET receipt='{}'`)
			case "forged-ack":
				edit(t, home, `UPDATE routing_inbox SET data=json_set(data,'$.receipt.state','accepted')`)
			}
			v, e := prove(t, home)
			if e == nil && v.Complete && v.Settled {
				t.Fatal("unsafe publication reset accepted", damage)
			}
		})
	}
}

func TestPublishedRouteAndContactMutationSerializeWithoutNestedManagerLocks(t *testing.T) {
	home := publishedProofFixtureConcurrent(t, true, true)
	v, e := prove(t, home)
	if e != nil || !v.Complete || !v.Settled {
		t.Fatal(v, e)
	}
}

func TestPublishedResetUsesFreshAuthorityWithoutResurrectingEndpoints(t *testing.T) {
	home := publishedProofFixture(t, true)
	review, e := database.ReviewReset(t.Context(), home, "explicitly retire private published poll fixture", true)
	if e != nil {
		t.Fatal(e)
	}
	if !review.Plan.Proof.Settled {
		t.Fatal(review.Plan.Proof.Blockers)
	}
	old := review.Plan.OldHost
	result, e := review.Apply(t.Context())
	review.Close()
	if e != nil || !result.Completed || result.NewHost == old {
		t.Fatal(result, e)
	}
	lock, e := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = database.InstallResetSeed(t.Context(), home, lock); e != nil {
		t.Fatal(e)
	}
	m, e := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	v, e := m.AgentPublicationRead(t.Context())
	if e != nil || v.Initialized || len(v.Publications)+len(v.Links)+len(v.Changes) != 0 {
		t.Fatal("fresh authority resurrected old endpoint/inbox links", v, e)
	}
	r, e := m.WorkspaceRoutingRead(t.Context())
	if e != nil || r.Initialized || len(r.Outbox)+len(r.Inbox) != 0 {
		t.Fatal("fresh authority resurrected old routed identity", r, e)
	}
}
