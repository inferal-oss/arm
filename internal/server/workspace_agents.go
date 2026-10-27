package server

import (
	"context"
	"encoding/json"
	"getarm.org/internal/model"
	"getarm.org/internal/published"
	"getarm.org/internal/routing"
	"getarm.org/internal/workspace"
	api "getarm.org/internal/workspaceapi"
	route "getarm.org/internal/workspaceroute"
	"reflect"
	"sort"
	"time"
)

func (s *Server) workspaceAgentOperation(ctx context.Context, op string, raw json.RawMessage) (any, error) {
	decode := func(v any) error {
		if len(raw) == 0 || len(raw) > 60<<10 {
			return model.Err("invalid_request", "bounded published agent operation required")
		}
		return workspace.Decode(raw, v)
	}
	switch op {
	case "workspace_agent_plan":
		var q published.Request
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.Manager.AgentPublicationPlan(ctx, q)
	case "workspace_agent_publish":
		var q api.AgentPublicationCommit
		if e := decode(&q); e != nil {
			return nil, e
		}
		p, e := s.Manager.AgentPublicationCreate(ctx, q.Request, q.Expect)
		if e != nil {
			return nil, e
		}
		return routingBounded(p)
	case "workspace_agent_withdraw":
		var q api.AgentPublicationWithdraw
		if e := decode(&q); e != nil {
			return nil, e
		}
		p, e := s.Manager.AgentPublicationWithdraw(ctx, q.Workspace, q.Endpoint, q.Request)
		if e != nil {
			return nil, e
		}
		return routingBounded(p)
	case "workspace_agent_publications":
		var q api.AgentPublicationPage
		if e := decode(&q); e != nil {
			return nil, e
		}
		if !published.Hex(q.Workspace, 16) || q.Offset < 0 || q.Limit < 1 || q.Limit > 8 {
			return nil, model.Err("invalid_request", "workspace, nonnegative offset and limit 1–8 required")
		}
		v, e := s.Manager.AgentPublicationRead(ctx)
		if e != nil {
			return nil, e
		}
		items := []published.Publication{}
		for _, p := range v.Publications {
			if p.Scope.Workspace == q.Workspace {
				items = append(items, p)
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Endpoint.ID < items[j].Endpoint.ID })
		if q.Offset > len(items) {
			return nil, model.Err("invalid_request", "publication offset exceeds retained count")
		}
		next := min(q.Offset+q.Limit, len(items))
		return routingBounded(api.AgentPublicationPageResult{Workspace: q.Workspace, Publications: items[q.Offset:next], Offset: q.Offset, Next: next, Total: len(items)})
	case "workspace_agent_endpoints":
		var q api.AgentEndpoints
		if e := decode(&q); e != nil {
			return nil, e
		}
		if e := q.Query.Validate(); e != nil {
			return nil, e
		}
		if e := s.workspaceMessageFresh(ctx, q.Query.Workspace); e != nil {
			return nil, e
		}
		conn, o, _, e := s.workspaceRouteDial(ctx, q.Query.Workspace, q.Host, route.ClassLookup)
		if e != nil {
			return nil, e
		}
		defer conn.Close()
		request := routedWireRequest(o, route.OperationEndpoints)
		request.Endpoints = &q.Query
		if e = route.WriteRequest(conn, o, request); e != nil {
			return nil, e
		}
		reply, e := route.ReadReply(conn, request)
		if e != nil {
			return nil, e
		}
		if reply.Failure != nil {
			return nil, model.Err(reply.Failure.Code, reply.Failure.Message)
		}
		if !time.Now().Before(reply.Endpoints.Expires) {
			return nil, model.Err("membership_stale", "endpoint page exceeds current route authority")
		}
		return routingBounded(reply.Endpoints)
	case "workspace_agent_send", "workspace_agent_reply", "workspace_agent_lookup":
		var q api.AgentMessage
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.workspaceAgentMessage(ctx, op, q)
	}
	return nil, model.Err("invalid_request", "unknown published agent operation")
}
func (s *Server) workspaceAgentMessage(ctx context.Context, op string, q api.AgentMessage) (any, error) {
	if !published.Hex(q.Workspace, 16) || !published.Text(q.Key, 128) || !published.Text(q.ID, 128) || !published.Hex(q.Registration, 16) || q.Session == "" {
		return nil, model.Err("invalid_request", "exact workspace, key and sender selectors required")
	}
	records, e := s.Manager.WorkspaceRoutingRead(ctx)
	if e != nil {
		return nil, e
	}
	old, found, e := routing.FindOutbox(records, q.Workspace, q.Key)
	if e != nil {
		return nil, e
	}
	if found {
		if records.Published == nil {
			return nil, model.Err("publication_invalid", "retained agent operation lost publication group")
		}
		p, ok := published.Find(*records.Published, q.Workspace, published.Address(old.Envelope.From))
		if !ok || p.Contact.ID != q.ID || p.Contact.Session != q.Session || p.Contact.Registration != q.Registration {
			return nil, model.Err("forbidden", "retained operation belongs to another exact sender")
		}
		if op != "workspace_agent_lookup" {
			intent := routing.Intent{Workspace: q.Workspace, Key: q.Key, Target: q.Target, From: old.Intent.From, To: q.To, Body: q.Body, TTL: q.TTL}
			if op == "workspace_agent_reply" {
				intent, e = s.Manager.WorkspaceAgentRetainedReplyIntent(ctx, q.Workspace, q.Message, q.ID, q.Session, q.Registration, q.Key, q.Body, q.TTL)
				if e != nil {
					return nil, e
				}
			}
			if !reflect.DeepEqual(intent, old.Intent) {
				return nil, model.Err("idempotency_conflict", "agent operation key belongs to another immutable request")
			}
		}
		if op == "workspace_agent_lookup" && q.Reconcile {
			raw, _ := json.Marshal(api.MessageLookup{Workspace: q.Workspace, Key: q.Key, Reconcile: true})
			return s.workspaceMessageOperation(ctx, "workspace_message_lookup", raw)
		}
		return routingBounded(api.RoutingResult(old))
	}
	if op == "workspace_agent_lookup" {
		return nil, model.Err("not_found", "original agent outbound key not found")
	}
	intent := routing.Intent{Workspace: q.Workspace, Key: q.Key, Target: q.Target, To: q.To, Body: q.Body, TTL: q.TTL}
	if op == "workspace_agent_reply" {
		intent, e = s.Manager.WorkspaceAgentReplyIntent(ctx, q.Workspace, q.Message, q.ID, q.Session, q.Registration, q.Key, q.Body, q.TTL)
		if e != nil {
			return nil, e
		}
	}
	if e = s.workspaceMessageFresh(ctx, q.Workspace); e != nil {
		return nil, e
	}
	out, created, e := s.Manager.WorkspaceAgentPrepare(ctx, intent, q.ID, q.Session, q.Registration)
	if e != nil {
		return nil, e
	}
	if !created {
		return routingBounded(api.RoutingResult(out))
	}
	out, e = s.Manager.WorkspaceRoutingBegin(ctx, out)
	if e != nil {
		return nil, e
	}
	result, e := s.workspaceDispatchMessage(ctx, out)
	if e != nil {
		record, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = s.Manager.WorkspaceRoutingUnknown(record, out)
		return nil, model.Err("routing_outcome_unknown", "agent send interrupted; retain exact key "+q.Key+" and inspect without resending")
	}
	return routingBounded(api.RoutingResult(result))
}
