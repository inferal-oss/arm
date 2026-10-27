package database_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/database"
	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
)

func workspaceProofFixture(t *testing.T, state string) (string, string) {
	t.Helper()
	home := proofFixture(t, false)
	m, e := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	status, e := m.Status()
	if e != nil {
		t.Fatal(e)
	}
	identity, e := mesh.Init(home, status.Host, "workspace-proof-fixture", "127.0.0.1:23457", "127.0.0.1:23457")
	if e != nil {
		t.Fatal(e)
	}
	identity.Close()
	op, e := m.WorkspacePrepareCreate(context.Background(), workspace.CreateRequest{Key: "proof-workspace", Name: "archival fixture", Policy: workspace.DefaultPolicy(), Reason: "local workspace reset proof"})
	if e != nil {
		t.Fatal(e)
	}
	switch state {
	case "declined":
		_, e = m.WorkspaceDecide(context.Background(), op.Workspace, op.Key, op.Revision, op.ReviewHash, false)
	case "active", "retired":
		_, e = m.WorkspaceDecide(context.Background(), op.Workspace, op.Key, op.Revision, op.ReviewHash, true)
		if e == nil && state == "retired" {
			v, read := m.WorkspaceRead(context.Background())
			if read != nil {
				t.Fatal(read)
			}
			_, e = m.WorkspaceRetire(context.Background(), op.Workspace, workspace.MutationRequest{Key: "proof-retire", Revision: v.Instances[0].Revision, Reason: "explicit local retirement; no remote cleanup claim"})
		}
	}
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	return home, op.Workspace
}
func TestWorkspaceResetProofActualRetirementAndDeclineWriters(t *testing.T) {
	for _, state := range []string{"prepared", "active", "retired", "declined"} {
		t.Run(state, func(t *testing.T) {
			home, _ := workspaceProofFixture(t, state)
			proof, e := prove(t, home)
			if e != nil {
				t.Fatal(e)
			}
			settled := state == "retired" || state == "declined"
			if !proof.Complete || proof.Settled != settled {
				t.Fatal("wrong explicit workspace settlement", proof.Blockers)
			}
			if proof.Tables["workspace_instances"] != 1 || proof.Tables["workspace_operations"] < 1 {
				t.Fatal("workspace evidence omitted from complete hash", proof.Tables)
			}
		})
	}
}
func TestWorkspaceResetProofRefusesCorruptUnknownAndFalseRetirement(t *testing.T) {
	for _, damage := range []string{"marker", "partial", "unknown-version", "extra-index", "missing-autoindex", "root-null", "root-alias", "root-missing", "lost-clock", "lost-operation", "lost-journal", "null-optional", "forged-retired", "member-active", "trust-active", "identity-change", "request-column", "null-nested-request"} {
		t.Run(damage, func(t *testing.T) {
			state := "retired"
			if damage == "forged-retired" {
				state = "active"
			}
			home, id := workspaceProofFixture(t, state)
			switch damage {
			case "marker":
				edit(t, home, `DELETE FROM metadata WHERE key='workspace_schema'`)
			case "partial":
				edit(t, home, `DROP TABLE workspace_clocks`)
			case "unknown-version":
				edit(t, home, `UPDATE metadata SET value='999' WHERE key='workspace_schema'`)
			case "extra-index":
				edit(t, home, `CREATE INDEX workspace_extra ON workspace_instances(data)`)
			case "missing-autoindex":
				edit(t, home, `PRAGMA writable_schema=ON;DELETE FROM sqlite_schema WHERE name='sqlite_autoindex_workspace_instances_1';PRAGMA writable_schema=OFF`)
			case "root-null":
				edit(t, home, `UPDATE workspace_instances SET data=json_set(data,'$.policy',NULL)`)
			case "root-alias":
				edit(t, home, `UPDATE workspace_instances SET data=json_set(data,'$.Policy',json_extract(data,'$.policy'))`)
			case "root-missing":
				edit(t, home, `DELETE FROM workspace_instances`)
			case "lost-clock":
				edit(t, home, `DELETE FROM workspace_clocks`)
			case "lost-operation":
				edit(t, home, `DELETE FROM workspace_operations WHERE key='proof-retire'`)
			case "lost-journal":
				edit(t, home, `DELETE FROM workspace_changes WHERE json_extract(data,'$.kind')='retire'`)
			case "null-optional":
				edit(t, home, `UPDATE workspace_operations SET data=json_set(data,'$.member',NULL) WHERE key='proof-retire'`)
			case "forged-retired":
				edit(t, home, `UPDATE workspace_instances SET data=json_set(data,'$.state','retired')`)
			case "member-active":
				edit(t, home, `UPDATE workspace_directory_members SET data=json_set(data,'$.state','active')`)
			case "trust-active":
				edit(t, home, `UPDATE workspace_trusted_members SET data=json_set(data,'$.state','active')`)
			case "identity-change":
				edit(t, home, `UPDATE workspace_trusted_members SET data=json_set(data,'$.identity.display_name','changed')`)
			case "request-column":
				edit(t, home, `UPDATE workspace_operations SET request='{}' WHERE key='proof-retire'`)
			case "null-nested-request":
				db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
				if e != nil {
					t.Fatal(e)
				}
				var raw string
				if e = db.QueryRow(`SELECT data FROM workspace_operations WHERE key='proof-retire'`).Scan(&raw); e != nil {
					t.Fatal(e)
				}
				var op map[string]any
				if e = json.Unmarshal([]byte(raw), &op); e != nil {
					t.Fatal(e)
				}
				op["request"].(map[string]any)["reason"] = nil
				newraw, _ := json.Marshal(op)
				req, _ := json.Marshal(op["request"])
				_, e = db.Exec(`UPDATE workspace_operations SET data=?,request=? WHERE key='proof-retire'`, string(newraw), string(req))
				db.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			proof, e := prove(t, home)
			if e == nil && proof.Complete && proof.Settled {
				t.Fatal("unsafe workspace archival accepted", id, damage)
			}
		})
	}
}
func TestWorkspaceRetiredResetArchivesEvidenceAndFencesFreshAuthority(t *testing.T) {
	home, id := workspaceProofFixture(t, "retired")
	review, e := database.ReviewReset(context.Background(), home, "archive explicitly retired local workspace", true)
	if e != nil {
		t.Fatal(e)
	}
	if !review.Plan.Proof.Settled {
		t.Fatal("retired fixture not settled", review.Plan.Proof.Blockers)
	}
	old := review.Plan.OldHost
	result, e := review.Apply(context.Background())
	review.Close()
	if e != nil {
		t.Fatal(e)
	}
	if result.NewHost == old {
		t.Fatal("reset inherited old workspace authority")
	}
	// The archive is retained; starting the replacement manager cannot load any
	// old workspace/root/cache or recreate its former identity.
	lock, e := os.OpenFile(filepath.Join(home, "daemon.lock"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = database.InstallResetSeed(context.Background(), home, lock); e != nil {
		t.Fatal(e)
	}
	m, e := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	v, e := m.WorkspaceRead(context.Background())
	if e != nil || v.Initialized || len(v.Instances) != 0 {
		t.Fatal("fresh authority inherited old membership", id, v, e)
	}
	if _, e = os.Stat(filepath.Join(home, "mesh", "node-key.pem")); e == nil {
		t.Fatal("fresh workspace implicitly inherited old node key")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "proof_nonce") {
		t.Fatal("reset result exposed workspace private proof")
	}
}

type proofWorkspaceTransport func(context.Context, workspace.Challenge) (workspace.Proof, error)

func (f proofWorkspaceTransport) Prove(ctx context.Context, c workspace.Challenge) (workspace.Proof, error) {
	return f(ctx, c)
}
func TestWorkspaceResetProofActualPendingUnknownAndJoinedRetirement(t *testing.T) {
	for _, phase := range []string{"pending", "unknown", "joined"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			directoryHome, id := workspaceProofFixture(t, "active")
			d, e := manager.Open(directoryHome, model.Resources{CPU: 2, Disk: 1 << 20})
			if e != nil {
				t.Fatal(e)
			}
			records, e := d.WorkspaceRead(ctx)
			if e != nil {
				t.Fatal(e)
			}
			root := records.Instances[0]
			memberHome := proofFixture(t, false)
			m, e := manager.Open(memberHome, model.Resources{CPU: 2, Disk: 1 << 20})
			if e != nil {
				t.Fatal(e)
			}
			status, e := m.Status()
			if e != nil {
				t.Fatal(e)
			}
			identity, e := mesh.Init(memberHome, status.Host, "member-proof-fixture", "127.0.0.1:23459", "127.0.0.1:23459")
			if e != nil {
				t.Fatal(e)
			}
			identity.Close()
			join, e := m.WorkspacePrepareJoin(ctx, workspace.JoinRequest{Key: "join-proof", Workspace: id, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: root.Self.Host, Reason: "explicit independent candidate consent"})
			if e != nil {
				t.Fatal(e)
			}
			join, e = m.WorkspaceDecide(ctx, id, join.Key, join.Revision, join.ReviewHash, true)
			if e != nil {
				t.Fatal(e)
			}
			p := workspace.Principal{Workspace: id, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: 1, Fingerprint: root.Self.Fingerprint}
			admit, e := d.WorkspacePrepareAdmission(ctx, p, workspace.AdmissionRequest{Key: "admission-proof", Candidate: join.Review.Source, ConsentHash: join.ReviewHash, HostInbox: true, PolicyRevision: root.PolicyRevision, Reason: "approved introduction proof"})
			if e != nil {
				t.Fatal(e)
			}
			if phase == "unknown" {
				_, e = d.WorkspaceActivate(ctx, p, admit.Key, proofWorkspaceTransport(func(context.Context, workspace.Challenge) (workspace.Proof, error) {
					return workspace.Proof{}, fmt.Errorf("injected unavailable candidate")
				}))
				if e == nil {
					t.Fatal("unknown proof fixture succeeded")
				}
			}
			if phase == "joined" {
				if _, e = d.WorkspaceActivate(ctx, p, admit.Key, proofWorkspaceTransport(func(ctx context.Context, c workspace.Challenge) (workspace.Proof, error) {
					return m.WorkspaceProveJoin(ctx, root.Directory, c)
				})); e != nil {
					t.Fatal(e)
				}
				q, e := m.WorkspaceBeginRefresh(ctx, id)
				if e != nil {
					t.Fatal(e)
				}
				snapshot, e := d.WorkspaceIssue(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				if e = m.WorkspaceAccept(ctx, root.Directory, snapshot); e != nil {
					t.Fatal(e)
				}
				join, _, e = m.WorkspaceLookup(ctx, id, join.Key)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = m.WorkspaceFinalize(ctx, id, join.Key, join.Revision); e != nil {
					t.Fatal(e)
				}
				if _, e = m.WorkspaceCheck(ctx, id, root.Directory.Host); e != nil {
					t.Fatal(e)
				}
			}
			// Snapshot the stopped production ledgers, including candidate-unknown and
			// approved-unfinalized local joins. Expiry cannot make these settled.
			if e = d.Close(); e != nil {
				t.Fatal(e)
			}
			if e = m.Close(); e != nil {
				t.Fatal(e)
			}
			for _, home := range []string{directoryHome, memberHome} {
				proof, e := prove(t, home)
				if e != nil || proof.Settled {
					t.Fatal("active/pending/unknown workspace archive accepted", phase, proof.Blockers, e)
				}
			}
			d, e = manager.Open(directoryHome, model.Resources{CPU: 2, Disk: 1 << 20})
			if e != nil {
				t.Fatal(e)
			}
			m, e = manager.Open(memberHome, model.Resources{CPU: 2, Disk: 1 << 20})
			if e != nil {
				t.Fatal(e)
			}
			if phase != "joined" {
				v, e := d.WorkspaceRead(ctx)
				if e != nil {
					t.Fatal(e)
				}
				for _, op := range v.Operations {
					if op.Key == admit.Key {
						if _, e = d.WorkspaceCancelAdmission(ctx, id, p.Host, op.Key, op.Revision); e != nil {
							t.Fatal(e)
						}
					}
				}
				join, _, e = m.WorkspaceLookup(ctx, id, join.Key)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = m.WorkspaceCancelJoin(ctx, id, join.Key, join.Revision); e != nil {
					t.Fatal(e)
				}
			}
			for _, node := range []*manager.Manager{d, m} {
				v, e := node.WorkspaceRead(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = node.WorkspaceRetire(ctx, id, workspace.MutationRequest{Key: "explicit-local-retire", Revision: v.Instances[0].Revision, Reason: "settled local retirement; other authority cleanup is not implied"}); e != nil {
					t.Fatal(e)
				}
				if e = node.Close(); e != nil {
					t.Fatal(e)
				}
			}
			for _, home := range []string{directoryHome, memberHome} {
				proof, e := prove(t, home)
				if e != nil || !proof.Settled {
					t.Fatal("complete explicit retirement refused", phase, proof.Blockers, e)
				}
			}
		})
	}
}
