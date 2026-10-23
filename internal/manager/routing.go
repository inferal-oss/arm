package manager

import (
	"context"
	"database/sql"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/published"
	"github.com/yrashk/arm/internal/routing"
	"github.com/yrashk/arm/internal/workspace"
)

// routeCall is an INTERNAL adapter. Original source/final target are derived
// from native authority or pinned end-to-end TLS by its caller, never forwarded
// ordinary RPC fields. The callback must not network, signal or lock Manager.mu.
func routeCall[T any](ctx context.Context, m *Manager, id, origin, target, class string, fn func(context.Context, *sql.Tx, workspace.RouteAccess) (T, error)) (result T, err error) {
	return workspaceCall(ctx, m, true, func(ctx context.Context, w *workspaceBoundary, _ *workspace.Store) (T, error) {
		var out T
		_, e := w.runtime.RouteTransaction(ctx, id, origin, target, class, func(tx *sql.Tx, a workspace.RouteAccess) error {
			var e error
			out, e = fn(ctx, tx, a)
			return e
		})
		return out, e
	})
}
func (m *Manager) WorkspaceRouteAccess(ctx context.Context, id, origin, target, class string) (workspace.RouteAccess, error) {
	return routeCall(ctx, m, id, origin, target, class, func(_ context.Context, _ *sql.Tx, a workspace.RouteAccess) (workspace.RouteAccess, error) {
		return a, nil
	})
}
func (m *Manager) WorkspaceRoutingPrepare(ctx context.Context, q routing.Intent) (routing.Outbox, bool, error) {
	type prepared struct {
		out     routing.Outbox
		created bool
	}
	v, e := routeCall(ctx, m, q.Workspace, m.host, q.Target, workspace.RouteMessage, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess) (prepared, error) {
		// Host endpoint is fixed by current authenticated local membership, not a
		// user-supplied sender selector. Agent publication is a separate reviewed API.
		if q.From != (routing.Endpoint{}) && q.From != routing.HostEndpoint(a.OriginGeneration) || q.To != (routing.Endpoint{}) && q.To != routing.HostEndpoint(a.DestinationGeneration) {
			return prepared{}, model.Err("endpoint_unpublished", "native host send cannot impersonate or silently select an agent")
		}
		q.From, q.To = routing.HostEndpoint(a.OriginGeneration), routing.HostEndpoint(a.DestinationGeneration)
		out, created, e := routing.Prepare(ctx, tx, a, q)
		return prepared{out, created}, e
	})
	return v.out, v.created, e
}
func (m *Manager) WorkspaceRoutingBegin(ctx context.Context, o routing.Outbox) (routing.Outbox, error) {
	if o.Envelope.Origin != m.host {
		return routing.Outbox{}, model.Err("forbidden", "outbound origin differs from native authority")
	}
	return routeCall(ctx, m, o.Workspace, m.host, o.Envelope.Target, workspace.RouteMessage, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess) (routing.Outbox, error) {
		return routing.Begin(ctx, tx, a, o.Envelope.From, o.Intent.Key, o.Revision)
	})
}
func (m *Manager) WorkspaceRoutingUnknown(ctx context.Context, o routing.Outbox) (routing.Outbox, error) {
	if o.Envelope.Origin != m.host {
		return routing.Outbox{}, model.Err("forbidden", "outbound origin differs from native authority")
	}
	return routeCall(ctx, m, o.Workspace, m.host, o.Envelope.Target, workspace.RouteMessage, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess) (routing.Outbox, error) {
		return routing.Unknown(ctx, tx, a, o.Envelope.From, o.Intent.Key, o.Revision)
	})
}
func (m *Manager) WorkspaceRoutingComplete(ctx context.Context, o routing.Outbox, receipt routing.Receipt) (routing.Outbox, error) {
	if o.Envelope.Origin != m.host {
		return routing.Outbox{}, model.Err("forbidden", "outbound origin differs from native authority")
	}
	return routeCall(ctx, m, o.Workspace, m.host, o.Envelope.Target, workspace.RouteMessage, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess) (routing.Outbox, error) {
		return routing.Complete(ctx, tx, a, o.Envelope.From, o.Intent.Key, receipt)
	})
}

// Accept/Lookup are called ONLY by the dedicated routed inner-TLS handler. The
// source identity is not selected by an ordinary browser/mesh/native request.
func (m *Manager) WorkspaceRoutingAccept(ctx context.Context, origin workspace.Identity, q routing.Envelope) (routing.Inbox, error) {
	v, e := routeCall(ctx, m, q.Workspace, origin.Host, m.host, workspace.RouteMessage, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess) (routing.Inbox, error) {
		if a.Origin != origin {
			return routing.Inbox{}, model.Err("forbidden", "routed source differs from currently pinned TLS member")
		}
		in, _, e := routing.AcceptWithDelivery(ctx, tx, a, q, func(in routing.Inbox, p published.Publication) error { return m.agentPollBridge(ctx, tx, in, p) })
		return in, e
	})
	if e == nil {
		m.mu.Lock()
		m.notifyInbox()
		m.mu.Unlock()
	}
	return v, e
}
func (m *Manager) WorkspaceRoutingLookup(ctx context.Context, origin workspace.Identity, q routing.Envelope) (*routing.Receipt, error) {
	return routeCall(ctx, m, q.Workspace, origin.Host, m.host, workspace.RouteMessage, func(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess) (*routing.Receipt, error) {
		if a.Origin != origin {
			return nil, model.Err("forbidden", "routed source differs from currently pinned TLS member")
		}
		return routing.Lookup(ctx, tx, a, q)
	})
}

// This inspection does not create schema, restore current membership or grant
// delivery. Callers project bounded pages; Status never scans the full ledger.
func (m *Manager) WorkspaceRoutingRead(ctx context.Context) (routing.Records, error) {
	return workspaceCall(ctx, m, false, func(ctx context.Context, _ *workspaceBoundary, _ *workspace.Store) (routing.Records, error) {
		tx, e := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			return routing.Records{}, e
		}
		defer tx.Rollback()
		v, e := routing.Read(ctx, tx)
		if e != nil {
			return v, e
		}
		return v, tx.Commit()
	})
}

func routingSettlement[T any](ctx context.Context, m *Manager, fn func(context.Context, *sql.Tx, workspace.Authority) (T, error)) (T, error) {
	return workspaceCall(ctx, m, false, func(ctx context.Context, _ *workspaceBoundary, _ *workspace.Store) (T, error) {
		var out T
		tx, e := m.db.BeginTx(ctx, nil)
		if e != nil {
			return out, e
		}
		defer tx.Rollback()
		out, e = fn(ctx, tx, workspace.Authority{Host: m.host, UID: m.owner})
		if e != nil {
			return out, e
		}
		return out, tx.Commit()
	})
}
func (m *Manager) WorkspaceRoutingCancelPrepared(ctx context.Context, id, key string, revision int64) (routing.Outbox, error) {
	return routingSettlement(ctx, m, func(ctx context.Context, tx *sql.Tx, a workspace.Authority) (routing.Outbox, error) {
		return routing.CancelPrepared(ctx, tx, a, id, key, revision, time.Now().UTC())
	})
}
func (m *Manager) WorkspaceRoutingRetire(ctx context.Context, id, key string, revision int64) (routing.Root, error) {
	return routingSettlement(ctx, m, func(ctx context.Context, tx *sql.Tx, a workspace.Authority) (routing.Root, error) {
		return routing.Retire(ctx, tx, a, id, key, revision, time.Now().UTC())
	})
}
