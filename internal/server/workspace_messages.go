package server

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/routing"
	"github.com/yrashk/arm/internal/workspace"
	api "github.com/yrashk/arm/internal/workspaceapi"
	route "github.com/yrashk/arm/internal/workspaceroute"
)

const maxRoutingNativeReply = 96 << 10

func routingBounded(v any) (any, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	if len(raw) > maxRoutingNativeReply {
		return nil, model.Err("response_too_large", "complete routed reply exceeds96KiB; select a bounded page; no record was truncated")
	}
	return v, nil
}
func (s *Server) workspaceMessageOperation(ctx context.Context, operation string, raw json.RawMessage) (any, error) {
	decode := func(v any) error {
		if len(raw) == 0 || len(raw) > 60<<10 {
			return model.Err("invalid_request", "bounded routed operation required")
		}
		if e := workspace.Decode(raw, v); e != nil {
			return model.Err("invalid_request", e.Error())
		}
		return nil
	}
	switch operation {
	case "workspace_message_send":
		var q api.MessageSend
		if e := decode(&q); e != nil {
			return nil, e
		}
		return s.workspaceSendMessage(ctx, routing.Intent{Workspace: q.Workspace, Key: q.Key, Target: q.Target, Body: q.Body, TTL: q.TTL})
	case "workspace_message_reply":
		var q api.MessageReply
		if e := decode(&q); e != nil {
			return nil, e
		}
		records, e := s.Manager.WorkspaceRoutingRead(ctx)
		if e != nil {
			return nil, e
		}
		for _, in := range records.Inbox {
			if in.Workspace == q.Workspace && in.Envelope.ID == q.Message {
				ref := in.Envelope.Reference()
				return s.workspaceSendMessage(ctx, routing.Intent{Workspace: q.Workspace, Key: q.Key, Target: in.Envelope.Origin, From: in.Envelope.To, To: in.Envelope.From, Body: q.Body, TTL: q.TTL, ReplyTo: &ref})
			}
		}
		return nil, model.Err("not_found", "exact original local inbox message not found")
	case "workspace_message_lookup":
		var q api.MessageLookup
		if e := decode(&q); e != nil {
			return nil, e
		}
		records, e := s.Manager.WorkspaceRoutingRead(ctx)
		if e != nil {
			return nil, e
		}
		out, found, e := routing.FindOutbox(records, q.Workspace, q.Key)
		if e != nil {
			return nil, e
		}
		if !found {
			return nil, model.Err("not_found", "exact original outbound key not found")
		}
		if !q.Reconcile || out.Phase == "prepared" || out.Phase == "cancelled" {
			return routingBounded(api.RoutingResult(out))
		}
		if e = s.workspaceMessageFresh(ctx, q.Workspace); e != nil {
			return nil, e
		}
		conn, o, _, e := s.workspaceRouteDial(ctx, q.Workspace, out.Envelope.Target, route.ClassLookup)
		if e != nil {
			return nil, e
		}
		defer conn.Close()
		request := routedWireRequest(o, route.OperationLookup)
		request.Envelope = &out.Envelope
		if e = route.WriteRequest(conn, o, request); e != nil {
			return nil, e
		}
		reply, e := route.ReadReply(conn, request)
		if e != nil {
			return nil, model.Err("routing_outcome_unknown", "receipt lookup interrupted; original key "+q.Key+" remains retained; no delivery was repeated")
		}
		if reply.Failure != nil {
			return nil, model.Err(reply.Failure.Code, reply.Failure.Message)
		}
		if !reply.Lookup.Found {
			return routingBounded(struct {
				Message api.MessageResult `json:"message"`
				Found   bool              `json:"found"`
				Detail  string            `json:"detail"`
			}{api.RoutingResult(out), false, "No receipt observed. This is not nonexecution evidence; original uncertainty remains retained."})
		}
		out, e = s.Manager.WorkspaceRoutingComplete(ctx, out, *reply.Lookup.Receipt)
		if e != nil {
			return nil, e
		}
		return routingBounded(api.RoutingResult(out))
	case "workspace_message_page":
		var q api.MessagePage
		if e := decode(&q); e != nil {
			return nil, e
		}
		if q.Limit < 1 || q.Limit > 20 || q.Offset < 0 || q.Side != "inbox" && q.Side != "outbox" {
			return nil, model.Err("invalid_request", "side inbox/outbox, offset>=0, limit1..20 required")
		}
		records, e := s.Manager.WorkspaceRoutingRead(ctx)
		if e != nil {
			return nil, e
		}
		result := api.MessagePageResult{Workspace: q.Workspace, Initialized: records.Initialized, Offset: q.Offset, Next: q.Offset}
		for _, root := range records.Roots {
			if root.Workspace == q.Workspace {
				copy := root
				result.Root = &copy
				result.Revision = root.Revision
			}
		}
		if q.Revision != 0 && q.Revision != result.Revision {
			return nil, model.Err("stale_revision", "routed inbox/outbox changed; restart bounded pagination")
		}
		sort.Slice(records.Outbox, func(i, j int) bool { return records.Outbox[i].Envelope.ID < records.Outbox[j].Envelope.ID })
		sort.Slice(records.Inbox, func(i, j int) bool { return records.Inbox[i].Envelope.ID < records.Inbox[j].Envelope.ID })
		entries := []any{}
		if q.Side == "outbox" {
			for _, o := range records.Outbox {
				if o.Workspace == q.Workspace {
					entries = append(entries, o)
				}
			}
		} else {
			for _, in := range records.Inbox {
				if in.Workspace == q.Workspace {
					entries = append(entries, in)
				}
			}
		}
		result.Total = len(entries)
		if q.Offset > len(entries) {
			return nil, model.Err("invalid_request", "offset exceeds exact retained message count")
		}
		for i := q.Offset; i < len(entries) && i < q.Offset+q.Limit; i++ {
			copy := result
			if q.Side == "outbox" {
				copy.Outbox = append(append([]routing.Outbox{}, result.Outbox...), entries[i].(routing.Outbox))
			} else {
				copy.Inbox = append(append([]routing.Inbox{}, result.Inbox...), entries[i].(routing.Inbox))
			}
			copy.Next = i + 1
			raw, _ := json.Marshal(copy)
			if len(raw) > maxRoutingNativeReply-4096 {
				break
			}
			result = copy
		}
		if result.Next == q.Offset && q.Offset < len(entries) {
			return nil, model.Err("response_too_large", "single exact message exceeds native page; no body was truncated")
		}
		return routingBounded(result)
	case "workspace_message_cancel":
		var q api.RevisionRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
		out, e := s.Manager.WorkspaceRoutingCancelPrepared(ctx, q.Workspace, q.Key, q.Revision)
		if e != nil {
			return nil, e
		}
		return routingBounded(api.RoutingResult(out))
	case "workspace_messages_retire":
		var q api.RevisionRequest
		if e := decode(&q); e != nil {
			return nil, e
		}
		root, e := s.Manager.WorkspaceRoutingRetire(ctx, q.Workspace, q.Key, q.Revision)
		if e != nil {
			return nil, e
		}
		return routingBounded(root)
	}
	return nil, model.Err("invalid_request", "unknown closed routed message operation")
}
func (s *Server) workspaceMessageFresh(ctx context.Context, id string) error {
	v, e := s.Manager.WorkspaceRead(ctx)
	if e != nil {
		return e
	}
	root, e := workspaceRoot(v, id)
	if e != nil {
		return e
	}
	if root.Role == "member" {
		_, e = s.workspaceRefresh(ctx, id)
	}
	return e
}
func (s *Server) workspaceSendMessage(ctx context.Context, q routing.Intent) (any, error) {
	// Known keys are lookup-only before ANY refresh, dial or potential delivery.
	// The complete native intent is compared using retained immutable endpoints;
	// current same-name or replacement endpoints never rewrite an old request.
	records, e := s.Manager.WorkspaceRoutingRead(ctx)
	if e != nil {
		return nil, e
	}
	old, found, e := routing.FindOutbox(records, q.Workspace, q.Key)
	if e != nil {
		return nil, e
	}
	if found {
		if q.From == (routing.Endpoint{}) {
			q.From = old.Intent.From
		}
		if q.To == (routing.Endpoint{}) {
			q.To = old.Intent.To
		}
		if !reflect.DeepEqual(q, old.Intent) {
			return nil, model.Err("idempotency_conflict", "outbound key belongs to another exact retained request")
		}
		return routingBounded(api.RoutingResult(old))
	}
	if e = s.workspaceMessageFresh(ctx, q.Workspace); e != nil {
		return nil, e
	}
	out, created, e := s.Manager.WorkspaceRoutingPrepare(ctx, q)
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
	result, err := s.workspaceDispatchMessage(ctx, out)
	if err != nil {
		record, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = s.Manager.WorkspaceRoutingUnknown(record, out)
		return nil, model.Err("routing_outcome_unknown", "routed send interrupted; inspect original key "+q.Key+" with message-lookup; no automatic delivery retry")
	}
	return routingBounded(api.RoutingResult(result))
}
func (s *Server) workspaceDispatchMessage(ctx context.Context, out routing.Outbox) (routing.Outbox, error) {
	conn, o, _, e := s.workspaceRouteDial(ctx, out.Workspace, out.Envelope.Target, route.ClassMessage)
	if e != nil {
		return out, e
	}
	defer conn.Close()
	q := routedWireRequest(o, route.OperationMessage)
	q.Envelope = &out.Envelope
	if e = route.WriteRequest(conn, o, q); e != nil {
		return out, e
	}
	reply, e := route.ReadReply(conn, q)
	if e != nil {
		return out, e
	}
	if reply.Failure != nil {
		return out, model.Err(reply.Failure.Code, reply.Failure.Message)
	}
	return s.Manager.WorkspaceRoutingComplete(ctx, out, *reply.Receipt)
}
