package database_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
	"github.com/yrashk/arm/internal/workspacegraph"
)

// Uses the real native lifecycle and graph writers. Test corruption happens
// only AFTER a successful complete committed producer history was preserved.
func graphProofClose(t *testing.T, m *manager.Manager) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if e := m.Close(); e != nil {
				t.Error(e)
			}
		})
	}
}
func graphProofFixture(t *testing.T) (string, string) { return graphProofFixtureWithEarlier(t, false) }
func graphProofFixtureWithEarlier(t *testing.T, earlier bool) (directory, member string) {
	t.Helper()
	ctx := t.Context()
	directory, id := workspaceProofFixture(t, "active")
	d, e := manager.Open(directory, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	closeDirectory := graphProofClose(t, d)
	defer closeDirectory()
	closers := map[*manager.Manager]func(){d: closeDirectory}
	v, e := d.WorkspaceRead(ctx)
	if e != nil {
		t.Fatal(e)
	}
	root := v.Instances[0]
	var live []workspacegraph.Observation
	neighbors := []workspacegraph.Neighbor{}
	nodes := []*manager.Manager{}
	join := func(key string) (string, *manager.Manager) {
		home := proofFixture(t, false)
		m, e := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
		if e != nil {
			t.Fatal(e)
		}
		closeMember := graphProofClose(t, m)
		t.Cleanup(closeMember)
		closers[m] = closeMember
		status, e := m.Status()
		if e != nil {
			t.Fatal(e)
		}
		identity, e := mesh.InitPrepared(home, status.Host, "graph-proof-fixture", "127.0.0.1:23459", "127.0.0.1:23459")
		if e != nil {
			t.Fatal(e)
		}
		identity.Close()
		op, e := m.WorkspacePrepareJoin(ctx, workspace.JoinRequest{Key: key, Workspace: id, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: root.Self.Host, Reason: "actual graph/reset proof history"})
		if e != nil {
			t.Fatal(e)
		}
		op, e = m.WorkspaceDecide(ctx, id, op.Key, op.Revision, op.ReviewHash, true)
		if e != nil {
			t.Fatal(e)
		}
		q, e := m.WorkspaceBeginRefresh(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		pair := strings.Repeat("a", 64)
		report, e := m.WorkspaceGraphReport(ctx, q, []workspacegraph.Neighbor{{Host: root.Self.Host, Generation: 1, Pin: root.Self.Fingerprint, Pair: pair}})
		if e != nil {
			t.Fatal(e)
		}
		if _, _, err := d.WorkspaceIssueGraph(ctx, q, report, []workspacegraph.Neighbor{}, nil); err == nil {
			t.Fatal("unadmitted joining source issued a directory graph")
		}
		principal := workspace.Principal{Workspace: id, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: 1, Fingerprint: root.Self.Fingerprint}
		admit, e := d.WorkspacePrepareAdmission(ctx, principal, workspace.AdmissionRequest{Key: key + "-admit", Candidate: op.Review.Source, ConsentHash: op.ReviewHash, HostInbox: true, PolicyRevision: root.PolicyRevision, Reason: "reviewed graph candidate"})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = d.WorkspaceActivate(ctx, principal, admit.Key, proofWorkspaceTransport(func(ctx context.Context, c workspace.Challenge) (workspace.Proof, error) {
			return m.WorkspaceProveJoin(ctx, root.Directory, c)
		})); e != nil {
			t.Fatal(e)
		}
		neighbors = append(neighbors, workspacegraph.Neighbor{Host: op.Review.Source.Host, Generation: 1, Pin: op.Review.Source.Fingerprint, Pair: pair})
		bundle, next, e := d.WorkspaceIssueGraph(ctx, q, report, neighbors, live)
		if e != nil {
			t.Fatal(e)
		}
		live = next
		if e = m.WorkspaceAcceptGraph(ctx, root.Directory, bundle); e != nil {
			t.Fatal(e)
		}
		for _, blocked := range []struct{ label, from, to, class string }{
			{"message", op.Review.Source.Host, root.Self.Host, workspace.RouteMessage},
			{"lookup", op.Review.Source.Host, root.Self.Host, "message_lookup"},
			{"foreign origin", root.Self.Host, root.Self.Host, workspace.RouteDirectory},
			{"relay", root.Self.Host, strings.Repeat("f", 32), workspace.RouteDirectory},
			{"other target", op.Review.Source.Host, strings.Repeat("f", 32), workspace.RouteDirectory},
		} {
			if _, err := m.WorkspaceRouteAccess(ctx, id, blocked.from, blocked.to, blocked.class); err == nil {
				t.Fatal("joining member gained " + blocked.label + " authority")
			}
		}
		latest, _, e := m.WorkspaceLookup(ctx, id, op.Key)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = m.WorkspaceFinalize(ctx, id, op.Key, latest.Revision); e != nil {
			t.Fatal(e)
		}
		nodes = append(nodes, m)
		return home, m
	}
	if earlier {
		join("earlier-source")
	}
	member, _ = join("graph-join")
	nodes = append(nodes, d)
	for _, n := range nodes {
		records, e := n.WorkspaceRead(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = n.WorkspaceRetire(ctx, id, workspace.MutationRequest{Key: "graph-parent-retire", Revision: records.Instances[0].Revision, Reason: "explicit local retirement preserves remote membership"}); e != nil {
			t.Fatal(e)
		}
		routes, e := n.WorkspaceRoutingRead(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = n.WorkspaceRoutingRetire(ctx, id, "graph-routing-retire", routes.Roots[0].Revision); e != nil {
			t.Fatal(e)
		}
	}
	for _, n := range nodes {
		closers[n]()
	}
	return directory, member
}
func TestGraphResetProofActualWriterCompleteRetiredHistory(t *testing.T) {
	d, m := graphProofFixture(t)
	for _, home := range []string{d, m} {
		p, e := prove(t, home)
		if e != nil {
			t.Fatal(e)
		}
		if !p.Complete || !p.Settled || p.Tables["routing_graph_issuance"] != 1 {
			t.Fatal("graph retirement/proof incomplete", p.Blockers, p.Tables)
		}
	}
}
func TestGraphResetProofRejectsMissingFalseClosedAndHistoricalSubstitution(t *testing.T) {
	for _, damage := range []string{"marker1", "partial", "lost-reports", "lost-own-report", "lost-requester-report", "lost-older-report", "refs-missing", "refs-null", "refs-alias", "refs-duplicate", "refs-unsorted", "refs-unknown", "refs-sequence", "refs-empty", "issuer", "enable", "false-closed", "counter", "alias", "null", "missing", "source-nonce", "membership-pin", "report-expiry", "unrequested-child"} {
		t.Run(damage, func(t *testing.T) {
			d, m := graphProofFixtureWithEarlier(t, damage == "lost-older-report")
			home := m
			switch damage {
			case "marker1":
				edit(t, home, `UPDATE metadata SET value='1' WHERE key='routing_schema'`)
			case "partial":
				edit(t, home, `DROP TABLE routing_graph_reports`)
			case "lost-reports":
				home = d
				edit(t, home, `DELETE FROM routing_graph_reports`)
			case "lost-own-report":
				home = d
				edit(t, home, `DELETE FROM routing_graph_reports WHERE source=json_extract(data,'$.directory_authority.host')`)
			case "lost-requester-report":
				home = d
				edit(t, home, `DELETE FROM routing_graph_reports WHERE source<>json_extract(data,'$.directory_authority.host')`)
			case "lost-older-report":
				home = d
				edit(t, home, `DELETE FROM routing_graph_reports WHERE source<>json_extract(data,'$.directory_authority.host') AND source<>(SELECT json_extract(data,'$.last_issue.membership.requester_host') FROM routing_graph_issuance)`)
			case "refs-missing":
				home = d
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_remove(data,'$.report_refs')`)
			case "refs-null":
				home = d
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_set(data,'$.report_refs',NULL)`)
			case "refs-alias":
				home = d
				edit(t, home, `UPDATE routing_graph_issuance SET data=replace(data,'"report_refs":','"Report_Refs":')`)
			case "refs-duplicate":
				home = d
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_insert(data,'$.report_refs[#]',json_extract(data,'$.report_refs[0]'))`)
			case "refs-unsorted":
				home = d
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_set(data,'$.report_refs',json_array(json_extract(data,'$.report_refs[1]'),json_extract(data,'$.report_refs[0]')))`)
			case "refs-unknown":
				home = d
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_set(data,'$.report_refs[0].extra','unknown')`)
			case "refs-sequence":
				home = d
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_set(data,'$.report_refs[0].report_sequence',999)`)
			case "refs-empty":
				home = d
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_set(data,'$.report_refs',json('[]'))`)
			case "issuer":
				edit(t, home, `DELETE FROM routing_graph_issuance`)
			case "enable":
				edit(t, home, `DELETE FROM routing_events WHERE json_extract(data,'$.kind')='graph_enabled'`)
			case "false-closed":
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_set(data,'$.closed',json('false'))`)
			case "counter":
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_set(data,'$.report_sequence',123456)`)
			case "alias":
				edit(t, home, `UPDATE routing_graph_issuance SET data=replace(data,'"closed":','"Closed":')`)
			case "null":
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_set(data,'$.last_issue',NULL)`)
			case "missing":
				edit(t, home, `UPDATE routing_graph_issuance SET data=json_remove(data,'$.wall_floor')`)
			case "source-nonce":
				edit(t, home, `UPDATE routing_graph_observations SET data=json_set(data,'$.source_report.request_nonce','0000000000000000000000000000000000000000000000000000000000000000')`)
			case "membership-pin":
				edit(t, home, `UPDATE routing_graph_observations SET data=json_set(data,'$.bundle.membership.members[0].identity.certificate_sha256','0000000000000000000000000000000000000000000000000000000000000000')`)
			case "report-expiry":
				home = d
				edit(t, home, `UPDATE routing_graph_reports SET data=json_set(data,'$.original_valid_until','9999-01-01T00:00:00Z')`)
			case "unrequested-child":
				edit(t, home, `UPDATE routing_graph_observations SET data=json_set(data,'$.bundle.topology.extra','unknown')`)
			}
			p, e := prove(t, home)
			if e == nil && p.Complete && p.Settled {
				t.Fatal("unsafe graph evidence accepted", damage)
			}
		})
	}
}
