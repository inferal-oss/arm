package routing

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/workspace"
)

func authority(a workspace.RouteAccess) workspace.Authority {
	return workspace.Authority{Host: a.Local.Host, UID: strconv.Itoa(os.Geteuid())}
}
func requireScope(a workspace.RouteAccess) error {
	if !hexID(a.Workspace, 16) || !hexID(a.Generation, 16) || a.Local.Host == "" || a.At.IsZero() || a.Remaining <= 0 || !a.Expires.After(a.At) {
		return model.Err("forbidden", "current in-process workspace route authority required")
	}
	return nil
}
func mutation(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, init bool) (Records, Root, error) {
	if e := requireScope(a); e != nil {
		return Records{}, Root{}, e
	}
	if _, e := schema(ctx, tx, init); e != nil {
		return Records{}, Root{}, e
	}
	records, e := Read(ctx, tx)
	if e != nil {
		return records, Root{}, e
	}
	var host string
	if e = tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='host'`).Scan(&host); e != nil {
		return records, Root{}, e
	}
	if host != a.Local.Host {
		return records, Root{}, invalid("routing local host differs from central authority")
	}
	root, found := rootOf(records, a.Workspace)
	if !found {
		if !init {
			return records, root, model.Err("not_found", "routing workspace is not initialized")
		}
		root = Root{Schema: Schema, Workspace: a.Workspace, Generation: a.Generation, Authority: authority(a), LocalPin: a.Local.Fingerprint, State: "active", Revision: 1, Created: a.At, Updated: a.At}
		if e = put(ctx, tx, `INSERT INTO routing_roots(workspace,data) VALUES(?,?)`, root, root.Workspace); e != nil {
			return records, root, e
		}
		if e = appendChange(ctx, tx, Change{Schema: Schema, Workspace: root.Workspace, Kind: "initialized", Actor: "uid:" + root.Authority.UID, Phase: "active", After: root}); e != nil {
			return records, root, e
		}
		records, e = Read(ctx, tx)
		if e != nil {
			return records, root, e
		}
	}
	if root.State != "active" || root.Generation != a.Generation || root.Authority != authority(a) || root.LocalPin != a.Local.Fingerprint {
		return records, root, model.Err("routing_unavailable", "routing authority/pin/generation is not currently active")
	}
	if a.At.Before(root.Updated) {
		return records, root, model.Err("routing_clock_unavailable", "routing journal clock moved backwards; no authority window was extended")
	}
	return records, root, nil
}
func appendChange(ctx context.Context, tx *sql.Tx, c Change) error {
	return put(ctx, tx, `INSERT INTO routing_events(workspace,data) VALUES(?,?)`, c, c.Workspace)
}
func advance(ctx context.Context, tx *sql.Tx, root Root, at time.Time, c Change) error {
	if root.Revision == math.MaxInt64 {
		return model.Err("routing_capacity_exhausted", "routing revision exhausted")
	}
	before := root
	root.Revision++
	root.Updated = at
	if c.Kind == "retired" {
		root.State = "retired"
	}
	c.Schema = Schema
	c.Workspace = root.Workspace
	c.Before = &before
	c.After = root
	raw, e := json.Marshal(root)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE routing_roots SET data=? WHERE workspace=?`, string(raw), root.Workspace); e != nil {
		return e
	}
	if e = appendChange(ctx, tx, c); e != nil {
		return e
	}
	_, e = Read(ctx, tx)
	return e
}
func writeOutbox(ctx context.Context, tx *sql.Tx, o Outbox) error {
	request, _ := json.Marshal(o.Intent)
	return put(ctx, tx, `INSERT INTO routing_outbox(workspace,endpoint,key,message,request,data) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace,endpoint,key) DO UPDATE SET data=excluded.data`, o, o.Workspace, hash(o.Envelope.From), o.Intent.Key, o.Envelope.ID, string(request))
}
func writeInbox(ctx context.Context, tx *sql.Tx, in Inbox) error {
	return put(ctx, tx, `INSERT INTO routing_inbox(workspace,origin,endpoint,key,message,data) VALUES(?,?,?,?,?,?) ON CONFLICT(workspace,origin,endpoint,key) DO UPDATE SET data=excluded.data`, in, in.Workspace, in.Envelope.Origin, hash(in.Envelope.From), in.Envelope.Key, in.Envelope.ID)
}
func collision(v Records, id, message string) bool {
	for _, o := range v.Outbox {
		if o.Workspace == id && o.Envelope.ID == message {
			return true
		}
	}
	for _, o := range v.Inbox {
		if o.Workspace == id && o.Envelope.ID == message {
			return true
		}
	}
	return false
}

// Prepare persists the immutable origin envelope BEFORE any transport. Known
// exact intents return created=false even after restart; callers must not send
// them automatically. A relay can never prepare another node's outbox.
func Prepare(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, q Intent) (Outbox, bool, error) {
	if a.Local != a.Origin || q.Workspace != a.Workspace || q.Target != a.Destination.Host || !q.From.Validate() || !q.To.Validate() || q.Key == "" || !validKey(q.Key) || q.TTL <= 0 || q.TTL > MaxLifetime {
		return Outbox{}, false, model.Err("invalid_request", "exact current local origin and bounded intent required")
	}
	records, root, e := mutation(ctx, tx, a, true)
	if e != nil {
		return Outbox{}, false, e
	}
	if old, found := outboxOf(records, q.Workspace, q.From, q.Key); found {
		if old.IntentHash != hash(q) {
			return Outbox{}, false, model.Err("idempotency_conflict", "outbound key belongs to another immutable request")
		}
		return old, false, nil
	}
	if q.From != HostEndpoint(a.OriginGeneration) || q.To != HostEndpoint(a.DestinationGeneration) {
		return Outbox{}, false, model.Err("endpoint_unpublished", "agent endpoints require explicit current publication; no contact was selected")
	}
	if q.ReplyTo != nil {
		found := false
		for _, in := range records.Inbox {
			if in.Workspace == q.Workspace && in.Envelope.Reference() == *q.ReplyTo && in.Envelope.Target == a.Local.Host && in.Envelope.To == q.From && known(in.Receipt.State, "accepted", "delivered", "acknowledged") {
				found = true
				break
			}
		}
		if !found {
			return Outbox{}, false, model.Err("forbidden", "reply must originate at an actual original recipient of the committed exact inbox message")
		}
	}
	envelope := Envelope{OriginPin: a.Origin.Fingerprint, TargetPin: a.Destination.Fingerprint, Schema: Schema, Workspace: q.Workspace, Generation: a.Generation, ID: local.ID(), Key: q.Key, Origin: a.Origin.Host, From: q.From, Target: a.Destination.Host, To: q.To, OriginMembership: a.OriginGeneration, TargetMembership: a.DestinationGeneration, Body: q.Body, Created: a.At, Expires: a.At.Add(q.TTL), ReplyTo: q.ReplyTo}
	if !envelope.Validate() {
		return Outbox{}, false, model.Err("invalid_request", "invalid bounded addressed message envelope")
	}
	if collision(records, q.Workspace, envelope.ID) {
		return Outbox{}, false, model.Err("message_id_conflict", "generated message identity collides with retained history")
	}
	out := Outbox{Schema: Schema, Workspace: q.Workspace, Authority: root.Authority, Intent: q, IntentHash: hash(q), Envelope: envelope, Phase: "prepared", Revision: 1, Created: a.At, Updated: a.At}
	if e = writeOutbox(ctx, tx, out); e != nil {
		return Outbox{}, false, e
	}
	e = advance(ctx, tx, root, a.At, Change{Kind: "outbound_prepared", Actor: "uid:" + root.Authority.UID, Key: q.Key, Message: envelope.ID, Digest: envelope.Digest(), Phase: out.Phase})
	return out, e == nil, e
}
func Begin(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, from Endpoint, key string, revision int64) (Outbox, error) {
	records, root, e := mutation(ctx, tx, a, false)
	if e != nil {
		return Outbox{}, e
	}
	out, found := outboxOf(records, a.Workspace, from, key)
	if !found {
		return out, model.Err("not_found", "original outbound key not found")
	}
	if out.Phase != "prepared" || out.Revision != revision {
		return out, model.Err("outcome_unresolved", "outbound intent was already dispatched or changed; inspect original outcome without replay")
	}
	if out.Envelope.Origin != a.Local.Host || a.Local != a.Origin || out.Envelope.Target != a.Destination.Host || out.Envelope.OriginPin != a.Origin.Fingerprint || out.Envelope.TargetPin != a.Destination.Fingerprint || out.Envelope.OriginMembership != a.OriginGeneration || out.Envelope.TargetMembership != a.DestinationGeneration || !a.At.Before(out.Envelope.Expires) {
		return out, model.Err("forbidden", "current exact origin/destination membership or expiry differs")
	}
	out.Phase = "dispatching"
	out.Revision++
	out.Updated = a.At
	if e = writeOutbox(ctx, tx, out); e != nil {
		return out, e
	}
	e = advance(ctx, tx, root, a.At, Change{Kind: "outbound_dispatching", Actor: "uid:" + root.Authority.UID, Key: key, Message: out.Envelope.ID, Digest: out.Envelope.Digest(), Phase: out.Phase})
	return out, e
}
func Unknown(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, from Endpoint, key string, revision int64) (Outbox, error) {
	records, root, e := mutation(ctx, tx, a, false)
	if e != nil {
		return Outbox{}, e
	}
	out, found := outboxOf(records, a.Workspace, from, key)
	if !found {
		return out, model.Err("not_found", "outbound key missing")
	}
	if out.Phase != "dispatching" || out.Revision != revision {
		return out, model.Err("stale_revision", "outbound result changed")
	}
	if a.Local.Host != out.Envelope.Origin {
		return out, model.Err("forbidden", "outbound authority differs")
	}
	out.Phase = "unknown"
	out.Revision++
	out.Updated = a.At
	if e = writeOutbox(ctx, tx, out); e != nil {
		return out, e
	}
	e = advance(ctx, tx, root, a.At, Change{Kind: "outbound_unknown", Actor: "uid:" + root.Authority.UID, Key: key, Message: out.Envelope.ID, Digest: out.Envelope.Digest(), Phase: out.Phase})
	return out, e
}

// Accept is called only after final TLS authenticated a.Origin and the current
// route transaction validated both admitted endpoints. Message, exact target
// inbox and dedup outcome are one destination commit. There is no native call.
func Accept(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, q Envelope) (Inbox, bool, error) {
	if q.From.Kind != "host" || q.To.Kind != "host" {
		return Inbox{}, false, model.Err("endpoint_unpublished", "this host-message schema does not publish or deliver agent endpoints")
	}
	if a.Local != a.Destination || !q.Validate() || q.Workspace != a.Workspace || q.Generation != a.Generation || q.Origin != a.Origin.Host || q.Target != a.Local.Host || q.OriginPin != a.Origin.Fingerprint || q.TargetPin != a.Destination.Fingerprint || q.OriginMembership != a.OriginGeneration || q.TargetMembership != a.DestinationGeneration {
		return Inbox{}, false, model.Err("forbidden", "message origin/target differ from authenticated final workspace scope")
	}
	records, root, e := mutation(ctx, tx, a, true)
	if e != nil {
		return Inbox{}, false, e
	}
	if old, found := inboxOf(records, q.Workspace, q.Origin, q.From, q.Key); found {
		if old.Envelope.Digest() != q.Digest() {
			return Inbox{}, false, model.Err("idempotency_conflict", "source key names a different immutable message")
		}
		return old, false, nil
	}
	if collision(records, q.Workspace, q.ID) {
		return Inbox{}, false, model.Err("message_id_conflict", "message identity belongs to another retained envelope")
	}
	state, code := "accepted", ""
	switch {
	case !a.At.Before(q.Expires):
		state, code = "expired", "message_expired"
	case q.Created.After(a.At.Add(workspace.MaxClockSkew)):
		return Inbox{}, false, model.Err("workspace_clock_unavailable", "message source creation time is in the future")
	case !a.DestinationInbox:
		state, code = "rejected", "host_inbox_disabled"
	}
	if q.ReplyTo != nil && state == "accepted" {
		found := false
		for _, old := range records.Outbox {
			if old.Workspace == q.Workspace && old.Envelope.Reference() == *q.ReplyTo && old.Envelope.Origin == a.Local.Host && old.Envelope.From == q.To && old.Envelope.Target == q.Origin && old.Envelope.To == q.From {
				found = true
				break
			}
		}
		if !found {
			state, code = "rejected", "original_recipient_proof_missing"
		}
	}
	in := Inbox{Schema: Schema, Workspace: q.Workspace, Envelope: q, Receipt: receipt(q, a.At, state, code), Received: a.At}
	if e = writeInbox(ctx, tx, in); e != nil {
		return in, false, e
	}
	kind := "inbound_accepted"
	if !known(state, "accepted", "delivery_pending") {
		kind = "inbound_rejected"
	}
	e = advance(ctx, tx, root, a.At, Change{Kind: kind, Actor: "host:" + q.Origin, Key: q.Key, Message: q.ID, Digest: q.Digest(), Phase: state, ReceiptHash: hash(in.Receipt)})
	return in, e == nil, e
}

// Complete imports only an exact final-destination TLS receipt. The caller must
// authenticate that destination; JSON claiming receipt authority is not proof.
func Complete(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, from Endpoint, key string, result Receipt) (Outbox, error) {
	records, root, e := mutation(ctx, tx, a, false)
	if e != nil {
		return Outbox{}, e
	}
	out, found := outboxOf(records, a.Workspace, from, key)
	if !found {
		return out, model.Err("not_found", "outbound key missing")
	}
	if a.Local != a.Origin || out.Envelope.Origin != a.Local.Host || out.Envelope.Target != a.Destination.Host || out.Envelope.OriginPin != a.Origin.Fingerprint || out.Envelope.TargetPin != a.Destination.Fingerprint || !result.Matches(out.Envelope) {
		return out, model.Err("forbidden", "final receipt differs from exact original envelope/destination")
	}
	if out.Receipt != nil {
		if *out.Receipt == result {
			return out, nil
		}
		if result.Revision <= out.Receipt.Revision {
			return out, model.Err("stale_revision", "destination receipt moved backwards or changed immutable revision")
		}
	}
	if !known(out.Phase, "dispatching", "unknown", "completed") {
		return out, model.Err("outcome_unresolved", "undispatched outbound cannot accept another request's receipt")
	}
	kind := "outbound_completed"
	if out.Phase == "completed" {
		kind = "outbound_receipt"
	}
	out.Phase = "completed"
	out.Receipt = &result
	out.Revision++
	out.Updated = a.At
	if e = writeOutbox(ctx, tx, out); e != nil {
		return out, e
	}
	e = advance(ctx, tx, root, a.At, Change{Kind: kind, Actor: "uid:" + root.Authority.UID, Key: key, Message: out.Envelope.ID, Digest: out.Envelope.Digest(), Phase: out.Phase, ReceiptHash: hash(result)})
	return out, e
}

// Lookup accepts the original full envelope and authenticates its original
// source even for an absent result. An empty lookup is NEVER nonexecution proof.
func Lookup(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, q Envelope) (*Receipt, error) {
	if q.From.Kind != "host" || q.To.Kind != "host" {
		return nil, model.Err("endpoint_unpublished", "agent endpoint publication is unavailable in this schema")
	}
	if !q.Validate() || q.Workspace != a.Workspace || q.Generation != a.Generation || q.Origin != a.Origin.Host || q.Target != a.Local.Host || q.OriginPin != a.Origin.Fingerprint || q.TargetPin != a.Destination.Fingerprint || a.Local != a.Destination {
		return nil, model.Err("forbidden", "lookup differs from authenticated original source/destination")
	}
	records, e := Read(ctx, tx)
	if e != nil {
		return nil, e
	}
	old, found := inboxOf(records, q.Workspace, q.Origin, q.From, q.Key)
	if !found {
		return nil, nil
	}
	if old.Envelope.Digest() != q.Digest() {
		return nil, model.Err("idempotency_conflict", "lookup key belongs to another immutable envelope")
	}
	copy := old.Receipt
	return &copy, nil
}
