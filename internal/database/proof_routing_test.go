package database_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/manager"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/routing"
	"github.com/yrashk/arm/internal/workspace"
)

func routedProofFixture(t *testing.T, phase string) string {
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
	t.Cleanup(func() { m.Close() })
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
	if phase == "guard-then-complete" {
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
	out, created, e := d.WorkspaceRoutingPrepare(ctx, routing.Intent{Workspace: id, Key: "message", Target: status.Host, Body: "private reset routed message", TTL: time.Minute})
	if e != nil || !created {
		t.Fatal(out, created, e)
	}
	if phase == "unknown" || phase == "guard-then-complete" {
		out, e = d.WorkspaceRoutingBegin(ctx, out)
		if e != nil {
			t.Fatal(e)
		}
		if out, e = d.WorkspaceRoutingUnknown(ctx, out); e != nil {
			t.Fatal(e)
		}
	} else {
		if _, e = d.WorkspaceRoutingCancelPrepared(ctx, id, out.Intent.Key, out.Revision); e != nil {
			t.Fatal(e)
		}
	}
	v, e = d.WorkspaceRead(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if phase == "guard-then-complete" {
		before, e := d.WorkspaceRoutingRead(ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = d.WorkspaceRetire(ctx, id, workspace.MutationRequest{Key: "guarded-retire", Revision: v.Instances[0].Revision, Reason: "must preserve live destination receipt lookup"})
		if e == nil {
			t.Fatal("native retirement stranded unknown source outcome")
		}
		after, e := d.WorkspaceRead(ctx)
		if e != nil || !reflect.DeepEqual(v, after) {
			t.Fatal("refused retirement changed parent/pins/operation/cache evidence", e)
		}
		routedAfter, e := d.WorkspaceRoutingRead(ctx)
		if e != nil || !reflect.DeepEqual(before, routedAfter) {
			t.Fatal("refused retirement changed routing evidence", e)
		}
		// The retained membership remains usable after the refused retirement.
		in, e := m.WorkspaceRoutingAccept(ctx, root.Self, out.Envelope)
		if e != nil {
			t.Fatal(e)
		}
		receipt, e := m.WorkspaceRoutingLookup(ctx, root.Self, out.Envelope)
		if e != nil || receipt == nil || *receipt != in.Receipt {
			t.Fatal("live exact lookup was lost", e)
		}
		if _, e = d.WorkspaceRoutingComplete(ctx, out, *receipt); e != nil {
			t.Fatal(e)
		}
	}
	_, e = d.WorkspaceRetire(ctx, id, workspace.MutationRequest{Key: "retire-parent", Revision: v.Instances[0].Revision, Reason: "explicit local retirement preserving remote resources"})
	if phase == "unknown" {
		if e == nil {
			t.Fatal("native retirement stranded unknown routing outcome")
		}
	} else if e != nil {
		t.Fatal(e)
	}
	if phase == "retired" || phase == "guard-then-complete" {
		r, e := d.WorkspaceRoutingRead(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = d.WorkspaceRoutingRetire(ctx, id, "retire-routing", r.Roots[0].Revision); e != nil {
			t.Fatal(e)
		}
	}
	if e = d.Close(); e != nil {
		t.Fatal(e)
	}
	d = nil
	return home
}
func TestRoutedResetProofActualWriterRetirementAndUnknown(t *testing.T) {
	for _, phase := range []string{"active", "retired", "unknown"} {
		t.Run(phase, func(t *testing.T) {
			home := routedProofFixture(t, phase)
			proof, e := prove(t, home)
			if e != nil {
				t.Fatal(e)
			}
			if !proof.Complete || proof.Settled != (phase == "retired") || proof.Tables["routing_outbox"] != 1 || proof.Tables["routing_events"] < 3 {
				t.Fatal(proof)
			}
			if strings.Contains(proof.SHA256, "private reset routed message") {
				t.Fatal("private body leaked into proof summary")
			}
		})
	}
}
func TestRoutedResetProofRejectsCorruptFalseSettlement(t *testing.T) {
	for _, damage := range []string{"marker", "partial", "alias", "null", "request", "history", "false-cancel", "parent-pin", "missing-index", "extra-index"} {
		t.Run(damage, func(t *testing.T) {
			home := routedProofFixture(t, "retired")
			switch damage {
			case "marker":
				edit(t, home, `DELETE FROM metadata WHERE key='routing_schema'`)
			case "partial":
				edit(t, home, `DROP TABLE routing_inbox`)
			case "alias":
				edit(t, home, `UPDATE routing_outbox SET data=replace(data,'"phase":','"Phase":')`)
			case "null":
				edit(t, home, `UPDATE routing_outbox SET data=json_set(data,'$.receipt',json('null'))`)
			case "request":
				edit(t, home, `UPDATE routing_outbox SET request='{}'`)
			case "history":
				edit(t, home, `DELETE FROM routing_events WHERE sequence=3`)
			case "false-cancel":
				edit(t, home, `UPDATE routing_events SET data=json_set(data,'$.phase','unknown','$.kind','outbound_unknown') WHERE sequence=3`)
			case "parent-pin":
				edit(t, home, `UPDATE routing_roots SET data=json_set(data,'$.local_pin',?)`, strings.Repeat("a", 64))
			case "missing-index":
				edit(t, home, `PRAGMA writable_schema=ON; DELETE FROM sqlite_schema WHERE name='sqlite_autoindex_routing_outbox_2'; PRAGMA writable_schema=OFF`)
			case "extra-index":
				edit(t, home, `CREATE INDEX routing_unreviewed ON routing_outbox(key)`)
			}
			p, e := prove(t, home)
			if e == nil && p.Complete {
				t.Fatal("damaged routed evidence accepted", damage, p)
			}
		})
	}
}

func TestRoutedNativeRetirementGuardPreservesLiveReceiptLookupThenSettles(t *testing.T) {
	home := routedProofFixture(t, "guard-then-complete")
	proof, e := prove(t, home)
	if e != nil || !proof.Complete || !proof.Settled {
		t.Fatal("settled actual manager producer history did not prove", proof, e)
	}
}
