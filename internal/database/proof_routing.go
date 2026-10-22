package database

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yrashk/arm/internal/routing"
)

// Independently fixed optional catalog. No dynamically copied producer DDL or
// unfamiliar schema is allowed at this destructive proof boundary.
var routingResetSchema = []string{
	`CREATE TABLE routing_roots(workspace TEXT PRIMARY KEY,data TEXT NOT NULL)`,
	`CREATE TABLE routing_outbox(workspace TEXT NOT NULL,endpoint TEXT NOT NULL,key TEXT NOT NULL,message TEXT NOT NULL,request TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,endpoint,key),UNIQUE(workspace,message))`,
	`CREATE TABLE routing_inbox(workspace TEXT NOT NULL,origin TEXT NOT NULL,endpoint TEXT NOT NULL,key TEXT NOT NULL,message TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,origin,endpoint,key),UNIQUE(workspace,message))`,
	`CREATE TABLE routing_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,workspace TEXT NOT NULL,data TEXT NOT NULL)`,
}
var routingResetTables = []string{"routing_roots", "routing_outbox", "routing_inbox", "routing_events"}

func routeProofEndpoint(e proofRouteEndpoint, generation int64) bool {
	return e.Kind == "host" && e.ID == "operator" && generation > 0 && e.Generation == strconv.FormatInt(generation, 10)
}
func routeProofEnvelope(e proofRouteEnvelope) bool {
	raw, err := json.Marshal(e)
	if err != nil || len(raw) > 56<<10 {
		return false
	}
	if e.Schema != 1 || !sshProofHex(e.Workspace, 16) || !sshProofHex(e.Generation, 16) || !sshProofHex(e.ID, 16) || !sshProofHex(e.Origin, 16) || !sshProofHex(e.Target, 16) || e.Origin == e.Target || !sshProofHex(e.OriginPin, 32) || !sshProofHex(e.TargetPin, 32) || !validIntentKey(e.Key) || len(e.Body) < 1 || len(e.Body) > 16384 || !utf8.ValidString(e.Body) || strings.IndexByte(e.Body, 0) >= 0 || e.Created.IsZero() || !e.Expires.After(e.Created) || e.Expires.Sub(e.Created) > 24*time.Hour || !routeProofEndpoint(e.From, e.OriginMembership) || !routeProofEndpoint(e.To, e.TargetMembership) {
		return false
	}
	if r := e.ReplyTo; r != nil {
		if !sshProofHex(r.ID, 16) || !sshProofHex(r.Digest, 32) || r.Origin != e.Target || r.Target != e.Origin || r.From != e.To || r.To != e.From {
			return false
		}
	}
	return true
}
func routeProofText(s string, limit int) bool {
	if len(s) < 1 || len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}
func validIntentKey(s string) bool { return routeProofText(s, 128) }
func routeProofReceipt(r proofRouteReceipt, e proofRouteEnvelope) bool {
	return r.Schema == 1 && r.Workspace == e.Workspace && r.Generation == e.Generation && r.ID == e.ID && r.Digest == workspaceProofHash(e) && r.Origin == e.Origin && r.Target == e.Target && r.From == e.From && r.To == e.To && r.Revision > 0 && !r.At.IsZero() && oneOf(r.State, "accepted", "rejected", "superseded", "expired") && (r.Code == "" || routeProofText(r.Code, 128))
}
func (p *proofReader) readRouting() error {
	present := 0
	for _, table := range routingResetTables {
		if _, ok := p.catalog[table]; ok {
			present++
		}
	}
	if present == 0 && !p.routingSchema {
		return nil
	}
	if present != len(routingResetTables) || !p.routingSchema || !p.workspaceSchema {
		return fmt.Errorf("routing tables/marker/parent form an incomplete group")
	}
	roots := map[string]proofRouteRoot{}
	outboxes := []proofRouteOutbox{}
	inboxes := []proofRouteInbox{}
	changes := []proofRouteChange{}
	producer := routing.Records{Initialized: true}
	bytesRead := 0
	limits := []int{8, 8 * 256, 8 * 256, 8 * 2048}
	for i, table := range routingResetTables {
		count := 0
		if e := p.walk(table, func(row proofRow) error {
			count++
			if count > limits[i] {
				return fmt.Errorf("routing proof row bound exceeded")
			}
			raw, e := row.text("data")
			if e != nil {
				return e
			}
			bytesRead += len(raw)
			if len(raw) > 64<<10 || bytesRead > 32<<20 {
				return fmt.Errorf("routing proof byte bound exceeded")
			}
			key := func(column, want string) error {
				got, e := row.text(column)
				if e != nil || got != want {
					return fmt.Errorf("routing embedded/table key differs: %s", column)
				}
				return nil
			}
			switch table {
			case "routing_roots":
				var v proofRouteRoot
				if e = workspaceProofJSON(raw, &v); e != nil {
					return e
				}
				if e = key("workspace", v.Workspace); e != nil {
					return e
				}
				if _, ok := roots[v.Workspace]; ok {
					return fmt.Errorf("duplicate routing root")
				}
				roots[v.Workspace] = v
				var pv routing.Root
				if e = json.Unmarshal([]byte(raw), &pv); e != nil {
					return e
				}
				producer.Roots = append(producer.Roots, pv)
			case "routing_outbox":
				var v proofRouteOutbox
				if e = workspaceProofJSON(raw, &v); e != nil {
					return e
				}
				for name, want := range map[string]string{"workspace": v.Workspace, "endpoint": workspaceProofHash(v.Envelope.From), "key": v.Intent.Key, "message": v.Envelope.ID} {
					if e = key(name, want); e != nil {
						return e
					}
				}
				request, e := row.text("request")
				if e != nil {
					return e
				}
				bytesRead += len(request)
				if bytesRead > 32<<20 {
					return fmt.Errorf("routing raw evidence plus exact request copies exceed32MiB")
				}
				var q proofRouteIntent
				if e = workspaceProofJSON(request, &q); e != nil {
					return e
				}
				canonical, _ := json.Marshal(v.Intent)
				if string(canonical) != request || !reflect.DeepEqual(q, v.Intent) {
					return fmt.Errorf("routing exact original request column differs")
				}
				outboxes = append(outboxes, v)
				var pv routing.Outbox
				if e = json.Unmarshal([]byte(raw), &pv); e != nil {
					return e
				}
				producer.Outbox = append(producer.Outbox, pv)
			case "routing_inbox":
				var v proofRouteInbox
				if e = workspaceProofJSON(raw, &v); e != nil {
					return e
				}
				for name, want := range map[string]string{"workspace": v.Workspace, "origin": v.Envelope.Origin, "endpoint": workspaceProofHash(v.Envelope.From), "key": v.Envelope.Key, "message": v.Envelope.ID} {
					if e = key(name, want); e != nil {
						return e
					}
				}
				inboxes = append(inboxes, v)
				var pv routing.Inbox
				if e = json.Unmarshal([]byte(raw), &pv); e != nil {
					return e
				}
				producer.Inbox = append(producer.Inbox, pv)
			case "routing_events":
				var v proofRouteChange
				if e = workspaceProofJSON(raw, &v); e != nil {
					return e
				}
				if e = key("workspace", v.Workspace); e != nil {
					return e
				}
				n, e := row.integer("sequence")
				if e != nil || n < 1 {
					return fmt.Errorf("routing sequence differs")
				}
				changes = append(changes, v)
				var pv routing.Change
				if e = json.Unmarshal([]byte(raw), &pv); e != nil {
					return e
				}
				producer.Events = append(producer.Events, pv)
			}
			return nil
		}); e != nil {
			return e
		}
	}
	parents := map[string]proofWSInstance{}
	if e := p.walk("workspace_instances", func(row proofRow) error {
		raw, e := row.text("data")
		if e != nil {
			return e
		}
		var v proofWSInstance
		if e = workspaceProofJSON(raw, &v); e != nil {
			return e
		}
		parents[v.ID] = v
		return nil
	}); e != nil {
		return e
	}
	lastRoots := map[string]proofRouteRoot{}
	lastItems := map[string]proofRouteChange{}
	itemCounts := map[string]int64{}
	firstItems := map[string]proofRouteChange{}
	for _, c := range changes {
		r, ok := roots[c.Workspace]
		if !ok || c.Schema != 1 || c.After.Workspace != r.Workspace || c.After.Generation != r.Generation || c.After.Authority != r.Authority || c.After.LocalPin != r.LocalPin || c.After.Created != r.Created {
			return fmt.Errorf("routing journal immutable authority differs")
		}
		old, seen := lastRoots[c.Workspace]
		if !seen {
			if c.Before != nil || c.Kind != "initialized" || c.Actor != "uid:"+r.Authority.UID || c.Phase != "active" || c.Key != "" || c.Message != "" || c.Digest != "" || c.ReceiptHash != "" || c.After.Revision != 1 || c.After.State != "active" || c.After.Created.IsZero() || c.After.Created != c.After.Updated {
				return fmt.Errorf("routing initialization journal differs")
			}
		} else {
			if c.Before == nil || *c.Before != old || old.Revision == math.MaxInt64 || c.After.Revision != old.Revision+1 || old.State != "active" || c.After.Updated.Before(old.Updated) {
				return fmt.Errorf("routing journal chain is incomplete")
			}
			if c.Kind == "retired" {
				if c.After.State != "retired" || c.Phase != "retired" || c.Actor != "uid:"+r.Authority.UID || !validIntentKey(c.Key) || c.Message != "" || c.Digest != "" || c.ReceiptHash != "" {
					return fmt.Errorf("routing retirement differs")
				}
			} else {
				if c.After.State != "active" || !validIntentKey(c.Key) || !sshProofHex(c.Message, 16) || !sshProofHex(c.Digest, 32) {
					return fmt.Errorf("invalid routing message event")
				}
				side := "out"
				if strings.HasPrefix(c.Kind, "inbound_") {
					side = "in"
				}
				k := c.Workspace + ":" + side + ":" + c.Message
				prior, had := lastItems[k]
				if had && (prior.Key != c.Key || prior.Digest != c.Digest) {
					return fmt.Errorf("routing event immutable message changed")
				}
				if side == "out" {
					if c.Actor != "uid:"+r.Authority.UID {
						return fmt.Errorf("routing sender actor is not local owner")
					}
					switch c.Kind {
					case "outbound_prepared":
						if had || c.Phase != "prepared" || c.ReceiptHash != "" {
							return fmt.Errorf("routing prepare differs")
						}
					case "outbound_dispatching":
						if !had || prior.Phase != "prepared" || c.Phase != "dispatching" || c.ReceiptHash != "" {
							return fmt.Errorf("routing dispatch differs")
						}
					case "outbound_unknown":
						if !had || prior.Phase != "dispatching" || c.Phase != "unknown" || c.ReceiptHash != "" {
							return fmt.Errorf("routing unknown differs")
						}
					case "outbound_cancelled":
						if !had || prior.Phase != "prepared" || c.Phase != "cancelled" || c.ReceiptHash != "" {
							return fmt.Errorf("routing cancellation lacks never-dispatched evidence")
						}
					case "outbound_completed":
						if !had || !oneOf(prior.Phase, "dispatching", "unknown") || c.Phase != "completed" || !sshProofHex(c.ReceiptHash, 32) {
							return fmt.Errorf("routing completion lacks prior dispatch")
						}
					case "outbound_receipt":
						if !had || prior.Phase != "completed" || c.Phase != "completed" || !sshProofHex(c.ReceiptHash, 32) {
							return fmt.Errorf("routing receipt update differs")
						}
					default:
						return fmt.Errorf("unknown routing outbound event")
					}
				} else {
					if !oneOf(c.Kind, "inbound_accepted", "inbound_rejected") || had || len(c.Actor) != 37 || !sshProofHex(strings.TrimPrefix(c.Actor, "host:"), 16) || !oneOf(c.Phase, "accepted", "rejected", "superseded", "expired") || !sshProofHex(c.ReceiptHash, 32) {
						return fmt.Errorf("unsupported routing inbox outcome history")
					}
				}
				if !had {
					firstItems[k] = c
				}
				lastItems[k] = c
				itemCounts[k]++
			}
		}
		lastRoots[c.Workspace] = c.After
	}
	for id, r := range roots {
		parent, ok := parents[id]
		if !ok || r.Schema != 1 || r.Authority.Host != p.result.Host || r.Authority.UID != p.result.Owner || r.Authority != parent.Authority || r.Generation != parent.Generation || r.Workspace != parent.ID || r.LocalPin != parent.Self.Fingerprint || !sshProofHex(r.LocalPin, 32) || r.Created.IsZero() || r.Updated.Before(r.Created) || lastRoots[id] != r {
			return fmt.Errorf("routing root/parent/native authority/journal differs")
		}
		if r.State != "retired" {
			if e := p.block("routing", id, "Routed messaging is not explicitly locally retired; unknown sends remain retained"); e != nil {
				return e
			}
		} else if parent.State != "retired" {
			return fmt.Errorf("routing retirement precedes explicit parent workspace retirement")
		}
	}
	retained := map[string]bool{}
	messageIDs := map[string]bool{}
	for _, o := range outboxes {
		root, ok := roots[o.Workspace]
		env := o.Envelope
		k := o.Workspace + ":out:" + env.ID
		c, had := lastItems[k]
		if !ok || !had || retained[k] || o.Schema != 1 || o.Authority != root.Authority || o.Workspace != env.Workspace || env.Generation != root.Generation || env.Origin != p.result.Host || env.OriginPin != root.LocalPin || !routeProofEnvelope(env) || o.IntentHash != workspaceProofHash(o.Intent) || o.Intent.Workspace != env.Workspace || o.Intent.Target != env.Target || o.Intent.Key != env.Key || o.Intent.From != env.From || o.Intent.To != env.To || o.Intent.Body != env.Body || o.Intent.TTL != env.Expires.Sub(env.Created) || !reflect.DeepEqual(o.Intent.ReplyTo, env.ReplyTo) || o.Created != env.Created || o.Created != firstItems[k].After.Updated || o.Revision != itemCounts[k] || o.Updated != c.After.Updated || c.Key != env.Key || c.Digest != workspaceProofHash(env) || c.Phase != o.Phase {
			return fmt.Errorf("routing outbox identity/request/current history differs")
		}
		if o.Phase == "completed" {
			if o.Receipt == nil || !routeProofReceipt(*o.Receipt, env) || workspaceProofHash(o.Receipt) != c.ReceiptHash {
				return fmt.Errorf("routing completion lacks exact final outcome")
			}
		} else if o.Receipt != nil {
			return fmt.Errorf("unresolved/cancelled routing intent claims receipt")
		}
		if !oneOf(o.Phase, "completed", "cancelled") {
			if root.State == "retired" {
				return fmt.Errorf("retired routing root retains unknown dispatch")
			}
			if e := p.block("routing_outbox", env.ID, "Routed delivery is pending/unknown; time, nil lookup or revoked transport do not prove nonexecution"); e != nil {
				return e
			}
		}
		if messageIDs[o.Workspace+":"+env.ID] {
			return fmt.Errorf("routing message ID collision")
		}
		messageIDs[o.Workspace+":"+env.ID] = true
		retained[k] = true
	}
	for _, in := range inboxes {
		root, ok := roots[in.Workspace]
		env := in.Envelope
		k := in.Workspace + ":in:" + env.ID
		c, had := lastItems[k]
		if !ok || !had || retained[k] || in.Schema != 1 || !routeProofEnvelope(env) || env.Workspace != in.Workspace || env.Generation != root.Generation || env.Target != p.result.Host || env.TargetPin != root.LocalPin || !routeProofReceipt(in.Receipt, env) || in.Receipt.Revision != itemCounts[k] || in.Receipt.At != c.After.Updated || in.Received != firstItems[k].After.Updated || in.Receipt.At.Before(in.Received) || c.Actor != "host:"+env.Origin || c.Key != env.Key || c.Digest != workspaceProofHash(env) || c.Phase != in.Receipt.State || c.ReceiptHash != workspaceProofHash(in.Receipt) {
			return fmt.Errorf("routing inbox identity/outcome/history differs")
		}
		if messageIDs[in.Workspace+":"+env.ID] {
			return fmt.Errorf("routing message ID collision")
		}
		messageIDs[in.Workspace+":"+env.ID] = true
		retained[k] = true
	}
	if len(retained) != len(lastItems) {
		return fmt.Errorf("routing message history was deleted")
	}
	// Supplemental pure validation is NOT the independent exact schema/raw/
	// authority/retirement proof above. It also checks producer capacity promises.
	if e := routing.Validate(producer); e != nil {
		return fmt.Errorf("routing producer graph invalid: %w", e)
	}
	return nil
}
