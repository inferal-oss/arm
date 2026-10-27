package routing

import (
	"context"
	"database/sql"
	"getarm.org/internal/published"
	"getarm.org/internal/workspace"
	"time"
)

// AcknowledgePoll consumes the finalization slot reserved at poll acceptance.
// It uses no current remote authority and grants no fresh delivery: only the
// original exact local inbox recipient may settle its already accepted record.
func AcknowledgePoll(ctx context.Context, tx *sql.Tx, a workspace.Authority, l published.Link, at time.Time) error {
	v, e := Read(ctx, tx)
	if e != nil {
		return e
	}
	root, ok := rootOf(v, l.Context.Workspace)
	if !ok || root.Authority != a {
		return invalid("poll acknowledgement lacks original active authority")
	}
	if root.State == "retired" {
		return nil
	} // Historical local receipt may settle without extending retired routing authority.
	for _, in := range v.Inbox {
		if in.Workspace != l.Context.Workspace || in.Envelope.ID != l.Context.Message {
			continue
		}
		if in.Envelope.Digest() != l.Context.Digest || in.Envelope.To != Endpoint(l.Context.To) {
			return invalid("poll acknowledgement original differs")
		}
		if in.Receipt.State == "acknowledged" {
			return nil
		}
		if in.Receipt.State != "accepted" || in.Envelope.To.Kind != "agent" || at.Before(in.Receipt.At) {
			return invalid("poll acknowledgement phase or clock differs")
		}
		in.Receipt.State = "acknowledged"
		in.Receipt.Revision++
		in.Receipt.At = at
		if e = writeInbox(ctx, tx, in); e != nil {
			return e
		}
		return advance(ctx, tx, root, at, Change{Kind: "inbound_receipt", Actor: "host:" + in.Envelope.Origin, Key: in.Envelope.Key, Message: in.Envelope.ID, Digest: in.Envelope.Digest(), Phase: in.Receipt.State, ReceiptHash: hash(in.Receipt)})
	}
	return invalid("poll acknowledgement original missing")
}
