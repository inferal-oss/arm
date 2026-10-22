package routing

import (
	"context"
	"database/sql"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
)

// settlement is identity-independent native OWNER settlement only. It neither
// manufactures current membership nor returns an authentication-capable Store.
// The caller supplies authenticated local authority, never a remote request.
func settlement(ctx context.Context, tx *sql.Tx, a workspace.Authority, id string) (Records, Root, error) {
	v, e := Read(ctx, tx)
	if e != nil {
		return v, Root{}, e
	}
	root, found := rootOf(v, id)
	if !found {
		return v, root, model.Err("not_found", "routing workspace not found")
	}
	if root.Authority != a {
		return v, root, model.Err("forbidden", "routing settlement belongs to another local authority")
	}
	return v, root, nil
}

// CancelPrepared can settle ONLY a durable intent for which dispatch was never
// admitted. Unknown/dispatching cannot become cancelled merely because time or
// process lifetime passed. No physical or remote reclamation is claimed.
func CancelPrepared(ctx context.Context, tx *sql.Tx, a workspace.Authority, id, key string, revision int64, at time.Time) (Outbox, error) {
	v, root, e := settlement(ctx, tx, a, id)
	if e != nil {
		return Outbox{}, e
	}
	out, found, e := FindOutbox(v, id, key)
	if e != nil {
		return out, e
	}
	if !found {
		return out, model.Err("not_found", "exact outbound key missing")
	}
	if out.Phase == "cancelled" {
		return out, nil
	}
	if root.State != "active" || out.Phase != "prepared" || out.Revision != revision || at.IsZero() || at.Before(root.Updated) {
		return out, model.Err("outcome_unresolved", "only a current never-dispatched intent may be cancelled; unknown delivery remains retained")
	}
	out.Phase = "cancelled"
	out.Revision++
	out.Updated = at
	if e = writeOutbox(ctx, tx, out); e != nil {
		return out, e
	}
	e = advance(ctx, tx, root, at, Change{Kind: "outbound_cancelled", Actor: "uid:" + a.UID, Key: key, Message: out.Envelope.ID, Digest: out.Envelope.Digest(), Phase: out.Phase})
	return out, e
}

// Retire records local retirement AFTER the parent workspace has been retired,
// and only after all actual outbound obligations are settled. Remote state is
// left intact. A repeated exact retirement key is read-only.
func Retire(ctx context.Context, tx *sql.Tx, a workspace.Authority, id, key string, revision int64, at time.Time) (Root, error) {
	v, root, e := settlement(ctx, tx, a, id)
	if e != nil {
		return root, e
	}
	if !validKey(key) {
		return root, model.Err("invalid_request", "bounded retirement key required")
	}
	if root.State == "retired" {
		for i := len(v.Events) - 1; i >= 0; i-- {
			c := v.Events[i]
			if c.Workspace == id {
				if c.Kind == "retired" && c.Key == key {
					return root, nil
				}
				break
			}
		}
		return root, model.Err("idempotency_conflict", "routing workspace retired with another operation key")
	}
	if root.Revision != revision || at.IsZero() || at.Before(root.Updated) {
		return root, model.Err("stale_revision", "routing authority or clock changed")
	}
	var raw []byte
	if e = tx.QueryRowContext(ctx, `SELECT data FROM workspace_instances WHERE id=?`, id).Scan(&raw); e != nil {
		return root, e
	}
	var parent workspace.Instance
	if e = workspace.Decode(raw, &parent); e != nil {
		return root, invalid("parent workspace retirement evidence is invalid")
	}
	if parent.State != "retired" || parent.Generation != root.Generation || parent.Authority != a {
		return root, model.Err("workspace_active", "explicit local workspace retirement is required first")
	}
	for _, out := range v.Outbox {
		if out.Workspace == id && !known(out.Phase, "completed", "cancelled") {
			return root, model.Err("outcome_unresolved", "dispatching/unknown outbound delivery remains unresolved; inspect original destination outcome")
		}
	}
	for _, in := range v.Inbox {
		if in.Workspace == id && in.Receipt.State == "delivery_pending" {
			return root, model.Err("outcome_unresolved", "accepted endpoint delivery remains pending")
		}
	}
	if g, found := graphIssuer(v, id); found {
		if at.Before(g.WallFloor) {
			return root, model.Err("routing_clock_unavailable", "graph issuance clock is ahead of retirement")
		}
		g.Closed = true
		g.Updated, g.WallFloor = at, at
		if e = writeGraphIssuer(ctx, tx, g); e != nil {
			return root, e
		}
	}
	e = advance(ctx, tx, root, at, Change{Kind: "retired", Actor: "uid:" + a.UID, Key: key, Phase: "retired"})
	if e != nil {
		return root, e
	}
	v, e = Read(ctx, tx)
	if e != nil {
		return root, e
	}
	root, _ = rootOf(v, id)
	return root, nil
}
