package server

import (
	"context"
	"encoding/json"
	"fmt"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
	"getarm.org/internal/nodestop"
)

// Uninstall uses the same authenticated direct-peer snapshot as Stop. The
// native recovery helper can finish only after this original admission closes.
func (m *meshService) dispatchUninstall(ctx context.Context, peer mesh.Peer, request mesh.Request, identity mesh.Identity, peers []mesh.PeerImpact) (any, error) {
	if peer.State != "active" || !peer.Proven || request.Expected == nil || request.Expected.Pair != peer.Pair || request.Expected.Revision != peer.Revision || request.Host != identity.Host {
		return nil, model.Err("node_context_changed", "exact active proven destination enrollment required")
	}
	lookup := request.Operation == "node_uninstall_lookup"
	if lookup {
		if !peer.Grant.Has("node-uninstall") && !peer.Grant.Has("observe-own") && !peer.Grant.Has("observe-all") {
			return nil, model.Err("forbidden", "current own-receipt read permission required")
		}
	} else if !peer.Grant.Has("node-uninstall") || !peer.Grant.Has("node-stop") {
		return nil, model.Err("forbidden", "separate current node-uninstall and node-stop grants are required")
	}
	s := m.owner
	switch request.Operation {
	case "node_uninstall_lookup":
		var q mesh.UninstallLookupRequest
		if e := nodestop.Decode(request.Params, &q); e != nil {
			return nil, e
		}
		r, e := mesh.LookupUninstall(q.Transfer, q.Key)
		if e != nil {
			return nil, e
		}
		actor := r.Plan.Stop.Plan.Actor
		if actor.Host != peer.Identity.Host || actor.Certificate != peer.Identity.Fingerprint || actor.SPKI != peer.Identity.SPKI || actor.Pair != peer.Pair || r.Plan.Stop.Plan.Target.Host != identity.Host {
			return nil, model.Err("forbidden", "original uninstall belongs to another enrollment")
		}
		return mesh.UninstallResult{Receipt: r, Detail: "Original target admission receipt; no Stop, helper creation or removal repeated."}, nil

	case "node_uninstall_plan":
		var q mesh.UninstallPrepare
		if e := nodestop.Decode(request.Params, &q); e != nil {
			return nil, e
		}
		path, e := s.runtimeExecutable.VerifiedPath()
		if e != nil || path != mesh.UninstallInstalledPath(q.Transfer) {
			return nil, model.Err("uninstall_unavailable", "running binary is not this exact managed publication")
		}
		raw, e := json.Marshal(stopPlanRequest{Key: mesh.UninstallStopKey(q.Key), Reason: q.Reason, Policy: q.Policy})
		if e != nil {
			return nil, e
		}
		stopRequest := request
		stopRequest.Operation = "node_stop_plan"
		stopRequest.Key = ""
		stopRequest.Params = raw
		prepared, e := m.dispatchStop(ctx, peer, stopRequest, identity, peers)
		if e != nil {
			return nil, e
		}
		stop, ok := prepared.(nodestop.Prepared)
		if !ok {
			return nil, fmt.Errorf("unexpected Stop preparation")
		}
		return mesh.PrepareUninstall(q.Transfer, q.Key, q.Reason, stop)
	case "node_uninstall_apply":
		var q mesh.UninstallApply
		if e := nodestop.Decode(request.Params, &q); e != nil {
			return nil, e
		}
		hash, e := q.Plan.Digest()
		if e != nil || hash != q.SHA256 || q.Key != q.Plan.Key || request.Key != q.Key {
			return nil, model.Err("review_changed", "exact uninstall key/digest required")
		}
		target, e := s.nodeStopBinding()
		if e != nil {
			return nil, e
		}
		actor := q.Plan.Stop.Plan.Actor
		if target != q.Plan.Stop.Plan.Target || actor.Host != peer.Identity.Host || actor.Certificate != peer.Identity.Fingerprint || actor.SPKI != peer.Identity.SPKI || actor.Pair != peer.Pair || actor.GrantRevision != peer.Revision {
			return nil, model.Err("node_context_changed", "uninstall target or actor changed")
		}
		// A known key is inspection only, including an admission whose response was
		// lost. It cannot repeat either Stop or recovery-copy creation.
		if r, e := mesh.LookupUninstall(q.Plan.Transfer, q.Key); e == nil {
			if r.SHA256 != hash {
				return nil, model.Err("idempotency_conflict", "uninstall key differs")
			}
			return r, nil
		} else if !mesh.UninstallMissing(e) {
			return nil, e
		}
		leave, e := s.beginStopWork("inspection")
		if e != nil {
			return nil, e
		}
		defer leave()
		r, e := mesh.BeginUninstall(ctx, q.Plan)
		if e != nil {
			return r, e
		}
		// Copying can span time while the enrollment lock stays held. Expiry and
		// request cancellation are still checked before the separate Stop admission.
		if e = ctx.Err(); e != nil {
			return r, e
		}
		if _, e = mesh.ValidateIdentity(peer.Identity); e != nil {
			return r, e
		}
		if _, e = mesh.ValidateIdentity(identity); e != nil {
			return r, e
		}
		raw, e := json.Marshal(nodestop.Apply{Key: q.Plan.Stop.Plan.Key, Plan: q.Plan.Stop.Plan, ReviewSHA256: q.Plan.Stop.SHA256})
		if e != nil {
			return r, e
		}
		stopRequest := request
		stopRequest.Operation = "node_stop_apply"
		stopRequest.Key = q.Plan.Stop.Plan.Key
		stopRequest.Params = raw
		_, stopErr := m.dispatchStop(ctx, peer, stopRequest, identity, peers)
		updated, saveErr := mesh.MarkUninstallStop(q.Plan, stopErr == nil)
		if saveErr != nil {
			return r, saveErr
		}
		if stopErr != nil {
			return updated, model.Err("node_uninstall_incomplete", fmt.Sprintf("original Stop outcome retained; inspect uninstall key; no retry: %v", stopErr))
		}
		return updated, nil
	default:
		return nil, model.Err("invalid_request", "unknown uninstall method")
	}
}
