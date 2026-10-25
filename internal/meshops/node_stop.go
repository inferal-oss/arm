package meshops

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
)

func (q StopDraft) Validate() error {
	host, err := hex.DecodeString(q.Host)
	if err != nil || len(host) != 16 || q.Host != strings.ToLower(q.Host) || strings.TrimSpace(q.Reason) == "" || len(q.Reason) > 4096 || q.Policy.Force && q.Policy.Wait || q.Policy.Timeout < 0 || q.Policy.Timeout > 0 && !q.Policy.Wait {
		return model.Err("invalid_request", "exact host, bounded reason and valid default/wait/force policy required")
	}
	return nil
}
func (a *Coordinator) Stop(key string, q StopDraft, yes, dry bool) (int, error) {
	return a.run(func() (int, error) { return a.stop(key, q, yes, dry) })
}
func (a *Coordinator) stopRPC(host string, scope model.NodeAccessScope, op, key string, params, result any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var response json.RawMessage
	if err = a.call("mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{host, mesh.Request{Expected: &mesh.ExpectedEnrollment{Pair: scope.Pair, Revision: scope.Revision}, Operation: op, Key: key, Params: raw}}, &response); err != nil {
		return err
	}
	return nodestop.Decode(response, result)
}
func (a *Coordinator) stopPinnedPeer(host string) (mesh.Peer, mesh.LifecycleInspection, error) {
	own, err := a.Inspect()
	if err != nil {
		return mesh.Peer{}, own, err
	}
	if !own.Running || own.Config == nil || own.Identity == nil {
		return mesh.Peer{}, own, model.Err("mesh_unavailable", "Stop uses an existing source backend; start it explicitly if needed")
	}
	peer, err := mesh.ResolveConfig(*own.Config, host)
	if err != nil {
		return peer, own, err
	}
	if peer.Identity.Host != host || peer.State != "active" || !peer.Proven {
		return peer, own, model.Err("forbidden", "exact active proven local enrollment required")
	}
	return peer, own, nil
}
func (a *Coordinator) stopLiveScope(peer mesh.Peer, own mesh.LifecycleInspection) (model.NodeAccessScope, error) {
	var scope model.NodeAccessScope
	err := a.call("mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{peer.Identity.Host, mesh.Request{Operation: "access_scope", Params: json.RawMessage(`{}`)}}, &scope)
	if err != nil {
		return scope, err
	}
	if scope.Source != own.Identity.Host || scope.Target != peer.Identity.Host || scope.Pair != peer.Pair || scope.Revision < 1 {
		return scope, model.Err("node_context_changed", "current destination enrollment differs")
	}
	return scope, nil
}
func (a *Coordinator) stopPeer(host string) (mesh.Peer, model.NodeAccessScope, error) {
	peer, own, err := a.stopPinnedPeer(host)
	if err != nil {
		return peer, model.NodeAccessScope{}, err
	}
	scope, err := a.stopLiveScope(peer, own)
	return peer, scope, err
}
func (a *Coordinator) stopConnection(peer mesh.Peer) (*mesh.LifecycleOperation, error) {
	all, err := mesh.LifecycleConnections(a.Home)
	if err != nil {
		return nil, err
	}
	var chosen *mesh.LifecycleOperation
	for _, v := range all {
		if v.RemoteIdentity == nil || v.RemoteHost != peer.Identity.Host || v.Pair != peer.Pair || v.RemoteIdentity.Fingerprint != peer.Identity.Fingerprint || v.RemoteIdentity.SPKI != peer.Identity.SPKI || v.SSHLocator == "" || v.SSHAccount == "" || v.SSHHostKey == "" || v.RemoteHome == "" || v.Binary == "" {
			continue
		}
		if chosen == nil || chosen.At.Before(v.At) {
			copy := v
			chosen = &copy
		}
	}
	return chosen, nil
}
func (a *Coordinator) stop(key string, q StopDraft, yes, dry bool) (int, error) {
	if !mesh.ValidKey(key) {
		return 2, model.Err("invalid_request", "immutable stop key required")
	}
	if err := q.Validate(); err != nil {
		return 2, err
	}
	digest, err := mesh.ReviewDigest(q)
	if err != nil {
		return 1, err
	}
	if a.guard == nil {
		if prior, ok, err := mesh.ReadCoordination(a.Home, key); err != nil {
			return 1, err
		} else if ok {
			if prior.Kind != "stop" || prior.Digest != digest {
				return 1, model.Err("idempotency_conflict", "stop key belongs to another immutable request")
			}
			return 0, a.Print(Project(prior)) // Known keys never prepare or apply again.
		}
	}
	peer, scope, err := a.stopPeer(q.Host)
	if err != nil {
		return 1, err
	}
	if scope.NodeStop == nil || !scope.NodeStop.Supported {
		return 1, model.Err("node_stop_unavailable", "Destination does not currently offer reviewed Stop; inspect node scope")
	}
	if !dry && !(a.Machine && !yes) {
		if err = a.begin(key, "stop", digest, "host:"+q.Host); err != nil {
			return 1, err
		}
		if err = a.recordRequest(StartRequest{Key: key, Kind: "stop", Stop: &q}); err != nil {
			return 1, err
		}
	}
	var prepared nodestop.Prepared
	if err = a.stopRPC(q.Host, scope, "node_stop_plan", "", struct {
		Key    string                `json:"key"`
		Reason string                `json:"reason"`
		Policy model.ShutdownRequest `json:"policy"`
	}{key, q.Reason, q.Policy}, &prepared); err != nil {
		return 1, err
	}
	plan := prepared.Plan
	hash, err := plan.Digest()
	if err != nil || hash != prepared.SHA256 || plan.Key != key || plan.Actor.Pair != scope.Pair || plan.Actor.GrantRevision != scope.Revision || plan.Target.Host != peer.Identity.Host || plan.Target.Certificate != peer.Identity.Fingerprint || plan.Target.SPKI != peer.Identity.SPKI || plan.Target.Owner != peer.Identity.Owner || plan.Reason != q.Reason || plan.Policy != q.Policy {
		return 1, model.Err("node_context_changed", "prepared Stop differs from exact target/enrollment/draft")
	}
	connection, err := a.stopConnection(peer)
	if err != nil {
		return 1, err
	}
	review := StopReview{Operation: "stop-arm-node", Key: key, Plan: plan, SHA256: hash, OwnerUsername: prepared.OwnerUsername, HistoryUsed: prepared.HistoryUsed, HistoryCapacity: prepared.HistoryCapacity, CompletionCheck: a.probeStopObserver(connection, plan.Target)}
	if err = review.Validate(); err != nil {
		return 1, err
	}
	if connection != nil && connection.RemoteHome != plan.Target.Home {
		return 1, model.Err("node_context_changed", "retained native connection home differs from target")
	}
	if !dry && a.guard != nil && (!plan.Impact.Complete || len(plan.Impact.Blockers) > 0 || plan.Impact.Outstanding > 0 && !plan.Policy.Wait && !plan.Policy.Force) {
		raw, e := json.Marshal(review)
		if e != nil {
			return 1, e
		}
		reviewHash, e := mesh.ReviewDigest(review)
		if e != nil {
			return 1, e
		}
		if e = a.guard.Change(func(op *mesh.Coordination) error {
			if op.Approved {
				return model.Err("review_changed", "review was already approved")
			}
			op.Review = raw
			op.ReviewHash = reviewHash
			op.ReviewRevision = op.Revision + 1
			op.ReviewExpires = plan.Expires
			return nil
		}); e != nil {
			return 1, e
		}
		return 1, model.Err("node_stop_blocked", fmt.Sprintf("Stop not applied: %d outstanding obligations and %d blockers. End or reconcile work, or separately review wait/force where supported.", plan.Impact.Outstanding, len(plan.Impact.Blockers)))
	}
	if err = a.Confirm(review, yes, dry); err != nil {
		return 1, err
	}
	if dry {
		return 0, nil
	}
	if a.guard == nil {
		return 1, model.Err("confirmation_required", "review --dry-run then explicitly confirm this exact Stop")
	}
	if err = a.markApproved(); err != nil {
		return 1, err
	} // Durable source fence before the sole apply.
	result := StopResult{Operation: "stop-arm-node", Key: key, Status: "unknown", Detail: "Original-key inspection is required; this request will never automatically be applied again."}
	if err = a.guard.Change(func(op *mesh.Coordination) error { raw, e := json.Marshal(result); op.Result = raw; return e }); err != nil {
		return 1, err
	}
	var record nodestop.Record
	applyErr := a.stopRPC(q.Host, scope, "node_stop_apply", key, nodestop.Apply{Key: key, Plan: plan, ReviewSHA256: hash}, &record)
	if applyErr == nil {
		if record.Validate() != nil || record.Digest != hash || record.Plan.Target != plan.Target || record.Plan.Actor != plan.Actor {
			return 1, model.Err("node_context_changed", "target returned a different stop receipt")
		}
		result.Record = &record
		result.Status = "accepted"
		result.Detail = "Destination owns the admitted stop. Completion still requires positive exact-generation inspection."
	}
	if connection != nil {
		var observation nodestop.Observation
		request := StopInspectRequest{Action: "node_stop_inspect", Home: connection.RemoteHome, Target: plan.Target, Key: key}
		_, observeErr := a.ssh(connection.SSHLocator, connection.Binary, request, meshssh.Identity{Account: connection.SSHAccount, HostKey: connection.SSHHostKey}, &observation)
		if observeErr == nil && validStopObservation(observation, plan, hash) {
			result.Status = "stopped"
			result.Record = nil
			result.Observation = &observation
			result.Detail = "Original backend generation positively joined and absent; stopped intent and history remain."
			if err = result.Validate(review); err != nil {
				return 1, err
			}
			return 0, a.Print(result)
		}
	}
	if err = result.Validate(review); err != nil {
		return 1, err
	}
	if err = a.Print(result); err != nil {
		return 1, err
	}
	if applyErr != nil {
		return 1, model.Err("node_stop_outcome_unknown", fmt.Sprintf("Stop outcome unknown; inspect original key %q. No apply retry: %v", key, applyErr))
	}
	return 1, model.Err("node_stop_completion_unknown", "Stop accepted; original generation has no positive completion observation yet. Check the original key only")
}

type StopInspectRequest struct {
	Action string          `json:"action"`
	Home   string          `json:"home"`
	Target nodestop.Target `json:"target"`
	Key    string          `json:"key"`
}

func validStopObservation(o nodestop.Observation, p nodestop.Plan, hash string) bool {
	review := nodestop.SourceReview{Operation: "stop-arm-node", Key: p.Key, Plan: p, SHA256: hash, HistoryCapacity: nodestop.MaxRecords}
	result := nodestop.Result{Operation: "stop-arm-node", Key: p.Key, Status: "stopped", Observation: &o}
	return result.Validate(review) == nil
}

// inspectOriginalStop performs reads only. It never prepares/applies, starts a
// backend, changes enrollment or substitutes a new key after an unknown reply.
func (a *Coordinator) inspectOriginalStop(review StopReview) (StopResult, error) {
	p := review.Plan
	result := StopResult{Operation: "stop-arm-node", Key: review.Key, Status: "unknown", Detail: "Original stop is still unconfirmed. No mutation or apply retry was performed."}
	hash, err := p.Digest()
	if err != nil || hash != review.SHA256 || p.Key != review.Key {
		return result, model.Err("invalid_request", "original complete Stop review required")
	}
	peer, own, err := a.stopPinnedPeer(p.Target.Host)
	if err != nil {
		return result, err
	}
	if peer.Identity.Fingerprint != p.Target.Certificate || peer.Identity.SPKI != p.Target.SPKI || peer.Identity.Owner != p.Target.Owner || peer.Pair != p.Actor.Pair || own.Identity.Host != p.Actor.Host || own.Identity.Fingerprint != p.Actor.Certificate || own.Identity.SPKI != p.Actor.SPKI {
		return result, model.Err("node_context_changed", "retained local peer/source identity or enrollment differs from original stop")
	}
	scope, readErr := a.stopLiveScope(peer, own)
	if readErr == nil {
		var record nodestop.Record
		readErr = a.stopRPC(p.Target.Host, scope, "node_stop_lookup", "", struct {
			Key string `json:"key"`
		}{p.Key}, &record)
		if readErr == nil {
			if record.Validate() != nil || record.Digest != hash || record.Plan.Target != p.Target || record.Plan.Actor != p.Actor {
				return result, model.Err("node_context_changed", "original stop lookup returned foreign receipt")
			}
			result.Record = &record
			result.Status = "accepted"
			result.Detail = "Original destination receipt is present; no positive native completion yet."
		}
	}
	// A stopped destination normally has no live mesh listener. Native exact
	// inspection uses the original locally pinned route independently of that RPC.
	connection, err := a.stopConnection(peer)
	if err != nil {
		return result, err
	}
	if connection != nil && connection.RemoteHome == p.Target.Home {
		var observation nodestop.Observation
		_, observeErr := a.ssh(connection.SSHLocator, connection.Binary, StopInspectRequest{Action: "node_stop_inspect", Home: connection.RemoteHome, Target: p.Target, Key: p.Key}, meshssh.Identity{Account: connection.SSHAccount, HostKey: connection.SSHHostKey}, &observation)
		if observeErr == nil && validStopObservation(observation, p, hash) {
			result.Status = "stopped"
			result.Record = nil
			result.Observation = &observation
			result.Detail = "Original backend generation positively joined and absent; history and stopped intent retained."
			return result, nil
		}
	}
	return result, readErr
}
