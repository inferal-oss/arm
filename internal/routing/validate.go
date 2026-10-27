package routing

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/published"
	"github.com/inferal-oss/arm/internal/workspace"
)

func known(v string, values ...string) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
func validRoot(r Root) bool {
	uid, e := strconv.ParseUint(r.Authority.UID, 10, 32)
	return r.Schema == Schema && hexID(r.Workspace, 16) && hexID(r.Generation, 16) && hexID(r.Authority.Host, 16) && e == nil && strconv.FormatUint(uid, 10) == r.Authority.UID && hexID(r.LocalPin, 32) && known(r.State, "active", "retired") && r.Revision > 0 && !r.Created.IsZero() && !r.Updated.Before(r.Created)
}
func Validate(v Records) error {
	if !v.Initialized {
		if len(v.Roots)+len(v.Outbox)+len(v.Inbox)+len(v.Events) > 0 || v.Graph != nil {
			return invalid("uninitialized routing ledger has records")
		}
		return nil
	}
	roots := map[string]Root{}
	for _, r := range v.Roots {
		if !validRoot(r) {
			return invalid("invalid immutable routing authority")
		}
		if _, ok := roots[r.Workspace]; ok {
			return invalid("duplicate routing root")
		}
		roots[r.Workspace] = r
	}
	if len(roots) > workspace.MaxWorkspaces {
		return invalid("too many routing workspaces")
	}
	lastRoot := map[string]Root{}
	lastItem := map[string]Change{}
	revisions := map[string]int64{}
	firstItem := map[string]Change{}
	for _, c := range v.Events {
		r, ok := roots[c.Workspace]
		if !ok || c.Schema != Schema || !validRoot(c.After) || c.After.Workspace != c.Workspace || c.After.Generation != r.Generation || c.After.Authority != r.Authority || c.After.LocalPin != r.LocalPin || c.After.Created != r.Created {
			return invalid("routing event has mismatched immutable root")
		}
		before, exists := lastRoot[c.Workspace]
		if !exists {
			if c.Before != nil || c.Kind != "initialized" || c.After.Revision != 1 || c.After.State != "active" || c.After.Created != c.After.Updated || c.Actor != "uid:"+r.Authority.UID || c.Key != "" || c.Message != "" || c.Digest != "" || c.ReceiptHash != "" || c.Phase != "active" {
				return invalid("routing initialization history incomplete")
			}
		} else {
			if c.Before == nil || *c.Before != before || before.Revision == math.MaxInt64 || c.After.Revision != before.Revision+1 || c.After.Updated.Before(before.Updated) || before.State != "active" {
				return invalid("routing event chain is not exact monotone history")
			}
			if c.Kind == "graph_enabled" {
				if c.After.State != "active" || c.Phase != "active" || c.Actor != "uid:"+r.Authority.UID || c.Key != "" || c.Message != "" || c.Digest != "" || c.ReceiptHash != "" {
					return invalid("graph enablement event differs")
				}
			} else if c.Kind == "retired" {
				if c.After.State != "retired" || c.Phase != "retired" || c.Actor != "uid:"+r.Authority.UID || !validKey(c.Key) || c.Message != "" || c.Digest != "" || c.ReceiptHash != "" {
					return invalid("routing retirement history differs")
				}
			} else {
				if c.After.State != "active" || !validKey(c.Key) || !hexID(c.Message, 16) || !hexID(c.Digest, 32) {
					return invalid("routing message event identity invalid")
				}
				side := ""
				switch c.Kind {
				case "outbound_prepared", "outbound_dispatching", "outbound_unknown", "outbound_completed", "outbound_receipt", "outbound_cancelled":
					side = "out"
					if c.Actor != "uid:"+r.Authority.UID {
						return invalid("outbound actor is not local owner")
					}
				case "inbound_accepted", "inbound_rejected", "inbound_receipt":
					side = "in"
					if len(c.Actor) != 5+32 || c.Actor[:5] != "host:" || !hexID(c.Actor[5:], 16) {
						return invalid("inbound actor missing exact origin")
					}
				default:
					return invalid("unknown routing message event")
				}
				key := c.Workspace + ":" + side + ":" + c.Message
				prior, seen := lastItem[key]
				if seen && (prior.Digest != c.Digest || prior.Key != c.Key) {
					return invalid("routing message immutable digest/key changed")
				}
				if side == "out" {
					switch c.Kind {
					case "outbound_prepared":
						if seen || c.Phase != "prepared" || c.ReceiptHash != "" {
							return invalid("outbox preparation differs")
						}
					case "outbound_cancelled":
						if !seen || prior.Phase != "prepared" || c.Phase != "cancelled" || c.ReceiptHash != "" {
							return invalid("outbox cancellation lacks undispatched proof")
						}
					case "outbound_dispatching":
						if !seen || prior.Phase != "prepared" || c.Phase != "dispatching" || c.ReceiptHash != "" {
							return invalid("outbox dispatch differs")
						}
					case "outbound_unknown":
						if !seen || prior.Phase != "dispatching" || c.Phase != "unknown" || c.ReceiptHash != "" {
							return invalid("outbox uncertainty differs")
						}
					case "outbound_completed":
						if !seen || !known(prior.Phase, "dispatching", "unknown") || c.Phase != "completed" || !hexID(c.ReceiptHash, 32) {
							return invalid("outbox completion differs")
						}
					case "outbound_receipt":
						if !seen || prior.Phase != "completed" || c.Phase != "completed" || !hexID(c.ReceiptHash, 32) {
							return invalid("outbox receipt update differs")
						}
					}
				} else {
					if c.Kind == "inbound_receipt" {
						if !seen || !hexID(c.ReceiptHash, 32) {
							return invalid("inbox receipt update missing origin")
						}
					} else if seen {
						return invalid("duplicate inbox acceptance")
					}
					if !known(c.Phase, "accepted", "delivery_pending", "delivered", "acknowledged", "rejected", "superseded", "expired") || !hexID(c.ReceiptHash, 32) {
						return invalid("inbox outcome differs")
					}
				}
				if !seen {
					firstItem[key] = c
				}
				revisions[key]++
				lastItem[key] = c
			}
		}
		lastRoot[c.Workspace] = c.After
	}
	for id, r := range roots {
		if lastRoot[id] != r {
			return invalid("routing root missing complete journal")
		}
	}
	ids := map[string]bool{}
	seenItems := map[string]bool{}
	for _, o := range v.Outbox {
		r, ok := roots[o.Workspace]
		e := o.Envelope
		if !ok || o.Schema != Schema || o.Authority != r.Authority || o.Workspace != e.Workspace || o.Workspace != o.Intent.Workspace || e.Generation != r.Generation || e.Origin != r.Authority.Host || !e.Validate() || o.IntentHash != hash(o.Intent) || o.Intent.Key != e.Key || o.Intent.Target != e.Target || o.Intent.From != e.From || o.Intent.To != e.To || o.Intent.Body != e.Body || o.Intent.TTL != e.Expires.Sub(e.Created) || hash(o.Intent.ReplyTo) != hash(e.ReplyTo) || o.Revision < 1 || o.Created != e.Created || o.Updated.Before(o.Created) || !known(o.Phase, "prepared", "dispatching", "unknown", "completed", "cancelled") {
			return invalid("outbox immutable request/scope invalid")
		}
		if e.From.Kind == "agent" {
			if v.Published == nil {
				return invalid("agent outbox lacks complete publication group")
			}
			p, ok := published.Find(*v.Published, o.Workspace, published.Address(e.From))
			if !ok || p.Scope.Generation != e.Generation || p.Scope.Authority != r.Authority || p.Scope.Pin != e.OriginPin || p.Scope.Membership != e.OriginMembership {
				return invalid("outbox lost immutable sender publication")
			}
		}
		if o.Phase == "completed" {
			if o.Receipt == nil || !o.Receipt.Matches(e) {
				return invalid("settled outbox lacks exact authoritative receipt")
			}
		} else if o.Receipt != nil {
			return invalid("unsettled outbox claims a receipt")
		}
		key := o.Workspace + ":out:" + e.ID
		c, ok := lastItem[key]
		if !ok || o.Revision != revisions[key] || seenItems[key] || c.Digest != e.Digest() || c.Key != e.Key || c.Phase != o.Phase || c.After.Updated != o.Updated {
			return invalid("outbox missing exact current phase journal")
		}
		if o.Receipt != nil && hash(o.Receipt) != c.ReceiptHash {
			return invalid("outbox receipt journal differs")
		}
		seenItems[key] = true
		id := o.Workspace + ":" + e.ID
		if ids[id] {
			return invalid("message ID collision")
		}
		ids[id] = true
	}
	for _, in := range v.Inbox {
		r, ok := roots[in.Workspace]
		e := in.Envelope
		if !ok || in.Schema != Schema || e.Workspace != in.Workspace || e.Generation != r.Generation || e.Target != r.Authority.Host || !e.Validate() || !in.Receipt.Matches(e) || in.Received.IsZero() || in.Receipt.At.Before(in.Received) {
			return invalid("inbox immutable request/scope invalid")
		}
		if e.To.Kind == "agent" && known(in.Receipt.State, "accepted", "acknowledged") {
			if v.Published == nil {
				return invalid("agent inbox lacks complete publication group")
			}
			found := false
			for _, l := range v.Published.Links {
				if l.Context.Workspace == in.Workspace && l.Context.Message == e.ID && l.Context.Digest == e.Digest() && l.Context.From == published.Address(e.From) && l.Context.To == published.Address(e.To) {
					found = true
				}
			}
			if !found {
				return invalid("accepted agent inbox lacks exact local delivery link")
			}
		}
		key := in.Workspace + ":in:" + e.ID
		c, ok := lastItem[key]
		if !ok || in.Receipt.Revision != revisions[key] || in.Received != firstItem[key].After.Updated || seenItems[key] || c.Actor != "host:"+e.Origin || c.Digest != e.Digest() || c.Key != e.Key || c.Phase != in.Receipt.State || c.ReceiptHash != hash(in.Receipt) || c.After.Updated != in.Receipt.At {
			return invalid("inbox missing exact acceptance/outcome history")
		}
		seenItems[key] = true
		id := in.Workspace + ":" + e.ID
		if ids[id] {
			return invalid("message ID collision")
		}
		ids[id] = true
	}
	if len(seenItems) != len(lastItem) {
		return invalid("routing event message has no retained record")
	}
	if e := validateGraph(v); e != nil {
		return e
	}
	return capacity(v)
}
func capacity(v Records) error {
	bytes := 0
	if v.Published != nil {
		bytes += v.Published.BudgetBytes()
	}
	add := func(x any) { raw, _ := json.Marshal(x); bytes += len(raw) }
	for _, r := range v.Roots {
		add(r)
		pending, events, out, in := 0, 0, 0, 0
		retireReserve := 0
		if r.State == "active" {
			retireReserve = 1
			bytes += 4096
		}
		for _, o := range v.Outbox {
			if o.Workspace != r.Workspace {
				continue
			}
			out++
			if !known(o.Phase, "completed", "cancelled") {
				pending++
				bytes += 16384
			}
			switch o.Phase {
			case "prepared":
				events += 3
			case "dispatching":
				events += 2
			case "unknown":
				events++
			}
			add(o)
			add(o.Intent) // exact request column is retained independently
		}
		for _, i := range v.Inbox {
			if i.Workspace == r.Workspace {
				in++
				if i.Envelope.To.Kind == "agent" && i.Receipt.State == "accepted" {
					events++
					bytes += 4096
				}
				add(i)
			}
		}
		for _, e := range v.Events {
			if e.Workspace == r.Workspace {
				events++
				add(e)
			}
		}
		if pending > MaxPending || out > MaxOutbox || in > MaxMessages || events+retireReserve > MaxEvents {
			return model.Err("routing_capacity_exhausted", "routing history/pending capacity including settlement reserve is full")
		}
	}
	if e := checkGraphCapacity(v, add, &bytes); e != nil {
		return model.Err("routing_capacity_exhausted", e.Error())
	}
	if max(bytes, v.rawBytes) > MaxLedgerBytes {
		return model.Err("routing_capacity_exhausted", "routing ledger including settlement reserve exceeds32MiB")
	}
	return nil
}
