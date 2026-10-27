package database_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"getarm.org/internal/manager"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
)

func recoveryProofFixture(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	directoryHome, id := workspaceProofFixture(t, "active")
	d, e := manager.Open(directoryHome, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	v, e := d.WorkspaceRead(ctx)
	if e != nil {
		t.Fatal(e)
	}
	root := v.Instances[0]
	if e = d.Close(); e != nil {
		t.Fatal(e)
	}
	memberHome := proofFixture(t, false)
	m, e := manager.Open(memberHome, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	status, e := m.Status()
	if e != nil {
		t.Fatal(e)
	}
	node, e := mesh.InitPrepared(memberHome, status.Host, "recovery-member-fixture", "127.0.0.1:23459", "127.0.0.1:23459")
	if e != nil {
		t.Fatal(e)
	}
	node.Close()
	join, e := m.WorkspacePrepareJoin(ctx, workspace.JoinRequest{Key: "join", Workspace: id, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: root.Self.Host, Reason: "explicit recovery candidate consent"})
	if e != nil {
		t.Fatal(e)
	}
	self := join.Review.Source
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	// Reopen the complete real central ledgers with the public workspace writer;
	// this fixture does not construct or patch fake private records.
	db, e := sql.Open("sqlite", filepath.Join(directoryHome, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ds, e := workspace.Open(ctx, db, root.Authority, root.Self, nil)
	if e != nil {
		t.Fatal(e)
	}
	mb, e := sql.Open("sqlite", filepath.Join(memberHome, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer mb.Close()
	mb.SetMaxOpenConns(1)
	a := workspace.Authority{Host: self.Host, UID: root.Authority.UID}
	ms, e := workspace.Open(ctx, mb, a, self, nil)
	if e != nil {
		t.Fatal(e)
	}
	// The old pre-restart review must be declined. Build a separate candidate
	// home to exercise completed admission reconciliation after a real restart.
	if _, e = ms.Decide(ctx, id, join.Key, join.Revision, join.ReviewHash, false); e != nil {
		t.Fatal(e)
	}
	if _, e = ms.Read(ctx); e != nil {
		t.Fatal(e)
	}
	// Use a new workspace generation created by its real directory writer; no
	// retired identity/tombstone is deleted or silently readmitted.
	create, e := ds.PrepareCreate(ctx, workspace.CreateRequest{Key: "recovery-root", Name: "recovery reset evidence", Policy: workspace.DefaultPolicy(), Reason: "second independently reviewed directory generation"})
	if e != nil {
		t.Fatal(e)
	}
	create, e = ds.Decide(ctx, create.Workspace, create.Key, create.Revision, create.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	records, e := ds.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range records.Instances {
		if r.ID == create.Workspace {
			root = r
		}
	}
	id = root.ID
	join, e = ms.PrepareJoin(ctx, workspace.JoinRequest{Key: "current-join", Workspace: id, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: root.Self.Host, Reason: "current candidate approval"})
	if e != nil {
		t.Fatal(e)
	}
	join, e = ms.Decide(ctx, id, join.Key, join.Revision, join.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	p := workspace.Principal{Workspace: id, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: 1, Fingerprint: root.Self.Fingerprint}
	admit, e := ds.PrepareAdmission(ctx, p, workspace.AdmissionRequest{Key: "admit", Candidate: self, ConsentHash: join.ReviewHash, HostInbox: true, PolicyRevision: root.PolicyRevision, Reason: "exact directory admission"})
	if e != nil {
		t.Fatal(e)
	}
	service := workspace.NewService(ctx, ds)
	defer service.Close()
	if _, e = service.Activate(ctx, p, admit.Key, proofWorkspaceTransport(func(ctx context.Context, c workspace.Challenge) (workspace.Proof, error) {
		return ms.ProveJoin(ctx, root.Directory, c)
	})); e != nil {
		t.Fatal(e)
	}
	// Restart only the candidate after directory committed, before finalize.
	ms, e = workspace.Open(ctx, mb, a, self, nil)
	if e != nil {
		t.Fatal(e)
	}
	records, e = ms.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var original workspace.Operation
	for _, op := range records.Operations {
		if op.Workspace == id && op.Key == join.Key {
			original = op
		}
	}
	q := workspace.RecoveryRequest{Key: "resume-join", Workspace: id, Generation: root.Generation, Original: workspace.OperationRef{Principal: original.Principal, Key: original.Key, RequestHash: original.RequestHash, Revision: original.Revision}, Attempt: 1, Consent: join.ReviewHash, DirectoryPin: root.Directory.Fingerprint, CandidatePin: self.Fingerprint, PolicyRevision: root.PolicyRevision, HostInbox: true, Reason: "read-only reconciliation after restart"}
	child, e := ms.PrepareRecovery(ctx, "resume_join", q)
	if e != nil {
		t.Fatal(e)
	}
	child, e = ms.DecideRecovery(ctx, id, child.Key, child.Revision, child.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	runtime := workspace.NewRuntime(ms)
	defer runtime.Close()
	request, e := runtime.BeginRecoveryStatus(ctx, id, child.Key)
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := ds.OwnAdmissionStatus(ctx, self, request)
	if e != nil {
		t.Fatal(e)
	}
	if e = runtime.AcceptRecoveryStatus(ctx, root.Directory, receipt); e != nil {
		t.Fatal(e)
	}
	refresh, e := runtime.Begin(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := ds.Issue(ctx, refresh)
	if e != nil {
		t.Fatal(e)
	}
	if e = runtime.Accept(ctx, root.Directory, snapshot); e != nil {
		t.Fatal(e)
	}
	if _, e = runtime.FinalizeRecovery(ctx, id, child.Key, child.Revision); e != nil {
		t.Fatal(e)
	}
	records, e = ms.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range records.Instances {
		if r.ID == id {
			_, e = ms.Retire(ctx, id, workspace.MutationRequest{Key: "retire-recovered", Revision: r.Revision, Reason: "explicitly retired local recovered workspace"})
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = mb.Close(); e != nil {
		t.Fatal(e)
	}
	return memberHome, id
}

func TestWorkspaceRecoveryResetProofActualWriterAndCorruption(t *testing.T) {
	for _, damage := range []string{"valid", "marker1", "null-parent", "alias-parent", "missing-parent", "family-fork", "lost-review", "lost-finalize"} {
		t.Run(damage, func(t *testing.T) {
			home, id := recoveryProofFixture(t)
			switch damage {
			case "marker1":
				edit(t, home, `UPDATE metadata SET value='1' WHERE key='workspace_schema'`)
			case "missing-parent":
				edit(t, home, `DELETE FROM workspace_operations WHERE key='current-join'`)
			case "lost-finalize":
				edit(t, home, `DELETE FROM workspace_changes WHERE json_extract(data,'$.kind')='join_finalized'`)
			case "lost-review":
				edit(t, home, `DELETE FROM workspace_changes WHERE json_extract(data,'$.kind')='recovery_decision'`)
			case "null-parent", "alias-parent", "family-fork":
				db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
				if e != nil {
					t.Fatal(e)
				}
				var raw string
				if e = db.QueryRow(`SELECT data FROM workspace_operations WHERE workspace=? AND key='resume-join'`, id).Scan(&raw); e != nil {
					t.Fatal(e)
				}
				var op map[string]any
				if e = json.Unmarshal([]byte(raw), &op); e != nil {
					t.Fatal(e)
				}
				request := op["request"].(map[string]any)
				if damage == "null-parent" {
					request["original"] = nil
				}
				if damage == "alias-parent" {
					request["Original"] = request["original"]
					delete(request, "original")
				}
				if damage == "family-fork" {
					request["attempt"] = float64(2)
					request["previous_recovery_key"] = "missing"
				}
				body, _ := json.Marshal(op)
				req, _ := json.Marshal(request)
				_, e = db.Exec(`UPDATE workspace_operations SET data=?,request=? WHERE workspace=? AND key='resume-join'`, string(body), string(req), id)
				db.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			proof, e := prove(t, home)
			if damage == "valid" {
				if e != nil || !proof.Complete || !proof.Settled {
					t.Fatal("valid recovered retirement rejected", proof.Blockers, e)
				}
			} else if e == nil && proof.Complete && proof.Settled {
				t.Fatal("false recovered settlement accepted", damage)
			}
		})
	}
}
