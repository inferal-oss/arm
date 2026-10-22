package server

import (
	"context"
	"encoding/json"
	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/model"
)

// ConnectDraft deliberately has no binary/home/SSH-options/public-listen input.
// A reviewed native connector may be selected by its immutable local ID.
type meshConnectDraft struct {
	Destination    string     `json:"ssh_destination,omitempty"`
	Connector      string     `json:"connector,omitempty"`
	Reason         string     `json:"reason"`
	Forward        mesh.Grant `json:"source_permissions_on_target"`
	Reverse        mesh.Grant `json:"target_permissions_on_source"`
	ReplaceRevoked bool       `json:"replace_revoked"`
}

func (s *Server) closeMeshOperator() {
	s.meshOperatorMu.Lock()
	service := s.meshOperator
	s.meshOperatorMu.Unlock()
	if service != nil {
		service.Close()
	}
}
func (s *Server) meshOperatorService() (*meshops.Service, error) {
	s.meshOperatorMu.Lock()
	defer s.meshOperatorMu.Unlock()
	s.admission.Lock()
	stopping := s.stopping
	s.admission.Unlock()
	if stopping {
		return nil, model.Err("backend_stopping", "operator coordinator is stopping")
	}
	s.lifecycleMu.Lock()
	closing := s.lifecycleClosing
	s.lifecycleMu.Unlock()
	if closing {
		return nil, model.Err("backend_stopping", "operator coordinator is stopping")
	}
	if s.meshOperator != nil {
		return s.meshOperator, nil
	}
	ctx := s.lifecycleContext
	if ctx == nil {
		ctx = context.Background()
	}
	service := &meshops.Service{Home: s.home, Context: ctx}
	service.New = func(ctx context.Context) *meshops.Coordinator {
		inspect := func() (mesh.LifecycleInspection, error) {
			q, e := mesh.InspectLifecycle(s.home, buildinfo.Version)
			if e != nil {
				return q, e
			}
			status, e := s.Manager.Status()
			if e != nil {
				return q, e
			}
			if status.Draining {
				return q, model.Err("backend_draining", "mesh setup cannot override a drain")
			}
			if q.Identity != nil && q.Identity.Host != status.Host {
				return q, model.Err("wrong_host", "mesh identity differs from this ledger")
			}
			q.Running = true
			q.Host = status.Host
			return q, nil
		}
		return &meshops.Coordinator{Home: s.home, Context: ctx, Inspect: inspect, Ensure: func(expected mesh.StartupPolicy) (mesh.LifecycleInspection, error) {
			if e := expected.Validate(); e != nil {
				return mesh.LifecycleInspection{}, e
			}
			return inspect()
		}, Call: func(ctx context.Context, op string, q, result any) error {
			if e := ctx.Err(); e != nil {
				return e
			}
			raw, e := json.Marshal(q)
			if e != nil {
				return e
			}
			value, e := s.meshOperation(ctx, op, raw)
			if e != nil || result == nil {
				return e
			}
			raw, e = json.Marshal(value)
			if e != nil {
				return e
			}
			return json.Unmarshal(raw, result)
		}, SSH: func(ctx context.Context, destination, binary string, q any, expected meshssh.Identity, result any) (meshssh.Identity, error) {
			return meshssh.Call(ctx, destination, binary, q, expected, result)
		}}
	}
	s.meshOperator = service
	return service, nil
}
func (s *Server) meshOperatorOperation(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	// Raw native setup remains excluded from browserAllowedOperation. Only this
	// narrow typed workflow, never an arbitrary mesh dispatch, is exposed there.
	if operation == "mesh_operator_connectors" {
		if len(raw) > 0 && string(raw) != "{}" && string(raw) != "null" {
			return nil, model.Err("invalid_request", "connector listing has no parameters")
		}
		all, e := mesh.ReadConnectors(s.home)
		if e != nil {
			return nil, e
		}
		out := []map[string]any{}
		for _, c := range all {
			out = append(out, map[string]any{"id": c.ID, "ssh_destination": c.Destination, "remote_binary": c.Binary, "remote_home": c.Home, "reviewed_at": c.At})
		}
		return out, nil
	}
	switch operation {
	case "mesh_operator_get":
		var q struct {
			Key string `json:"key"`
		}
		if e := mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		op, ok, e := mesh.ReadCoordination(s.home, q.Key)
		if e != nil {
			return nil, e
		}
		if !ok {
			return nil, model.Err("not_found", "operator key not found")
		}
		return meshops.Project(op), nil
	case "mesh_operator_list":
		if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
			return nil, model.Err("invalid_request", "list has no parameters")
		}
		all, e := mesh.ListCoordinations(s.home)
		if e != nil {
			return nil, e
		}
		out := make([]meshops.Operation, 0, len(all))
		for _, op := range all {
			out = append(out, meshops.ProjectSummary(op))
		}
		return out, nil
	}
	service, e := s.meshOperatorService()
	if e != nil {
		return nil, e
	}
	switch operation {
	case "mesh_operator_prepare":
		var q struct {
			Key        string                   `json:"key"`
			Kind       string                   `json:"kind"`
			Connect    *meshConnectDraft        `json:"connect,omitempty"`
			Disconnect *meshops.DisconnectDraft `json:"disconnect,omitempty"`
		}
		if e = mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		start := meshops.StartRequest{Key: q.Key, Kind: q.Kind, Disconnect: q.Disconnect}
		if q.Disconnect != nil {
			inspection, e := mesh.InspectLifecycle(s.home, buildinfo.Version)
			if e != nil {
				return nil, e
			}
			if inspection.Config == nil {
				return nil, model.Err("mesh_unavailable", "no peer configuration")
			}
			peer, e := mesh.ResolveConfig(*inspection.Config, q.Disconnect.Host)
			if e != nil {
				return nil, e
			}
			if peer.Identity.Host != q.Disconnect.Host {
				return nil, model.Err("wrong_host", "disconnect requires exact stable host")
			}
			if q.Disconnect.Pair != "" && q.Disconnect.Pair != peer.Pair {
				return nil, model.Err("setup_changed", "reviewed enrollment changed")
			}
			q.Disconnect.Pair = peer.Pair
		}
		if q.Connect != nil {
			r := q.Connect
			target := meshops.ConnectRequest{Destination: r.Destination, Binary: "arm", Reason: r.Reason, Forward: r.Forward, Reverse: r.Reverse, ReplaceRevoked: r.ReplaceRevoked}
			if r.Connector != "" {
				if r.Destination != "" {
					return nil, model.Err("invalid_request", "choose connector or destination, not both")
				}
				all, e := mesh.ReadConnectors(s.home)
				if e != nil {
					return nil, e
				}
				found := false
				for _, c := range all {
					if c.ID == r.Connector {
						target.Destination = c.Destination
						target.Binary = c.Binary
						target.RemoteHome = c.Home
						found = true
						break
					}
				}
				if !found {
					return nil, model.Err("not_found", "reviewed native connector not found")
				}
			}
			start.Connect = &target
		}
		return service.Start(start)
	case "mesh_operator_decide":
		var q meshops.Decision
		if e = mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		return service.Decide(q)
	case "mesh_operator_resume":
		var q struct {
			Key      string `json:"key"`
			Revision int64  `json:"revision"`
		}
		if e = mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		return service.Resume(q.Key, q.Revision)
	case "mesh_operator_handoff":
		var q struct {
			Key      string `json:"key"`
			Revision int64  `json:"revision"`
			Hash     string `json:"review_sha256"`
			Reason   string `json:"reason"`
		}
		if e = mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		op, e := mesh.HandoffCoordination(s.home, q.Key, q.Revision, q.Hash, q.Reason)
		if e != nil {
			return nil, e
		}
		return meshops.Project(op), nil
	case "mesh_operator_cancel":
		var q struct {
			Key      string `json:"key"`
			Revision int64  `json:"revision"`
		}
		if e = mesh.Decode(raw, &q); e != nil {
			return nil, e
		}
		return service.Cancel(q.Key, q.Revision)
	default:
		return nil, model.Err("unknown_operation", "unknown mesh operator workflow")
	}
}
