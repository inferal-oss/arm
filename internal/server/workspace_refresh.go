package server

import (
	"context"
	"time"

	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
	api "getarm.org/internal/workspaceapi"
	wire "getarm.org/internal/workspacetransport"
)

func (s *Server) workspaceRecoveryRun(ctx context.Context, q api.RecoveryRun) (any, error) {
	records, e := s.Manager.WorkspaceRead(ctx)
	if e != nil {
		return nil, e
	}
	op, found, e := workspaceOperationLookup(records, api.Lookup{Workspace: q.Workspace, Key: q.Key})
	if e != nil {
		return nil, e
	}
	if !found {
		return nil, model.Err("not_found", "exact local recovery key not found")
	}
	if op.Kind != "resume_join" && op.Kind != "resume_admission" {
		return nil, model.Err("invalid_request", "recovery child key required")
	}
	if op.Phase == "completed" {
		return workspaceBounded(op)
	}
	if op.Revision != q.Revision {
		return nil, model.Err("stale_revision", "recovery changed; inspect exact key")
	}
	var request workspace.RecoveryRequest
	if e = workspace.Decode(op.Request, &request); e != nil {
		return nil, e
	}
	if op.Kind == "resume_admission" {
		if !request.PermitProof {
			return s.Manager.WorkspaceReconcileAdmission(ctx, q.Workspace, q.Key, q.Revision)
		}
		c, dispatch, e := s.Manager.WorkspaceBeginRecoveryProof(ctx, q.Workspace, q.Key)
		if e != nil {
			return nil, e
		}
		proof, e := (workspaceProofTransport{s}).Prove(ctx, c)
		if e != nil {
			record, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			_ = s.Manager.WorkspaceRecordRecoveryFailure(record, q.Workspace, q.Key, dispatch.Revision)
			return nil, model.Err("workspace_outcome_unknown", "recovery proof interrupted; inspect exact child key "+q.Key+"; no automatic reprobe")
		}
		result, e := s.Manager.WorkspaceCompleteRecoveryProof(ctx, q.Workspace, q.Key, dispatch.Revision, proof)
		if e != nil {
			return nil, e
		}
		return workspaceBounded(result)
	}
	root, e := workspaceRoot(records, q.Workspace)
	if e != nil {
		return nil, e
	}
	ticket, e := s.Manager.WorkspaceBeginRecoveryStatus(ctx, q.Workspace, q.Key)
	if e != nil {
		return nil, e
	}
	conn, o, admission, e := s.workspaceDial(ctx, root.ID, root.Generation, root.Self.Host, root.Directory.Host, wire.ClassAdmissionStatus, root.ConsentHash)
	if e != nil {
		return nil, e
	}
	r := workspaceWireRequest(o, wire.OperationStatus)
	r.Status = &ticket
	e = wire.WriteRequest(conn, o, r)
	if e != nil {
		conn.Close()
		return nil, e
	}
	reply, e := wire.ReadReply(conn, r)
	conn.Close()
	if e != nil {
		return nil, e
	}
	if e = wireReplyError(reply); e != nil {
		return nil, e
	}
	if e = s.Manager.WorkspaceAcceptRecoveryStatus(ctx, admission.Remote, *reply.Status); e != nil {
		return nil, e
	}
	// A pending directory admission is a read-only diagnosis. It never triggers
	// a proof here; the directory owner must review their own separately keyed child.
	if reply.Status.State != "active" {
		return workspaceBounded(*reply.Status)
	}
	if _, e = s.workspaceRefresh(ctx, root.ID); e != nil {
		return nil, e
	}
	// Status acceptance and refresh can append observation history but do not
	// rewrite the child's immutable approved request/revision.
	result, e := s.Manager.WorkspaceFinalizeRecovery(ctx, root.ID, q.Key, q.Revision)
	if e != nil {
		return nil, e
	}
	return workspaceBounded(result)
}
