package server

import (
	"context"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
	api "github.com/yrashk/arm/internal/workspaceapi"
	wire "github.com/yrashk/arm/internal/workspacetransport"
)

func (s *Server) workspaceRefresh(ctx context.Context, id string) (api.RefreshResult, error) {
	records, e := s.Manager.WorkspaceRead(ctx)
	if e != nil {
		return api.RefreshResult{}, e
	}
	root, e := workspaceRoot(records, id)
	if e != nil {
		return api.RefreshResult{}, e
	}
	ticket, e := s.Manager.WorkspaceBeginRefresh(ctx, id)
	if e != nil {
		return api.RefreshResult{}, e
	}
	defer s.Manager.WorkspaceAbandonRefresh(context.Background(), ticket.Nonce)
	conn, o, admission, e := s.workspaceDial(ctx, id, root.Generation, root.Self.Host, root.Directory.Host, wire.ClassDirectory, "")
	if e != nil {
		return api.RefreshResult{}, e
	}
	defer conn.Close()
	q := workspaceWireRequest(o, wire.OperationRefresh)
	q.Refresh = &ticket
	if e = wire.WriteRequest(conn, o, q); e != nil {
		return api.RefreshResult{}, e
	}
	pages := []workspace.Page{}
	count := 0
	for {
		r, e := wire.ReadReply(conn, q)
		if e != nil {
			return api.RefreshResult{}, e
		}
		if e = wireReplyError(r); e != nil {
			return api.RefreshResult{}, e
		}
		page := *r.Page
		if page.Offset != count {
			return api.RefreshResult{}, model.Err("invalid_snapshot", "directory page ordering differs")
		}
		pages = append(pages, page)
		count += len(page.Snapshot.Members)
		if count == page.Total {
			break
		}
		if len(pages) >= workspace.MaxMembers {
			return api.RefreshResult{}, model.Err("invalid_snapshot", "too many directory pages")
		}
	}
	snapshot, e := workspace.Assemble(pages)
	if e != nil {
		return api.RefreshResult{}, e
	}
	if e = s.Manager.WorkspaceAccept(ctx, admission.Remote, snapshot); e != nil {
		return api.RefreshResult{}, e
	}
	return api.RefreshResult{Workspace: id, Revision: snapshot.Revision, Members: len(snapshot.Members), ValidUntil: snapshot.ValidUntil}, nil
}
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
