package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
)

const nativeNodeHandshakeTimeout = 10 * time.Second

// negotiateNativeNode runs before any ordinary request writer or subscription
// exists. A failed negotiation closes this dedicated socket, never dispatching
// the first frame with local privileges. There is no later rebind operation.
func (s *Server) negotiateNativeNode(ctx context.Context, c *websocket.Conn) (nodeSession, error) {
	ctx, cancel := context.WithTimeout(ctx, nativeNodeHandshakeTimeout)
	defer cancel()
	var req model.WireRequest
	if err := wsjson.Read(ctx, c, &req); err != nil {
		return nodeSession{}, err
	}
	response := model.WireResponse{Version: 1, ID: req.ID}
	bound, result, err := s.nativeNodeFirstFrame(ctx, req)
	if err != nil {
		var api *model.Error
		if errors.As(err, &api) {
			response.Error = api
		} else {
			response.Error = &model.Error{Code: "node_unavailable", Message: "selected node could not be bound; no local fallback was used"}
		}
	} else {
		response.Result = result
	}
	if writeErr := wsjson.Write(ctx, c, response); writeErr != nil {
		return nodeSession{}, writeErr
	}
	return bound, err
}

func (s *Server) nativeNodeFirstFrame(ctx context.Context, req model.WireRequest) (nodeSession, any, error) {
	if req.Version != 1 || req.ID == "" || req.Operation != "node_context_open" {
		return nodeSession{}, nil, model.Err("protocol_error", "scoped native connection requires node_context_open as its first frame")
	}
	var q model.NodeSelection
	if err := mesh.Decode(req.Params, &q); err != nil {
		return nodeSession{}, nil, err
	}
	if !exactNodeID(q.Host) || !exactNodeID(q.Source) {
		return nodeSession{}, nil, model.Err("invalid_request", "exact source and destination host IDs are required")
	}
	bound, err := s.bindNodeContext(ctx, q.Host)
	if err != nil {
		return nodeSession{}, nil, err
	}
	if bound.Source != q.Source {
		return nodeSession{}, nil, model.Err("node_context_changed", "local source authority changed; inspect Mesh and select the node again")
	}
	if !bound.Local {
		result, err := s.nodeSessionOperation(ctx, bound, model.WireRequest{Operation: "node_context", Params: json.RawMessage(`{}`)})
		return bound, result, err
	}
	return bound, model.NodeContextReport{Context: model.NodeContext{Host: bound.Host, Source: bound.Source, Local: true}, Unavailable: []string{}}, nil
}
