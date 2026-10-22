package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
	api "github.com/yrashk/arm/internal/workspaceapi"
)

const maxWorkspaceNativeReply = 48 << 10

func workspaceBounded(v any) (any, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	if len(raw) > maxWorkspaceNativeReply {
		return nil, model.Err("response_too_large", "complete workspace reply exceeds48KiB; select a bounded section; no records truncated")
	}
	return v, nil
}
func workspaceRoot(v workspace.Records, id string) (workspace.Instance, error) {
	for _, root := range v.Instances {
		if root.ID == id {
			return root, nil
		}
	}
	return workspace.Instance{}, model.Err("not_found", "exact local workspace binding not found")
}
func workspaceOperationLookup(v workspace.Records, q api.Lookup) (workspace.Operation, bool, error) {
	var out workspace.Operation
	found := false
	for _, op := range v.Operations {
		if op.Workspace == q.Workspace && op.Key == q.Key && (q.Principal == "" || op.Principal == q.Principal) {
			if found {
				return out, false, model.Err("ambiguous_key", "key has multiple principal namespaces; inspect and select exact principal")
			}
			out = op.View()
			found = true
		}
	}
	return out, found, nil
}
func (s *Server) workspaceOperation(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	ctx, done, err := s.beginWorkspaceCall(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	decode := func(v any) error {
		if len(raw) == 0 || len(raw) > 60<<10 {
			return model.Err("invalid_request", "one bounded workspace request required")
		}
		if e := workspace.Decode(raw, v); e != nil {
			return model.Err("invalid_request", e.Error())
		}
		return nil
	}
	switch operation {
	case "workspace_list":
		if e := decode(&struct{}{}); e != nil {
			return nil, e
		}
		v, e := s.Manager.WorkspaceRead(ctx)
		if e != nil {
			return nil, e
		}
		out := api.List{Initialized: v.Initialized, Workspaces: []api.Summary{}}
		for _, root := range v.Instances {
			r := api.Summary{Workspace: root.ID, Generation: root.Generation, Name: root.Name, Role: root.Role, State: root.State, Revision: root.Revision, Directory: root.Directory.Host, Updated: root.Updated}
			for _, m := range v.Members {
				if m.Workspace == root.ID && m.State == "active" {
					r.Members++
				}
			}
			for _, op := range v.Operations {
				if op.Workspace == root.ID && op.Phase != "completed" && op.Phase != "cancelled" && op.Phase != "declined" {
					r.Unresolved++
				}
			}
			out.Workspaces = append(out.Workspaces, r)
		}
		return workspaceBounded(out)
	case "workspace_inspect":
		var q api.InspectRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
		if q.Offset < 0 || q.Limit < 1 || q.Limit > 20 {
			return nil, model.Err("invalid_request", "offset>=0 and limit1..20 required")
		}
		v, e := s.Manager.WorkspaceRead(ctx)
		if e != nil {
			return nil, e
		}
		root, e := workspaceRoot(v, q.Workspace)
		if e != nil {
			return nil, e
		}
		items := []json.RawMessage{}
		add := func(value any) { b, _ := json.Marshal(value); items = append(items, b) }
		switch q.Section {
		case "operations":
			for _, o := range v.Operations {
				if o.Workspace == q.Workspace {
					add(o.View())
				}
			}
		case "members":
			for _, m := range v.Members {
				if m.Workspace == q.Workspace {
					add(m)
				}
			}
		case "trusted":
			for _, m := range v.Trusted {
				if m.Workspace == q.Workspace {
					add(m)
				}
			}
		case "changes":
			for _, c := range v.Changes {
				if c.Workspace == q.Workspace {
					add(c)
				}
			}
		case "denies":
			for _, d := range v.Denies {
				if d.Workspace == q.Workspace {
					add(d)
				}
			}
		default:
			return nil, model.Err("invalid_request", "section must be operations, members, trusted, changes or denies")
		}
		start := min(q.Offset, len(items))
		end := min(start+q.Limit, len(items))
		out := api.Page{Workspace: root, Section: q.Section, Offset: start, Total: len(items), Items: items[start:end]}
		if end < len(items) {
			out.Next = &end
		}
		return workspaceBounded(out)
	case "workspace_lookup":
		var q api.Lookup
		if e := decode(&q); e != nil {
			return nil, e
		}
		v, e := s.Manager.WorkspaceRead(ctx)
		if e != nil {
			return nil, e
		}
		op, found, e := workspaceOperationLookup(v, q)
		if e != nil {
			return nil, e
		}
		r := api.LookupResult{Found: found}
		if found {
			r.Operation = &op
		}
		return workspaceBounded(r)
	case "workspace_create":
		var q workspace.CreateRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
		v, e := s.Manager.WorkspacePrepareCreate(ctx, q)
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_join":
		var q api.Join
		if e := decode(&q); e != nil {
			return nil, e
		}
		if e := q.Invitation.Validate(time.Now()); e != nil {
			return nil, e
		}
		m, e := s.workspaceService()
		if e != nil {
			return nil, e
		}
		peer, e := m.store.Resolve(q.Invitation.Directory.Host)
		if e != nil {
			return nil, e
		}
		i := q.Invitation.Directory
		if peer.State != "active" || !peer.Proven || peer.Identity.Fingerprint != i.Fingerprint || peer.Identity.SPKI != i.SPKI || peer.Identity.Certificate != i.Certificate {
			return nil, model.Err("forbidden", "invitation directory must match the existing active proven direct peer; no peer was added")
		}
		join := workspace.JoinRequest{Key: q.Key, Workspace: q.Invitation.Workspace, Generation: q.Invitation.Generation, Name: q.Invitation.Name, Directory: i, Policy: q.Invitation.Policy, PolicyRevision: q.Invitation.PolicyRevision, Introducer: i.Host, RequestIntroducer: q.Introducer, Reason: q.Reason}
		v, e := s.Manager.WorkspacePrepareJoin(ctx, join)
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_decide", "workspace_recovery_decide":
		var q api.Decision
		if e := decode(&q); e != nil {
			return nil, e
		}
		var v workspace.Operation
		var e error
		if operation == "workspace_decide" {
			v, e = s.Manager.WorkspaceDecide(ctx, q.Workspace, q.Key, q.Revision, q.Review, q.Approve)
		} else {
			v, e = s.Manager.WorkspaceDecideRecovery(ctx, q.Workspace, q.Key, q.Revision, q.Review, q.Approve)
		}
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_invitation":
		var q api.Selector
		if e := decode(&q); e != nil {
			return nil, e
		}
		v, e := s.Manager.WorkspaceInvitation(ctx, q.Workspace)
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_consent":
		var q api.Key
		if e := decode(&q); e != nil {
			return nil, e
		}
		v, e := s.Manager.WorkspaceConsent(ctx, q.Workspace, q.Key)
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_admission_review":
		var q workspace.NativeAdmissionRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
		v, e := s.Manager.WorkspaceAdmissionReview(ctx, q)
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_admit":
		var q api.AdmissionCommit
		if e := decode(&q); e != nil {
			return nil, e
		}
		op, created, e := s.Manager.WorkspaceAdmitReviewed(ctx, q.Request, q.Review)
		if e != nil {
			return nil, e
		}
		if !created {
			return workspaceBounded(op)
		}
		records, e := s.Manager.WorkspaceRead(ctx)
		if e != nil {
			return nil, e
		}
		root, e := workspaceRoot(records, op.Workspace)
		if e != nil {
			return nil, e
		}
		p := workspace.Principal{Workspace: root.ID, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: root.SelfGeneration, Fingerprint: root.Self.Fingerprint}
		op, e = s.Manager.WorkspaceActivate(ctx, p, op.Key, workspaceProofTransport{s})
		if e != nil {
			return nil, e
		}
		return workspaceBounded(op)
	case "workspace_refresh":
		var q api.Selector
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.workspaceRefresh(ctx, q.Workspace)
	case "workspace_finalize":
		var q api.Finalize
		if e := decode(&q); e != nil {
			return nil, e
		}
		v, e := s.Manager.WorkspaceFinalize(ctx, q.Workspace, q.Key, q.Revision)
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_deny", "workspace_revoke", "workspace_retire":
		var q api.Mutation
		if e := decode(&q); e != nil {
			return nil, e
		}
		var v workspace.Operation
		var e error
		switch operation {
		case "workspace_deny":
			v, e = s.Manager.WorkspaceDeny(ctx, q.Workspace, q.Request)
		case "workspace_revoke":
			v, e = s.Manager.WorkspaceRevoke(ctx, q.Workspace, q.Request)
		case "workspace_retire":
			v, e = s.Manager.WorkspaceRetire(ctx, q.Workspace, q.Request)
		}
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_cancel_join", "workspace_cancel_admission", "workspace_cancel_recovery":
		var q api.Cancel
		if e := decode(&q); e != nil {
			return nil, e
		}
		var v workspace.Operation
		var e error
		switch operation {
		case "workspace_cancel_join":
			if q.Source != "" {
				return nil, model.Err("invalid_request", "join cancellation has no source selector")
			}
			v, e = s.Manager.WorkspaceCancelJoin(ctx, q.Workspace, q.Key, q.Revision)
		case "workspace_cancel_admission":
			v, e = s.Manager.WorkspaceCancelAdmission(ctx, q.Workspace, q.Source, q.Key, q.Revision)
		case "workspace_cancel_recovery":
			if q.Source != "" {
				return nil, model.Err("invalid_request", "recovery cancellation has no source selector")
			}
			v, e = s.Manager.WorkspaceCancelRecovery(ctx, q.Workspace, q.Key, q.Revision)
		}
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_recovery_prepare_auto":
		var q api.RecoveryPrepare
		if e := decode(&q); e != nil {
			return nil, e
		}
		v, e := s.workspacePrepareRecovery(ctx, q)
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_recovery_prepare":
		var q api.Recovery
		if e := decode(&q); e != nil {
			return nil, e
		}
		v, e := s.Manager.WorkspacePrepareRecovery(ctx, q.Kind, q.Request)
		if e != nil {
			return nil, e
		}
		return workspaceBounded(v)
	case "workspace_recovery_run":
		var q api.RecoveryRun
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.workspaceRecoveryRun(ctx, q)
	default:
		return nil, model.Err("invalid_request", "unknown native workspace operation")
	}
}
