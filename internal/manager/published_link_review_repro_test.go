package manager

import (
	"getarm.org/internal/local"
	"getarm.org/internal/published"
	"getarm.org/internal/routing"
	"getarm.org/internal/workspace"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reviewPollLinkFixture(t *testing.T) *Manager {
	m, op := workspaceManagerFixture(t, filepath.Join(t.TempDir(), "arm"))
	t.Cleanup(func() { m.Close() })
	_, p := publishFixture(t, m, op.Workspace, "recipient")
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
	return m
}
func TestReviewPublishedLinkedRawNULKeyMustRefuse(t *testing.T) {
	for _, q := range []struct{ table, col string }{{"published_agent_changes", "workspace"}, {"routed_agent_links", "workspace"}, {"routed_agent_links", "message"}, {"routed_agent_links", "local_message"}} {
		t.Run(q.table+"/"+q.col, func(t *testing.T) {
			m := reviewPollLinkFixture(t)
			if _, err := m.db.Exec("UPDATE " + q.table + " SET " + q.col + "=" + q.col + "||char(0)||'hidden-suffix'"); err != nil {
				t.Fatal(err)
			}
			v, err := m.AgentPublicationRead(t.Context())
			if err == nil {
				t.Fatalf("accepted malformed raw SQL %s.%s; links=%d", q.table, q.col, len(v.Links))
			}
		})
	}
}
