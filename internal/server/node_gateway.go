package server

import (
	"context"
	"encoding/json"
	"time"

	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

// A node session is a closed, read-only gateway. It cannot invoke native mesh
// setup, nested forwarding, local subscriptions, contacts or operator history.
func (s *Server) nodeSessionOperation(ctx context.Context, bound nodeSession, r model.WireRequest) (any, error) {
	service, e := s.validateNodeSession(bound)
	if e != nil {
		return nil, e
	}
	if bound.Local {
		return nil, model.Err("invalid_request", "local node sessions use ordinary local dispatch")
	}
	allowed := r.Operation == "node_context" || r.Operation == "node_metadata" || r.Operation == "status" || r.Operation == "resources" || r.Operation == "inspect"
	if !allowed {
		return nil, model.Err("forbidden", "this remote node window supports scoped inspection only; local streams, administration and mutations are unavailable")
	}
	callCtx, stop := context.WithTimeout(ctx, 4*time.Second)
	defer stop()
	scopeRaw, e := scopedNodeCall(callCtx, service, bound, "access_scope", json.RawMessage(`{}`))
	if e != nil {
		return nil, e
	}
	var scope model.NodeAccessScope
	if e = decodeNodeRead(scopeRaw, &scope); e != nil {
		return nil, e
	}
	if scope.Observed.IsZero() || scopeGrant(scope.Grant).Validate() != nil || scope.Source != bound.Source || scope.Target != bound.Host || scope.Pair != bound.Pair || scope.Revision != bound.RemoteRevision {
		return nil, model.Err("node_context_changed", "destination grants changed; reopen this node from Mesh")
	}
	if r.Operation == "node_context" {
		return map[string]any{"context": bound, "access_scope": scope, "read_only": true, "unavailable": []string{"live-monitor", "event-stream", "contacts", "messages", "resource-administration", "lease-mutations", "mesh-administration"}}, nil
	}
	params := r.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	// No target selector can be smuggled into a scoped request. Destination
	// independently enforces every operation's permission and own-lease scope.
	if r.Operation != "inspect" {
		if e = nodeMetadataParams(params); e != nil {
			return nil, e
		}
	} else {
		var q struct {
			ID string `json:"id"`
		}
		if e = mesh.Decode(params, &q); e != nil {
			return nil, e
		}
		if !exactNodeID(q.ID) {
			return nil, model.Err("invalid_request", "exact lease ID required")
		}
	}
	result, e := scopedNodeCall(callCtx, service, bound, r.Operation, params)
	if e != nil {
		return nil, e
	}
	if _, e = s.validateNodeSession(bound); e != nil {
		return nil, e
	}
	if r.Operation == "node_metadata" || r.Operation == "status" {
		var target struct {
			Host string `json:"host"`
		}
		if json.Unmarshal(result, &target) != nil || target.Host != bound.Host {
			return nil, model.Err("wrong_host", "peer response differs from bound destination")
		}
	}
	return result, nil
}

// Every scoped request carries its immutable destination admission condition.
// No lock is held across network I/O; the destination checks under WithPeer.
func scopedNodeCall(ctx context.Context, service *meshService, bound nodeSession, operation string, params json.RawMessage) (json.RawMessage, error) {
	return service.store.Call(ctx, bound.Host, mesh.Request{Expected: &mesh.ExpectedEnrollment{Pair: bound.Pair, Revision: bound.RemoteRevision}, Operation: operation, Params: params})
}
