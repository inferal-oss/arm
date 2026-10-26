package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"sync"

	"github.com/yrashk/arm/internal/database"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/nodestop"
	"github.com/yrashk/arm/internal/storage"
)

type stopPlanRequest struct {
	Key    string                `json:"key"`
	Reason string                `json:"reason"`
	Policy model.ShutdownRequest `json:"policy"`
}
type stopLookupRequest struct {
	Key string `json:"key"`
}

// beginStopWork registers the ENTIRE possible native/workflow interval before
// any launch. Inspection remains available during drain, but is also joined.
// The mutex order is peer-grant → this gate → short registry snapshots →
// lifecycle journal exclusion → Manager.mu. No lock is held during joins.
func (s *Server) beginStopWork(kind string) (func(), error) {
	s.nodeStopMu.Lock()
	defer s.nodeStopMu.Unlock()
	inspection := kind == "inspection" || kind == "websocket"
	if s.nodeStopClosing || s.nodeStopClosed && !inspection {
		return nil, model.Err("backend_stopping", "durable stop admission prevents new native work")
	}
	if s.nodeStopCalls == nil {
		s.nodeStopCalls = map[string]int{}
	}
	s.nodeStopCalls[kind]++
	s.nodeStopCallsWG.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() { s.nodeStopMu.Lock(); s.nodeStopCalls[kind]--; s.nodeStopMu.Unlock(); s.nodeStopCallsWG.Done() })
	}, nil
}

func stopInspection(operation string) bool {
	switch operation {
	case "node_stop_observer_context", "startup_inspect", "lease_review_list", "lease_review_inspect", "lease_review_policy", "status", "resources", "inspect", "events", "output", "shutdown_status", "kube_contexts", "kube_nodes", "node_list", "node_inspect", "node_metadata", "agent_inbox", "message_lookup", "agent_message_lookup", "messages", "agents", "pool_list", "pool_inspect", "pool_history", "pool_children", "pool_consumers", "pool_lookup", "budget_list", "budget_inspect", "budget_consumers", "budget_history", "budget_lookup", "storage_list", "storage_inspect", "storage_exports", "storage_export_inspect", "monitor", "incidents", "usage", "access_scope":
		return true
	case "lease_review_reply", "release", "cancel", "cancel_request", "bulk_end", "agent_receipt":
		// Existing settlement can finish; Manager's own draining guard and
		// provider ownership rules remain authoritative. It is joined too.
		return true
	}
	return false
}

func (s *Server) registerStopOperation(operation string) (func(), error) {
	if stopInspection(operation) {
		return s.beginStopWork("inspection")
	}
	return s.beginStopWork("rpc")
}

func (s *Server) configureNodeStop() error {
	if s.mesh == nil {
		return nil
	}
	return s.installNodeStop(s.mesh.store.Config().Identity)
}

// installNodeStop binds the FIRST actually owned listener identity in this Serve
// generation, including dynamic enable after a no-mesh startup. It is called
// before Serve starts exposing that listener. Same identity is idempotent;
// changed identity cannot borrow this generation's old boundary.
func (s *Server) installNodeStop(identity mesh.Identity) error {
	s.nodeStopTargetMu.Lock()
	defer s.nodeStopTargetMu.Unlock()
	unavailable := func(err error) error { s.nodeStopUnavailable = err.Error(); return err }
	if s.nodeStopDirectory == nil || s.generation == "" {
		return unavailable(model.Err("node_stop_unavailable", "native lifetime directory/generation binding unavailable"))
	}
	id, err := storage.DirectoryIdentity(int(s.nodeStopDirectory.FD.Fd()))
	if err != nil {
		return unavailable(err)
	}
	target := nodestop.Target{Host: identity.Host, Certificate: identity.Fingerprint, SPKI: identity.SPKI, Owner: identity.Owner, Home: s.home, Directory: id, Generation: s.generation, Startup: nodestop.StartupContract}
	if s.nodeStopTarget == target {
		s.nodeStopUnavailable = ""
		return nil
	}
	if s.nodeStopTarget.Generation != "" {
		return unavailable(model.Err("node_generation_changed", "node-stop identity changed; a fresh backend generation is required"))
	}
	b, err := nodestop.NewBoundary(target)
	if err != nil {
		return unavailable(err)
	}
	if err = s.Manager.ConfigureNodeStop(b); err != nil {
		return unavailable(err)
	}
	s.nodeStopTarget = target
	s.nodeStopUnavailable = ""
	return nil
}

func (s *Server) nodeStopBinding() (nodestop.Target, error) {
	s.nodeStopTargetMu.RLock()
	defer s.nodeStopTargetMu.RUnlock()
	if s.nodeStopUnavailable != "" || s.nodeStopTarget.Generation == "" {
		return nodestop.Target{}, model.Err("node_stop_unavailable", "node-stop native lifetime binding is unavailable; ordinary backend remains available")
	}
	return s.nodeStopTarget, nil
}

// dispatchStop is invoked ONLY from Store.WithPeerSnapshot, with the peer grant
// and complete exact local/peer roster captured under the same original RLock.
// It never calls Store.Config or takes meshMu under that lock.
func (m *meshService) dispatchStop(ctx context.Context, peer mesh.Peer, request mesh.Request, local mesh.Identity, peers []mesh.PeerImpact) (any, error) {
	if peer.State != "active" || !peer.Proven || request.Expected == nil || request.Expected.Pair != peer.Pair || request.Expected.Revision != peer.Revision || request.Host != local.Host {
		return nil, model.Err("node_context_changed", "active proven exact direct enrollment required")
	}
	s := m.owner
	traceStopGrant(s.home, peer, local)
	actor := nodestop.Actor{Host: peer.Identity.Host, Certificate: peer.Identity.Fingerprint, SPKI: peer.Identity.SPKI, Pair: peer.Pair, GrantRevision: peer.Revision}
	if request.Operation == "node_stop_lookup" {
		if !peer.Grant.Has("node-stop") && !peer.Grant.Has("observe-own") && !peer.Grant.Has("observe-all") {
			return nil, model.Err("forbidden", "current own-receipt inspection permission is required")
		}
		var q stopLookupRequest
		if err := nodestop.Decode(request.Params, &q); err != nil {
			return nil, model.Err("invalid_request", err.Error())
		}
		return s.Manager.LookupNodeStop(q.Key, actor)
	}
	if !peer.Grant.Has("node-stop") {
		return nil, model.Err("forbidden", "peer lacks explicit current destination-local node-stop permission")
	}
	if request.Operation == "node_stop_apply" && !s.nodeStopAdminEnabled {
		return nil, model.Err("node_stop_not_enabled", "remote Stop is disabled for this incomplete/test backend lifetime")
	}
	target, bindingErr := s.nodeStopBinding()
	if bindingErr != nil {
		return nil, bindingErr
	}
	if target.Host != local.Host || target.Certificate != local.Fingerprint || target.SPKI != local.SPKI || target.Owner != local.Owner {
		return nil, model.Err("node_generation_changed", "lifetime identity changed; no stop admitted")
	}
	var q stopPlanRequest
	var apply nodestop.Apply
	switch request.Operation {
	case "node_stop_plan":
		if err := nodestop.Decode(request.Params, &q); err != nil {
			return nil, model.Err("invalid_request", err.Error())
		}
	case "node_stop_apply":
		if err := nodestop.Decode(request.Params, &apply); err != nil {
			return nil, model.Err("invalid_request", err.Error())
		}
		if request.Key != apply.Key {
			return nil, model.Err("invalid_request", "outer and reviewed original stop keys differ")
		}
	default:
		return nil, model.Err("invalid_request", "unknown node-stop method")
	}
	s.nodeStopMu.Lock()
	defer s.nodeStopMu.Unlock()
	if e := s.nodeStopDirectory.Validate(); e != nil {
		return nil, e
	}
	if s.nodeStopClosing {
		return nil, model.Err("backend_stopping", "server is closing")
	}
	// Known keys are lookup-only even after current admission/impact changed.
	if request.Operation == "node_stop_apply" {
		if previous, err := s.Manager.LookupNodeStop(apply.Key, actor); err == nil {
			hash, e := apply.Plan.Digest()
			if e != nil || hash != apply.ReviewSHA256 || hash != previous.Digest || apply.Plan.Key != apply.Key {
				return nil, model.Err("idempotency_conflict", "known stop key belongs to its original immutable request")
			}
			return previous, nil
		} else if api, ok := err.(*model.Error); !ok || api.Code != "not_found" {
			return nil, err
		}
	}
	if s.nodeStopClosed {
		return nil, model.Err("backend_stopping", "stop was already admitted; inspect original key")
	}
	impact := nodestop.Impact{Complete: true, Obligations: []nodestop.Obligation{}, Blockers: []nodestop.Blocker{}, Peers: []string{}}
	for _, peerImpact := range peers {
		if peerImpact.State != "revoked" {
			impact.Peers = append(impact.Peers, peerImpact.Host)
		}
	}
	sort.Strings(impact.Peers)
	history, e := s.Manager.NodeStopHistory()
	if e != nil {
		return nil, e
	}
	if s.nodeStopLock == nil {
		return nil, model.Err("node_stop_unavailable", "retained native authority lock missing")
	}
	lockID, e := storage.DirectoryIdentity(int(s.nodeStopLock.Fd()))
	if e != nil {
		return nil, e
	}
	for _, older := range history {
		if older.Phase == "drain-ready" {
			if e = readStopMarker(s.nodeStopDirectory, older, lockID); e != nil {
				impact.Blockers = append(impact.Blockers, nodestop.Blocker{Class: "unknown", ID: "join:" + older.Plan.Key, Detail: "An earlier drained generation lacks complete matched native closure evidence."})
			}
		}
	}

	if blocker := s.meshCloseBlocker(); blocker != nil {
		impact.Blockers = append(impact.Blockers, *blocker)
	}
	for kind, count := range s.nodeStopCalls {
		if count > 0 && kind != "inspection" && kind != "websocket" {
			impact.Blockers = append(impact.Blockers, nodestop.Blocker{Class: "unjoined", ID: kind, Detail: "An owned native/workflow interval is still active; finish or cancel it before stop review."})
		}
	}
	// Entries include handlers before authentication/upgrade and every source
	// worker/outgoing workspace transport. A released registry lock cannot create
	// stale-empty: all new registrations must first acquire nodeStopMu above.
	var result any
	err := database.WithLifecycleStopScope(ctx, s.home, target.Owner, target.Host, func(scope database.LifecycleStopScope) error {
		for _, block := range scope.Blockers {
			impact.Blockers = append(impact.Blockers, nodestop.Blocker{Class: "unknown", ID: block.Kind + ":" + block.Entity, Detail: block.Detail})
		}
		if len(impact.Blockers) > nodestop.MaxItems {
			return model.Err("node_stop_scope_incomplete", "complete component census exceeds bound")
		}
		sort.Slice(impact.Blockers, func(i, j int) bool {
			a, b := impact.Blockers[i], impact.Blockers[j]
			if a.Class != b.Class {
				return a.Class < b.Class
			}
			return a.ID < b.ID
		})
		encoded, _ := json.Marshal(struct {
			Scope    string
			Peers    []mesh.PeerImpact
			Blockers []nodestop.Blocker
		}{scope.SHA256, peers, impact.Blockers})
		sum := sha256.Sum256(encoded)
		impact.SHA256 = hex.EncodeToString(sum[:])
		if request.Operation == "node_stop_plan" {
			plan, e := s.Manager.PlanNodeStop(q.Key, actor, q.Policy, q.Reason, impact, nodestop.LeaseReadScope{Source: actor.Host, Own: peer.Grant.Has("observe-own"), All: peer.Grant.Has("observe-all")})
			if e != nil {
				return e
			}
			hash, e := plan.Digest()
			if e != nil {
				return e
			}
			status, e := s.Manager.Status()
			if e != nil {
				return e
			}
			result = nodestop.Prepared{Plan: plan, SHA256: hash, OwnerUsername: status.OwnerUsername, HistoryUsed: len(history), HistoryCapacity: nodestop.MaxRecords}
			return e
		}
		record, e := s.Manager.BeginNodeStop(apply, actor, true, impact, nodestop.LeaseReadScope{Source: actor.Host, Own: peer.Grant.Has("observe-own"), All: peer.Grant.Has("observe-all")})
		if s.Manager.ShutdownActive() {
			s.nodeStopClosed = true
		}
		result = record
		return e
	})
	if request.Operation == "node_stop_apply" && s.nodeStopClosed {
		// The peer/gate/manager locks are still held here. Only register the
		// lifetime worker; it performs no synchronous join under these locks.
		s.startShutdownWorker()
	}
	return result, err
}

func (s *Server) closeStopMesh() error {
	owner, err := s.beginMeshClose()
	if err != nil || owner == nil {
		return err
	}
	// Stop was already admitted after a complete census. Its final marker must
	// await this exact retained owner, outside every admission/peer/manager lock.
	<-owner.done
	if owner.result.err == nil && !owner.result.complete() {
		return model.Err("mesh_close_unknown", "original mesh closure lacks complete join facts")
	}
	return owner.result.err
}

func (s *Server) nodeStopDrainReady(report model.ShutdownReport) error {
	record, present, err := s.Manager.NodeStopDrainReady(report)
	if err != nil || !present {
		return err
	}
	target, err := s.nodeStopBinding()
	if err != nil {
		return err
	}
	if record.Plan.Target != target || record.Plan.Target.Generation != s.generation {
		return model.Err("node_generation_changed", "ready stop belongs to another lifetime")
	}
	c, err := nodestop.NewClosure(record)
	if err != nil {
		return err
	}
	s.nodeStopMu.Lock()
	s.nodeStopClosure = c
	s.nodeStopMu.Unlock()
	return nil
}

func (s *Server) decodeStopEnvelope(raw []byte, request *mesh.Request) error {
	if err := nodestop.Decode(raw, request); err != nil {
		return model.Err("invalid_request", err.Error())
	}
	return nil
}
