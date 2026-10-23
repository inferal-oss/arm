package manager

import (
	"context"
	"database/sql"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/published"
	"github.com/yrashk/arm/internal/routing"
	"github.com/yrashk/arm/internal/workspace"
)

func (m *Manager) AgentPublicationPlan(ctx context.Context, q published.Request) (published.Plan, error) {
	return routingSettlement(ctx, m, func(ctx context.Context, tx *sql.Tx, a workspace.Authority) (published.Plan, error) {
		if _, e := published.Read(ctx, tx); e != nil {
			return published.Plan{}, e
		}
		return published.MakePlan(ctx, tx, a, q)
	})
}
func (m *Manager) AgentPublicationCreate(ctx context.Context, q published.Request, expected string) (published.Publication, error) {
	return routingSettlement(ctx, m, func(ctx context.Context, tx *sql.Tx, a workspace.Authority) (published.Publication, error) {
		p, e := published.Create(ctx, tx, a, q, expected, published.Actor{UID: m.owner, Username: m.ownerUsername}, time.Now().UTC())
		if e != nil {
			return p, e
		}
		_, e = routing.Read(ctx, tx)
		return p, e
	})
}
func (m *Manager) AgentPublicationWithdraw(ctx context.Context, id string, address published.Address, q published.Withdrawal) (published.Publication, error) {
	return routingSettlement(ctx, m, func(ctx context.Context, tx *sql.Tx, a workspace.Authority) (published.Publication, error) {
		return published.Withdraw(ctx, tx, a, id, address.ID, address.Generation, q, time.Now().UTC())
	})
}
func (m *Manager) AgentPublicationRead(ctx context.Context) (published.Records, error) {
	return routingSettlement(ctx, m, func(ctx context.Context, tx *sql.Tx, _ workspace.Authority) (published.Records, error) {
		return published.Read(ctx, tx)
	})
}
func (m *Manager) AgentEndpointPage(ctx context.Context, origin workspace.Identity, q published.Query) (published.Page, error) {
	return routeCall(ctx, m, q.Workspace, origin.Host, m.host, workspace.RouteMessage, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess) (published.Page, error) {
		if a.Origin != origin {
			return published.Page{}, model.Err("forbidden", "endpoint query source differs from pinned TLS member")
		}
		return published.PublicPage(ctx, tx, a, q)
	})
}

// This does not call a manager method from inside RouteTransaction. Contact
// checks and SQL inserts run only on the already serialized SQLite transaction.
func (m *Manager) agentPollBridge(ctx context.Context, tx *sql.Tx, in routing.Inbox, p published.Publication) error {
	e := in.Envelope
	c := published.Context{Workspace: e.Workspace, Generation: e.Generation, Message: e.ID, Digest: e.Digest(), Origin: e.Origin, Target: e.Target, From: published.Address(e.From), To: published.Address(e.To)}
	msg := Message{ID: local.ID(), Owner: m.owner, OwnerUsername: m.ownerUsername, Sender: "workspace:" + e.Origin + "/" + e.From.ID, Audience: p.Contact.ID, Body: e.Body, Created: in.Received, RoutedOrigin: &c, Deliveries: map[string]agents.Delivery{p.Contact.ID: {State: "available", Mode: "poll", Registration: p.Contact.Registration}}}
	if err := insertMessage(tx, msg, []agents.Contact{p.Contact}); err != nil {
		return err
	}
	return published.InsertLink(ctx, tx, published.Link{Schema: 1, Context: c, LocalMessage: msg.ID, Recipient: p.Contact.ID, Registration: p.Contact.Registration, ContactSHA: p.ContactSHA, Created: in.Received})
}
func (m *Manager) WorkspaceAgentPrepare(ctx context.Context, q routing.Intent, id, session, registration string) (routing.Outbox, bool, error) {
	type result struct {
		o       routing.Outbox
		created bool
	}
	v, e := routeCall(ctx, m, q.Workspace, m.host, q.Target, workspace.RouteMessage, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess) (result, error) {
		p, e := published.Source(ctx, tx, q.Workspace, id, session, registration)
		if e != nil {
			return result{}, e
		}
		if q.From != (routing.Endpoint{}) && q.From != routing.Endpoint(p.Endpoint) {
			return result{}, model.Err("forbidden", "sender differs from exact published local registration")
		}
		q.From = routing.Endpoint(p.Endpoint)
		if q.To.Kind != "agent" {
			return result{}, model.Err("invalid_request", "exact published destination endpoint is required")
		}
		o, created, e := routing.Prepare(ctx, tx, a, q)
		return result{o, created}, e
	})
	return v.o, v.created, e
}

// A reply is resolved only from the actual immutable local routed inbox link.
// Its source endpoint must still be the original current publication, never a
// namesake replacement. No remote selector supplied by the caller is trusted.
func (m *Manager) WorkspaceAgentReplyIntent(ctx context.Context, id, message, contact, session, registration, key, body string, ttl time.Duration) (routing.Intent, error) {
	return m.workspaceAgentReplyIntent(ctx, id, message, contact, session, registration, key, body, ttl, false)
}
func (m *Manager) WorkspaceAgentRetainedReplyIntent(ctx context.Context, id, message, contact, session, registration, key, body string, ttl time.Duration) (routing.Intent, error) {
	return m.workspaceAgentReplyIntent(ctx, id, message, contact, session, registration, key, body, ttl, true)
}
func (m *Manager) workspaceAgentReplyIntent(ctx context.Context, id, message, contact, session, registration, key, body string, ttl time.Duration, historical bool) (routing.Intent, error) {
	return routingSettlement(ctx, m, func(ctx context.Context, tx *sql.Tx, _ workspace.Authority) (routing.Intent, error) {
		v, e := published.Read(ctx, tx)
		if e != nil {
			return routing.Intent{}, e
		}
		var p published.Publication
		if !historical {
			p, e = published.Source(ctx, tx, id, contact, session, registration)
			if e != nil {
				return routing.Intent{}, e
			}
		}
		var link *published.Link
		for i := range v.Links {
			l := &v.Links[i]
			if l.LocalMessage == message && l.Context.Workspace == id && l.Recipient == contact && l.Registration == registration {
				link = l
				break
			}
		}
		if historical && link != nil {
			for _, old := range v.Publications {
				if old.Scope.Workspace == id && old.Endpoint == link.Context.To && old.Contact.ID == contact && old.Contact.Session == session && old.Contact.Registration == registration {
					p = old
				}
			}
		}
		if link == nil || link.Context.To != p.Endpoint {
			return routing.Intent{}, model.Err("forbidden", "reply requires the exact original published recipient")
		}
		r, e := routing.Read(ctx, tx)
		if e != nil {
			return routing.Intent{}, e
		}
		for _, in := range r.Inbox {
			if in.Workspace == id && in.Envelope.ID == link.Context.Message && in.Envelope.Digest() == link.Context.Digest {
				ref := in.Envelope.Reference()
				return routing.Intent{Workspace: id, Key: key, Target: ref.Origin, From: ref.To, To: ref.From, Body: body, TTL: ttl, ReplyTo: &ref}, nil
			}
		}
		return routing.Intent{}, model.Err("not_found", "original routed message is unavailable")
	})
}
