package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/inferal-oss/arm/internal/workspace"
)

func openStoppedWorkspace(t *testing.T, home string) (*sql.DB, *workspace.Store, workspace.Instance) {
	t.Helper()
	db, e := sql.Open("sqlite", filepath.Join(home, "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	var raw string
	if e = db.QueryRow(`SELECT data FROM workspace_instances LIMIT 1`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var root workspace.Instance
	if e = workspace.Decode([]byte(raw), &root); e != nil {
		t.Fatal(e)
	}
	store, e := workspace.Open(context.Background(), db, root.Authority, root.Self, nil)
	if e != nil {
		t.Fatal(e)
	}
	return db, store, root
}
func TestWorkspaceRecoveryCompletedProofThenReadOnlyChildKeepsHistoricalCompletion(t *testing.T) {
	ctx := context.Background()
	directoryHome, id := workspaceProofFixture(t, "active")
	memberHome, _ := workspaceProofFixture(t, "declined")
	db, ds, root := openStoppedWorkspace(t, directoryHome)
	mb, ms, memberRoot := openStoppedWorkspace(t, memberHome)
	join, e := ms.PrepareJoin(ctx, workspace.JoinRequest{Key: "join", Workspace: id, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: root.Self.Host, Reason: "explicit candidate consent"})
	if e != nil {
		t.Fatal(e)
	}
	join, e = ms.Decide(ctx, id, join.Key, join.Revision, join.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	p := workspace.Principal{Workspace: id, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: 1, Fingerprint: root.Self.Fingerprint}
	admit, e := ds.PrepareAdmission(ctx, p, workspace.AdmissionRequest{Key: "admit", Candidate: memberRoot.Self, ConsentHash: join.ReviewHash, HostInbox: true, PolicyRevision: root.PolicyRevision, Reason: "native approved exact admission"})
	if e != nil {
		t.Fatal(e)
	}
	challenge, started, e := ds.BeginProof(ctx, p, admit.Key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ms.ProveJoin(ctx, root.Directory, challenge); e != nil {
		t.Fatal(e)
	}
	if e = ds.RecordProofFailure(ctx, p, admit.Key, started.Revision); e != nil {
		t.Fatal(e)
	}
	ds, e = workspace.Open(ctx, db, root.Authority, root.Self, nil)
	if e != nil {
		t.Fatal(e)
	}
	ms, e = workspace.Open(ctx, mb, memberRoot.Authority, memberRoot.Self, nil)
	if e != nil {
		t.Fatal(e)
	}
	records, e := ms.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var original workspace.Operation
	for _, op := range records.Operations {
		if op.Workspace == id && op.Key == join.Key {
			original = op
		}
	}
	jq := workspace.RecoveryRequest{Key: "join-recovery", Workspace: id, Generation: root.Generation, Original: workspace.OperationRef{Principal: original.Principal, Key: original.Key, RequestHash: original.RequestHash, Revision: original.Revision}, Attempt: 1, Consent: join.ReviewHash, DirectoryPin: root.Directory.Fingerprint, CandidatePin: memberRoot.Self.Fingerprint, PolicyRevision: root.PolicyRevision, HostInbox: true, PermitProof: true, Reason: "new candidate recovery consent"}
	jc, e := ms.PrepareRecovery(ctx, "resume_join", jq)
	if e != nil {
		t.Fatal(e)
	}
	jc, e = ms.DecideRecovery(ctx, id, jc.Key, jc.Revision, jc.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	records, e = ds.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, op := range records.Operations {
		if op.Key == admit.Key {
			original = op
		}
	}
	aq := workspace.RecoveryRequest{Key: "proof-recovery", Workspace: id, Generation: root.Generation, Original: workspace.OperationRef{Principal: original.Principal, Key: original.Key, RequestHash: original.RequestHash, Revision: original.Revision}, Attempt: 1, Consent: join.ReviewHash, DirectoryPin: root.Directory.Fingerprint, CandidatePin: memberRoot.Self.Fingerprint, PolicyRevision: root.PolicyRevision, HostInbox: true, PermitProof: true, CandidateAuthorization: jc.ReviewHash, Reason: "one explicit fresh proof"}
	ac, e := ds.PrepareRecovery(ctx, "resume_admission", aq)
	if e != nil {
		t.Fatal(e)
	}
	ac, e = ds.DecideRecovery(ctx, id, ac.Key, ac.Revision, ac.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	challenge, ac, e = ds.BeginRecoveryProof(ctx, id, ac.Key)
	if e != nil {
		t.Fatal(e)
	}
	proof, e := ms.ProveJoin(ctx, root.Directory, challenge)
	if e != nil {
		t.Fatal(e)
	}
	ac, e = ds.CompleteRecoveryProof(ctx, id, ac.Key, ac.Revision, proof)
	if e != nil {
		t.Fatal(e)
	}
	records, e = ds.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, op := range records.Operations {
		if op.Key == admit.Key {
			original = op
		}
	}
	aq.Key = "readonly-recovery"
	aq.Original.Revision = original.Revision
	aq.Attempt = 2
	aq.Previous = ac.Key
	aq.PermitProof = false
	aq.CandidateAuthorization = ""
	aq.Reason = "fresh explicit read-only reconciliation of known completion"
	read, e := ds.PrepareRecovery(ctx, "resume_admission", aq)
	if e != nil {
		t.Fatal(e)
	}
	read, e = ds.DecideRecovery(ctx, id, read.Key, read.Revision, read.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ds.ReconcileAdmissionRecovery(ctx, id, read.Key, read.Revision); e != nil {
		t.Fatal(e)
	}
	records, e = ds.Read(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range records.Instances {
		if r.ID == id {
			if _, e = ds.Retire(ctx, id, workspace.MutationRequest{Key: "retire", Revision: r.Revision, Reason: "explicit local retirement of completed recovery family"}); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if e = mb.Close(); e != nil {
		t.Fatal(e)
	}
	result, e := prove(t, directoryHome)
	if e != nil || !result.Complete || !result.Settled {
		t.Fatal("legitimate historical completion invalidated by later read-only child", result.Blockers, e)
	}
}
