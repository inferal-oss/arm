package server

import (
	"context"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
	api "github.com/yrashk/arm/internal/workspaceapi"
)

func (s *Server) workspacePrepareRecovery(ctx context.Context, q api.RecoveryPrepare) (workspace.Operation, error) {
	v, e := s.Manager.WorkspaceRead(ctx)
	if e != nil {
		return workspace.Operation{}, e
	}
	root, e := workspaceRoot(v, q.Workspace)
	if e != nil {
		return workspace.Operation{}, e
	}
	original, found, e := workspaceOperationLookup(v, api.Lookup{Workspace: q.Workspace, Key: q.OriginalKey, Principal: q.OriginalPrincipal})
	if e != nil {
		return workspace.Operation{}, e
	}
	if !found {
		return workspace.Operation{}, model.Err("not_found", "original key not found")
	}
	if q.Kind != "resume_join" && q.Kind != "resume_admission" || q.Kind == "resume_join" && original.Kind != "join" || q.Kind == "resume_admission" && original.Kind != "admit" {
		return workspace.Operation{}, model.Err("invalid_request", "requested recovery kind differs from original")
	}
	// An existing child is an inspection-only exact retry, never another attempt.
	for _, child := range v.Operations {
		if child.Workspace != q.Workspace || child.Key != q.Key || child.Kind != q.Kind {
			continue
		}
		var saved workspace.RecoveryRequest
		if e = workspace.Decode(child.Request, &saved); e != nil {
			return workspace.Operation{}, e
		}
		candidateAuthorization := ""
		if q.CandidateConsent != nil && q.CandidateConsent.Recovery != nil {
			candidateAuthorization = q.CandidateConsent.Recovery.ReviewHash
		}
		if saved.Original.Principal != original.Principal || saved.Original.Key != original.Key || saved.Original.RequestHash != original.RequestHash || saved.Original.Revision != q.OriginalRevision || saved.Reason != q.Reason || saved.PermitProof != q.PermitProof || saved.CandidateAuthorization != candidateAuthorization {
			return workspace.Operation{}, model.Err("idempotency_conflict", "recovery key belongs to a different immutable request")
		}
		return child.View(), nil
	}
	if original.Revision != q.OriginalRevision {
		return workspace.Operation{}, model.Err("stale_revision", "original changed; inspect before preparing a separately keyed recovery")
	}
	request := workspace.RecoveryRequest{Key: q.Key, Workspace: root.ID, Generation: root.Generation, Original: workspace.OperationRef{Principal: original.Principal, Key: original.Key, RequestHash: original.RequestHash, Revision: original.Revision}, Attempt: 1, DirectoryPin: root.Directory.Fingerprint, PolicyRevision: root.PolicyRevision, PermitProof: q.PermitProof, Reason: q.Reason}
	kind := ""
	switch original.Kind {
	case "join":
		kind = "resume_join"
		if q.CandidateConsent != nil {
			return workspace.Operation{}, model.Err("invalid_request", "local candidate recovery has no remote consent input")
		}
		if original.Review == nil {
			return workspace.Operation{}, model.Err("workspace_state_invalid", "original candidate review missing")
		}
		request.CandidatePin = root.Self.Fingerprint
		request.Consent = root.ConsentHash
		request.Introducer = original.Review.Introducer
		request.HostInbox = original.Review.HostInbox
	case "admit":
		kind = "resume_admission"
		var admitted workspace.AdmissionRequest
		if e = workspace.Decode(original.Request, &admitted); e != nil {
			return workspace.Operation{}, e
		}
		request.CandidatePin = admitted.Candidate.Fingerprint
		request.Consent = admitted.ConsentHash
		request.Introducer = admitted.Introducer
		request.HostInbox = admitted.HostInbox
		if original.Phase == "completed" && q.PermitProof {
			return workspace.Operation{}, model.Err("invalid_request", "completed directory admission must reconcile read-only; another probe is unnecessary")
		}
		if q.PermitProof {
			consent := q.CandidateConsent
			if consent == nil {
				return workspace.Operation{}, model.Err("invalid_request", "fresh exact candidate recovery consent required for a new proof")
			}
			if e = consent.Validate(time.Now()); e != nil {
				return workspace.Operation{}, e
			}
			if consent.Recovery == nil || consent.Workspace != root.ID || consent.Generation != root.Generation || consent.Directory != root.Directory || consent.Candidate != admitted.Candidate || consent.Consent != admitted.ConsentHash || consent.PolicyRevision != admitted.PolicyRevision || consent.Introducer != admitted.Introducer || consent.HostInbox != admitted.HostInbox {
				return workspace.Operation{}, model.Err("review_changed", "candidate recovery consent does not match exact original admission")
			}
			request.CandidateAuthorization = consent.Recovery.ReviewHash
		} else if q.CandidateConsent != nil {
			return workspace.Operation{}, model.Err("invalid_request", "read-only directory reconciliation must not carry candidate proof consent")
		}
	default:
		return workspace.Operation{}, model.Err("invalid_request", "only original join/admit families can resume")
	}
	for _, child := range v.Operations {
		if child.Workspace != root.ID || (child.Kind != "resume_join" && child.Kind != "resume_admission") {
			continue
		}
		var r workspace.RecoveryRequest
		if e = workspace.Decode(child.Request, &r); e != nil {
			return workspace.Operation{}, e
		}
		if r.Original.Principal == original.Principal && r.Original.Key == original.Key && r.Original.RequestHash == original.RequestHash && r.Attempt >= request.Attempt {
			request.Attempt = r.Attempt + 1
			request.Previous = child.Key
		}
	}
	return s.Manager.WorkspacePrepareRecovery(ctx, kind, request)
}
