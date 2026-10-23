package database

import (
	"encoding/json"
	"fmt"
	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/published"
	"reflect"
	"sort"
)

var publishedResetTables = []string{"published_agents", "published_agent_changes", "routed_agent_links"}
var publishedResetSchema = []string{
	`CREATE TABLE published_agents(workspace TEXT NOT NULL,endpoint TEXT NOT NULL,generation TEXT NOT NULL,key TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,endpoint,generation),UNIQUE(workspace,key))`,
	`CREATE TABLE published_agent_changes(workspace TEXT NOT NULL,sequence INTEGER NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,sequence))`,
	`CREATE TABLE routed_agent_links(workspace TEXT NOT NULL,message TEXT NOT NULL,local_message TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,message),UNIQUE(local_message))`,
}

func pubKey(p proofPublishedPublication) string {
	return p.Scope.Workspace + "/" + p.Endpoint.ID + "/" + p.Endpoint.Generation
}
func pubInitial(p proofPublishedPublication) proofPublishedPublication {
	p.State = "active"
	p.Revision = 1
	p.Updated = p.Created
	p.Withdrawal = nil
	return p
}
func (p *proofReader) readPublished() error {
	count := 0
	for _, t := range publishedResetTables {
		if _, ok := p.catalog[t]; ok {
			count++
		}
	}
	if count == 0 && !p.publishedSchema {
		return nil
	}
	if count != 3 || !p.publishedSchema || !p.workspaceSchema {
		return fmt.Errorf("incomplete published agent optional group")
	}
	pubs := map[string]proofPublishedPublication{}
	changes := []proofPublishedChange{}
	links := []proofPublishedLink{}
	producer := published.Records{Initialized: true}
	bytes := 0
	for i, t := range publishedResetTables {
		n := 0
		limit := []int{8 * 256, 8 * 512, 8 * 256}[i]
		if e := p.walk(t, func(r proofRow) error {
			n++
			if n > limit {
				return fmt.Errorf("published agent independent row bound exceeded")
			}
			raw, e := r.text("data")
			if e != nil {
				return e
			}
			cap := 12288
			if i == 1 {
				cap = 28672
			}
			bytes += len(raw)
			if len(raw) > cap || bytes > 16<<20 {
				return fmt.Errorf("published agent independent byte bound exceeded")
			}
			key := func(col, want string) error {
				got, e := r.text(col)
				if e != nil || got != want {
					return fmt.Errorf("published embedded SQL key differs")
				}
				return nil
			}
			switch i {
			case 0:
				var v proofPublishedPublication
				if e = workspaceProofJSON(raw, &v); e != nil {
					return e
				}
				if e = key("workspace", v.Scope.Workspace); e != nil {
					return e
				}
				if e = key("endpoint", v.Endpoint.ID); e != nil {
					return e
				}
				if e = key("generation", v.Endpoint.Generation); e != nil {
					return e
				}
				if e = key("key", v.Request.Key); e != nil {
					return e
				}
				if _, ok := pubs[pubKey(v)]; ok {
					return fmt.Errorf("duplicate publication")
				}
				pubs[pubKey(v)] = v
				var pv published.Publication
				if e = json.Unmarshal([]byte(raw), &pv); e != nil {
					return e
				}
				producer.Publications = append(producer.Publications, pv)
			case 1:
				var v proofPublishedChange
				if e = workspaceProofJSON(raw, &v); e != nil {
					return e
				}
				if e = key("workspace", v.Workspace); e != nil {
					return e
				}
				seq, e := r.integer("sequence")
				if e != nil || seq != v.Sequence {
					return fmt.Errorf("publication journal SQL sequence differs")
				}
				changes = append(changes, v)
				var pv published.Change
				json.Unmarshal([]byte(raw), &pv)
				producer.Changes = append(producer.Changes, pv)
			case 2:
				var v proofPublishedLink
				if e = workspaceProofJSON(raw, &v); e != nil {
					return e
				}
				if e = key("workspace", v.Context.Workspace); e != nil {
					return e
				}
				if e = key("message", v.Context.Message); e != nil {
					return e
				}
				if e = key("local_message", v.LocalMessage); e != nil {
					return e
				}
				links = append(links, v)
				var pv published.Link
				json.Unmarshal([]byte(raw), &pv)
				producer.Links = append(producer.Links, pv)
			}
			return nil
		}); e != nil {
			return e
		}
	}
	retained, active, events := map[string]int{}, map[string]int{}, map[string]int{}
	contactIDs := map[string]bool{}
	for _, v := range pubs {
		s, q := v.Scope, v.Request
		plan := proofPublishedPlan{Request: q, Scope: s, ContactSHA: v.ContactSHA}
		digest := workspaceProofHash(plan)
		plan.ReviewSHA = digest
		if v.Schema != 1 || !sshProofHex(s.Workspace, 16) || !sshProofHex(s.Generation, 16) || s.Authority.Host != p.result.Host || s.Authority.UID != p.result.Owner || !sshProofHex(s.Pin, 32) || s.Membership < 1 || s.WorkspaceRevision < 1 || q.Workspace != s.Workspace || !routeProofText(q.ID, 128) || !routeProofText(q.Session, 64) || !sshProofHex(q.Registration, 16) || !routeProofText(q.Label, 128) || !routeProofText(q.Key, 128) || !routeProofText(q.Reason, 512) || q.Delivery != "poll" || v.Endpoint.Kind != "agent" || v.Endpoint.ID != workspaceProofHash([]string{s.Authority.Host, s.Workspace, q.Key})[:32] || v.Endpoint.Generation != workspaceProofHash(plan)[:32] || v.ReviewSHA != digest || v.Contact.ID != q.ID || v.Contact.Session != q.Session || v.Contact.Registration != q.Registration || !validHistoricalContact(v.Contact) || v.ContactSHA != workspaceProofHash(v.Contact) || v.Actor.UID != p.result.Owner || v.Actor.Username != "" && !routeProofText(v.Actor.Username, 256) || v.Created.IsZero() || v.Updated.Before(v.Created) {
			return fmt.Errorf("independent exact publication/review/contact identity invalid")
		}
		var raw string
		if e := p.tx.QueryRowContext(p.ctx, `SELECT data FROM workspace_instances WHERE id=?`, s.Workspace).Scan(&raw); e != nil {
			return e
		}
		var parent proofWSInstance
		if e := workspaceProofJSON(raw, &parent); e != nil {
			return e
		}
		if parent.ID != s.Workspace || parent.Generation != s.Generation || parent.Authority != s.Authority || parent.Self.Fingerprint != s.Pin {
			return fmt.Errorf("publication lost original workspace parent")
		}
		retained[s.Workspace]++
		if v.State == "active" {
			encoded, _ := json.Marshal(v)
			if v.Revision != 1 || v.Withdrawal != nil || v.Created != v.Updated || len(encoded) > 6144 || parent.State != "active" {
				return fmt.Errorf("active publication phase or finalization reserve differs")
			}
			k := s.Workspace + "/" + v.Contact.ID
			if contactIDs[k] {
				return fmt.Errorf("ambiguous active published contact")
			}
			contactIDs[k] = true
			active[s.Workspace]++
			if active[s.Workspace] > parent.Policy.MaxPublishedAgents {
				return fmt.Errorf("active publication exceeds workspace policy")
			}
			var cr string
			if e := p.tx.QueryRowContext(p.ctx, `SELECT data FROM agents WHERE id=?`, v.Contact.ID).Scan(&cr); e != nil {
				return e
			}
			var current agents.Contact
			if e := workspaceProofJSON(cr, &current); e != nil {
				return e
			}
			if current != v.Contact {
				return fmt.Errorf("active publication contact was replaced without atomic withdrawal")
			}
			if e := p.block("published_agent", v.Endpoint.ID, "Explicit agent publication remains active"); e != nil {
				return e
			}
		} else if v.State != "withdrawn" || v.Revision != 2 || v.Withdrawal == nil || v.Withdrawal.Revision != 1 || !routeProofText(v.Withdrawal.Key, 128) || !routeProofText(v.Withdrawal.Reason, 512) {
			return fmt.Errorf("publication withdrawal phase differs")
		}
	}
	// Changes are read in physical PK order by p.walk; sort explicitly by their
	// typed workspace/sequence, then independently reconstruct every generation.
	sortPublishedChanges(changes)
	last := map[string]proofPublishedPublication{}
	seq := map[string]int64{}
	for _, c := range changes {
		if c.Sequence != seq[c.Workspace]+1 || c.After.Scope.Workspace != c.Workspace {
			return fmt.Errorf("publication sequence incomplete")
		}
		seq[c.Workspace] = c.Sequence
		events[c.Workspace]++
		k := pubKey(c.After)
		prior, ok := last[k]
		if !ok {
			if c.Before != nil || c.After.State != "active" || c.After.Revision != 1 {
				return fmt.Errorf("publication creation missing")
			}
		} else if c.Before == nil || !reflect.DeepEqual(*c.Before, prior) || prior.State != "active" || c.After.State != "withdrawn" || !reflect.DeepEqual(pubInitial(prior), pubInitial(c.After)) {
			return fmt.Errorf("publication immutable generation retargeted")
		}
		last[k] = c.After
	}
	if len(last) != len(pubs) {
		return fmt.Errorf("publication journal cardinality differs")
	}
	for k, v := range pubs {
		if !reflect.DeepEqual(last[k], v) {
			return fmt.Errorf("publication journal terminal differs")
		}
	}
	if len(retained) > 8 {
		return fmt.Errorf("publication workspace bound exceeded")
	}
	reserve := 0
	for id, n := range retained {
		if n > 256 || active[id] > 32 || events[id]+active[id] > 512 {
			return fmt.Errorf("publication finalization capacity exhausted")
		}
		reserve += active[id] * 40960
	}
	if bytes+reserve > 16<<20 {
		return fmt.Errorf("publication finalization bytes exhausted")
	}
	p.publishedLinks = map[string]proofPublishedLink{}
	linkCounts := map[string]int{}
	for _, l := range links {
		c := l.Context
		var pub proofPublishedPublication
		found := false
		for _, v := range pubs {
			if v.Scope.Workspace == c.Workspace && v.Endpoint == c.To {
				pub = v
				found = true
			}
		}
		if l.Schema != 1 || !found || !sshProofHex(l.LocalMessage, 16) || !sshProofHex(c.Message, 16) || !sshProofHex(c.Digest, 32) || !sshProofHex(c.Origin, 16) || c.Target != p.result.Host || c.Origin == c.Target || c.Generation != pub.Scope.Generation || c.To.Kind != "agent" || pub.Contact.ID != l.Recipient || pub.Contact.Registration != l.Registration || pub.ContactSHA != l.ContactSHA || l.Created.IsZero() {
			return fmt.Errorf("independent routed link identity differs")
		}
		if _, ok := p.publishedLinks[l.LocalMessage]; ok {
			return fmt.Errorf("duplicate routed local link")
		}
		var raw string
		if e := p.tx.QueryRowContext(p.ctx, `SELECT data FROM routing_inbox WHERE workspace=? AND message=?`, c.Workspace, c.Message).Scan(&raw); e != nil {
			return e
		}
		var in proofRouteInbox
		if e := workspaceProofJSON(raw, &in); e != nil {
			return e
		}
		env := in.Envelope
		from := proofPublishedAddress(env.From)
		to := proofPublishedAddress(env.To)
		if env.Workspace != c.Workspace || env.Generation != c.Generation || env.ID != c.Message || workspaceProofHash(env) != c.Digest || env.Origin != c.Origin || env.Target != c.Target || from != c.From || to != c.To || !oneOf(in.Receipt.State, "accepted", "acknowledged") || in.Received != l.Created {
			return fmt.Errorf("routed link changed immutable inbox")
		}
		if e := p.tx.QueryRowContext(p.ctx, `SELECT data FROM messages WHERE id=?`, l.LocalMessage).Scan(&raw); e != nil {
			return e
		}
		var msg proofMessage
		if e := workspaceProofJSON(raw, &msg); e != nil {
			return e
		}
		if msg.ID != l.LocalMessage || msg.RoutedOrigin == nil || *msg.RoutedOrigin != c || msg.Audience != l.Recipient || msg.Sender != "workspace:"+c.Origin+"/"+c.From.ID || msg.Body != env.Body || msg.Created != l.Created || msg.SenderContact != nil || msg.OriginHost != "" || msg.ReplyTo != "" || msg.AgentKey != "" || msg.Key != "" || msg.Lease != "" || len(msg.Deliveries) != 1 {
			return fmt.Errorf("routed link changed local message")
		}
		d, ok := msg.Deliveries[l.Recipient]
		if !ok || d.Mode != "poll" || d.State != "available" || d.Registration != l.Registration {
			return fmt.Errorf("routed poll link promoted or retargeted delivery")
		}
		var n int
		if e := p.tx.QueryRowContext(p.ctx, `SELECT COUNT(*) FROM agent_inbox WHERE recipient=? AND registration=? AND message=?`, l.Recipient, l.Registration, l.LocalMessage).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return fmt.Errorf("routed link lost exact agent inbox")
		}
		var receiptRaw string
		if e := p.tx.QueryRowContext(p.ctx, `SELECT receipt FROM agent_inbox WHERE recipient=? AND registration=? AND message=?`, l.Recipient, l.Registration, l.LocalMessage).Scan(&receiptRaw); e != nil {
			return e
		}
		var localReceipt agents.Receipt
		if len(receiptRaw) > 8192 || workspaceProofJSON(receiptRaw, &localReceipt) != nil {
			return fmt.Errorf("routed poll receipt invalid")
		}
		if in.Receipt.State == "acknowledged" && (localReceipt.Acknowledged == nil || localReceipt.Received == nil || *localReceipt.Acknowledged != in.Receipt.At || localReceipt.Text != "" || localReceipt.ReplyID != "") {
			return fmt.Errorf("independent routed acknowledgement lacks original exact local receipt")
		}
		p.publishedLinks[l.LocalMessage] = l
		linkCounts[c.Workspace]++
		if linkCounts[c.Workspace] > 256 {
			return fmt.Errorf("routed link capacity exceeded")
		}
	}
	// Only after the independent fixed-shape/raw/authority/journal/cross-link
	// proof above do we run the supplemental producer budget validator.
	if e := published.Validate(producer); e != nil {
		return fmt.Errorf("published supplemental validator: %w", e)
	}
	p.publishedRecords = &producer
	return nil
}
func (p *proofReader) proofPublishedSource(id string, e proofRouteEnvelope) error {
	if p.publishedRecords == nil {
		return fmt.Errorf("agent outbox lacks original publication group")
	}
	for _, v := range p.publishedRecords.Publications {
		if v.Scope.Workspace == id && v.Endpoint.ID == e.From.ID && v.Endpoint.Generation == e.From.Generation && v.Scope.Generation == e.Generation && v.Scope.Authority.Host == e.Origin && v.Scope.Pin == e.OriginPin && v.Scope.Membership == e.OriginMembership {
			return nil
		}
	}
	return fmt.Errorf("agent outbox original publication disappeared")
}
func sortPublishedChanges(v []proofPublishedChange) {
	sort.Slice(v, func(i, j int) bool {
		if v[i].Workspace == v[j].Workspace {
			return v[i].Sequence < v[j].Sequence
		}
		return v[i].Workspace < v[j].Workspace
	})
}
