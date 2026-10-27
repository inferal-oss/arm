package published

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"getarm.org/internal/agents"
	"getarm.org/internal/workspace"
)

func Read(ctx context.Context, tx *sql.Tx) (Records, error) {
	v := Records{Publications: []Publication{}, Changes: []Change{}, Links: []Link{}}
	ok, e := schema(ctx, tx, false)
	if e != nil || !ok {
		return v, e
	}
	v.Initialized = true
	read := func(table, columns string, limit int, fn func([]string, []byte) error) error {
		rowBytes := MaxRecord
		if table == Tables[1] {
			rowBytes = MaxChangeRecord
		}
		order := ""
		if table == Tables[1] {
			order = " ORDER BY workspace,sequence"
		}
		rows, e := tx.QueryContext(ctx, fmt.Sprintf("SELECT %s,substr(CAST(data AS BLOB),1,%d) FROM %s%s LIMIT %d", columns, rowBytes+1, table, order, limit+1))
		if e != nil {
			return e
		}
		count := 0
		for rows.Next() {
			count++
			if count > limit {
				rows.Close()
				return Invalid("publication row bound exceeded")
			}
			names, _ := rows.Columns()
			keys := make([]string, len(names)-1)
			args := make([]any, len(names))
			for i := range keys {
				args[i] = &keys[i]
			}
			var raw []byte
			args[len(args)-1] = &raw
			if e = rows.Scan(args...); e != nil {
				rows.Close()
				return e
			}
			if len(raw) > rowBytes {
				rows.Close()
				return Invalid("publication record exceeds bounded size")
			}
			v.RawBytes += len(raw)
			if v.RawBytes > MaxBytes {
				rows.Close()
				return Invalid("publication byte bound exceeded")
			}
			if e = fn(keys, raw); e != nil {
				rows.Close()
				return e
			}
		}
		e = rows.Err()
		rows.Close()
		return e
	}
	if e = read(Tables[0], strings.Join([]string{rawTextKey("workspace", 32), rawTextKey("endpoint", 32), rawTextKey("generation", 32), rawTextKey("key", 128)}, ","), workspace.MaxWorkspaces*MaxRetained, func(k []string, b []byte) error {
		var p Publication
		if e := workspace.Decode(b, &p); e != nil {
			return e
		}
		if p.Scope.Workspace != k[0] || p.Endpoint.ID != k[1] || p.Endpoint.Generation != k[2] || p.Request.Key != k[3] {
			return Invalid("publication row key differs")
		}
		v.Publications = append(v.Publications, p)
		return nil
	}); e != nil {
		return v, e
	}
	if e = read(Tables[1], rawTextKey("workspace", 32)+",CASE WHEN typeof(sequence)='integer' THEN CAST(sequence AS TEXT) ELSE NULL END", workspace.MaxWorkspaces*MaxEvents, func(k []string, b []byte) error {
		var c Change
		if e := workspace.Decode(b, &c); e != nil {
			return e
		}
		if c.Workspace != k[0] || strconv.FormatInt(c.Sequence, 10) != k[1] {
			return Invalid("publication journal key differs")
		}
		v.Changes = append(v.Changes, c)
		return nil
	}); e != nil {
		return v, e
	}
	if e = read(Tables[2], strings.Join([]string{rawTextKey("workspace", 32), rawTextKey("message", 32), rawTextKey("local_message", 32)}, ","), workspace.MaxWorkspaces*MaxLinks, func(k []string, b []byte) error {
		var l Link
		if e := workspace.Decode(b, &l); e != nil {
			return e
		}
		if l.Context.Workspace != k[0] || l.Context.Message != k[1] || l.LocalMessage != k[2] {
			return Invalid("routed agent link key differs")
		}
		v.Links = append(v.Links, l)
		return nil
	}); e != nil {
		return v, e
	}
	if e = Validate(v); e != nil {
		return v, e
	}
	activeCounts := map[string]int{}
	for _, p := range v.Publications {
		var raw []byte
		if e = tx.QueryRowContext(ctx, `SELECT substr(CAST(data AS BLOB),1,1048577) FROM workspace_instances WHERE id=?`, p.Scope.Workspace).Scan(&raw); e != nil {
			return v, e
		}
		var root workspace.Instance
		if workspace.Decode(raw, &root) != nil || root.Generation != p.Scope.Generation || root.Authority != p.Scope.Authority || root.Self.Host != p.Scope.Authority.Host || root.Self.Fingerprint != p.Scope.Pin {
			return v, Invalid("publication parent authority differs")
		}
		if p.State == "active" {
			activeCounts[p.Scope.Workspace]++
			if root.State != "active" || activeCounts[p.Scope.Workspace] > root.Policy.MaxPublishedAgents {
				return v, Invalid("active publication exceeds current workspace policy")
			}
			c, e := contact(ctx, tx, p.Contact.ID)
			if e != nil || Hash(c) != p.ContactSHA {
				return v, Invalid("active publication lost exact current contact binding")
			}
		}
	}
	for _, l := range v.Links {
		if e = checkLink(ctx, tx, v, l); e != nil {
			return v, e
		}
	}
	return v, nil
}
func immutable(p Publication) Publication {
	p.State = "active"
	p.Revision = 1
	p.Updated = p.Created
	p.Withdrawal = nil
	return p
}
func Validate(v Records) error {
	if !v.Initialized {
		if len(v.Publications)+len(v.Changes)+len(v.Links) != 0 {
			return Invalid("uninitialized publication records exist")
		}
		return nil
	}
	pubs := map[string]Publication{}
	retained, active, events, links := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	contactIDs := map[string]bool{}
	for _, p := range v.Publications {
		encoded, _ := json.Marshal(p)
		if len(encoded) > MaxRecord || p.State == "active" && len(encoded) > MaxInitialPublication {
			return Invalid("publication lacks escaped-byte finalization space")
		}
		s := p.Scope
		uid, e := strconv.ParseUint(s.Authority.UID, 10, 32)
		if p.Schema != 1 || p.Request.Validate() != nil || p.Request.Workspace != s.Workspace || !Hex(s.Workspace, 16) || !Hex(s.Generation, 16) || !Hex(s.Authority.Host, 16) || e != nil || strconv.FormatUint(uid, 10) != s.Authority.UID || s.Membership < 1 || s.WorkspaceRevision < 1 || !Hex(s.Pin, 32) || !ContactValid(p.Contact) || p.Contact.ID != p.Request.ID || p.Contact.Session != p.Request.Session || p.Contact.Registration != p.Request.Registration || p.ContactSHA != Hash(p.Contact) || p.Actor.UID != s.Authority.UID || p.Actor.Username != "" && !Text(p.Actor.Username, 256) || p.Endpoint.Kind != "agent" || p.Endpoint.ID != Hash([]string{s.Authority.Host, s.Workspace, p.Request.Key})[:32] || p.Endpoint.Generation != Hash(Plan{Request: p.Request, Scope: s, ContactSHA: p.ContactSHA, ReviewSHA: p.ReviewSHA})[:32] || p.ReviewSHA != (Plan{Request: p.Request, Scope: s, ContactSHA: p.ContactSHA}).Digest() || p.Created.IsZero() || p.Updated.Before(p.Created) {
			return Invalid("invalid exact publication identity/review")
		}
		if p.State == "active" {
			if p.Revision != 1 || p.Updated != p.Created || p.Withdrawal != nil {
				return Invalid("active publication phase differs")
			}
			key := s.Workspace + "/" + p.Contact.ID
			if contactIDs[key] {
				return Invalid("contact has ambiguous active publications")
			}
			contactIDs[key] = true
			active[s.Workspace]++
		} else if p.State != "withdrawn" || p.Revision != 2 || p.Withdrawal == nil || p.Withdrawal.Revision != 1 || !Text(p.Withdrawal.Key, 128) || !Text(p.Withdrawal.Reason, 512) {
			return Invalid("invalid exact publication withdrawal")
		}
		key := s.Workspace + "/" + p.Endpoint.ID + "/" + p.Endpoint.Generation
		if _, exists := pubs[key]; exists {
			return Invalid("duplicate publication generation")
		}
		pubs[key] = p
		retained[s.Workspace]++
	}
	last := map[string]Publication{}
	seq := map[string]int64{}
	for _, c := range v.Changes {
		if c.Sequence != seq[c.Workspace]+1 || c.After.Scope.Workspace != c.Workspace {
			return Invalid("publication journal sequence differs")
		}
		seq[c.Workspace] = c.Sequence
		events[c.Workspace]++
		key := c.Workspace + "/" + c.After.Endpoint.ID + "/" + c.After.Endpoint.Generation
		old, exists := last[key]
		if !exists {
			if c.Before != nil || c.After.State != "active" || c.After.Revision != 1 {
				return Invalid("publication missing original creation")
			}
		} else {
			if c.Before == nil || !reflect.DeepEqual(*c.Before, old) || old.State != "active" || c.After.State != "withdrawn" || !reflect.DeepEqual(immutable(old), immutable(c.After)) {
				return Invalid("publication journal retargeted immutable scope")
			}
		}
		last[key] = c.After
	}
	if len(last) != len(pubs) {
		return Invalid("publication journal/record count differs")
	}
	for k, p := range pubs {
		if !reflect.DeepEqual(last[k], p) {
			return Invalid("publication record differs from complete journal")
		}
	}
	seen := map[string]bool{}
	for _, l := range v.Links {
		c := l.Context
		if l.Schema != 1 || !Hex(c.Workspace, 16) || !Hex(c.Generation, 16) || !Hex(c.Message, 16) || !Hex(c.Digest, 32) || !Hex(c.Origin, 16) || !Hex(c.Target, 16) || c.Origin == c.Target || c.To.Kind != "agent" || !Hex(c.To.ID, 16) || !Hex(c.To.Generation, 16) || !Hex(l.LocalMessage, 16) || !Text(l.Recipient, 128) || !Hex(l.Registration, 16) || !Hex(l.ContactSHA, 32) || l.Created.IsZero() {
			return Invalid("invalid routed poll link")
		}
		p, ok := Find(v, c.Workspace, c.To)
		if !ok || p.Scope.Generation != c.Generation || p.Scope.Authority.Host != c.Target || p.Contact.ID != l.Recipient || p.Contact.Registration != l.Registration || p.ContactSHA != l.ContactSHA {
			return Invalid("routed poll link lost original publication")
		}
		if seen[l.LocalMessage] {
			return Invalid("duplicate local routed message")
		}
		seen[l.LocalMessage] = true
		links[c.Workspace]++
	}
	if len(retained) > workspace.MaxWorkspaces {
		return Invalid("too many publication workspaces")
	}
	for id, n := range retained {
		if n > MaxRetained || active[id] > MaxActive || events[id]+active[id] > MaxEvents || links[id] > MaxLinks {
			return Invalid("publication capacity including finalization slots exhausted")
		}
	}
	reserve := 0
	for _, n := range active {
		reserve += n * FinalizationReserve
	}
	if max(v.RawBytes, encodedBytes(v))+reserve > MaxBytes {
		return Invalid("publication bytes including finalization reserve exhausted")
	}
	return nil
}
func encodedBytes(v Records) int {
	n := 0
	for _, p := range v.Publications {
		b, _ := json.Marshal(p)
		n += len(b)
	}
	for _, p := range v.Changes {
		b, _ := json.Marshal(p)
		n += len(b)
	}
	for _, p := range v.Links {
		b, _ := json.Marshal(p)
		n += len(b)
	}
	return n
}
func (v Records) BudgetBytes() int {
	n := max(v.RawBytes, encodedBytes(v))
	for _, p := range v.Publications {
		if p.State == "active" {
			n += FinalizationReserve
		}
	}
	return n
}
func checkLink(ctx context.Context, tx *sql.Tx, v Records, l Link) error {
	var raw []byte
	e := tx.QueryRowContext(ctx, `SELECT substr(CAST(data AS BLOB),1,65537) FROM routing_inbox WHERE workspace=? AND message=?`, l.Context.Workspace, l.Context.Message).Scan(&raw)
	if e != nil {
		return e
	}
	var in struct {
		Workspace string `json:"workspace"`
		Envelope  struct {
			Workspace  string  `json:"workspace"`
			Generation string  `json:"generation"`
			ID         string  `json:"message_id"`
			Origin     string  `json:"origin_host"`
			Target     string  `json:"target_host"`
			From       Address `json:"origin_endpoint"`
			To         Address `json:"target_endpoint"`
			Body       string  `json:"body"`
		} `json:"envelope"`
		Receipt struct {
			State string    `json:"state"`
			At    time.Time `json:"recorded_at"`
		} `json:"receipt"`
		Received time.Time `json:"received_at"`
	}
	if len(raw) > 65536 || json.Unmarshal(raw, &in) != nil {
		return Invalid("routed link inbox malformed")
	}
	c := l.Context
	e0 := in.Envelope
	if in.Workspace != c.Workspace || e0.Workspace != c.Workspace || e0.Generation != c.Generation || e0.ID != c.Message || e0.Origin != c.Origin || e0.Target != c.Target || e0.From != c.From || e0.To != c.To || in.Receipt.State != "accepted" && in.Receipt.State != "acknowledged" || !in.Received.Equal(l.Created) {
		return Invalid("routed link differs from immutable acceptance")
	}
	var whole struct {
		Receipt struct {
			Digest string `json:"message_sha256"`
		} `json:"receipt"`
	}
	if json.Unmarshal(raw, &whole) != nil || whole.Receipt.Digest != c.Digest {
		return Invalid("routed link digest differs")
	}
	e = tx.QueryRowContext(ctx, `SELECT substr(CAST(data AS BLOB),1,65537) FROM messages WHERE id=?`, l.LocalMessage).Scan(&raw)
	if e != nil {
		return e
	}
	var msg struct {
		ID            string                     `json:"id"`
		Sender        string                     `json:"sender"`
		SenderContact *agents.Contact            `json:"sender_contact,omitempty"`
		OriginHost    string                     `json:"origin_host,omitempty"`
		ReplyTo       string                     `json:"reply_to,omitempty"`
		AgentKey      string                     `json:"agent_operation_key,omitempty"`
		Key           string                     `json:"idempotency_key,omitempty"`
		Lease         string                     `json:"lease,omitempty"`
		Audience      string                     `json:"audience"`
		Body          string                     `json:"body"`
		Created       time.Time                  `json:"created_at"`
		Routed        *Context                   `json:"routed_origin,omitempty"`
		Deliveries    map[string]agents.Delivery `json:"deliveries"`
	}
	if len(raw) > 65536 || json.Unmarshal(raw, &msg) != nil || msg.ID != l.LocalMessage || msg.Audience != l.Recipient || msg.Sender != "workspace:"+c.Origin+"/"+c.From.ID || msg.SenderContact != nil || msg.OriginHost != "" || msg.ReplyTo != "" || msg.AgentKey != "" || msg.Key != "" || msg.Lease != "" || msg.Body != e0.Body || msg.Created != l.Created || msg.Routed == nil || *msg.Routed != c || len(msg.Deliveries) != 1 {
		return Invalid("routed link lost exact local poll message")
	}
	d, ok := msg.Deliveries[l.Recipient]
	if !ok || d.Mode != "poll" || d.State != "available" || d.Registration != l.Registration {
		return Invalid("routed message changed poll recipient")
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_inbox WHERE recipient=? AND registration=? AND message=?`, l.Recipient, l.Registration, l.LocalMessage).Scan(&count); e != nil {
		return e
	}
	if count != 1 {
		return Invalid("routed message lacks exact local inbox entry")
	}
	var receiptRaw []byte
	if e = tx.QueryRowContext(ctx, `SELECT substr(CAST(receipt AS BLOB),1,8193) FROM agent_inbox WHERE recipient=? AND registration=? AND message=?`, l.Recipient, l.Registration, l.LocalMessage).Scan(&receiptRaw); e != nil {
		return e
	}
	var localReceipt agents.Receipt
	if len(receiptRaw) > 8192 || workspace.Decode(receiptRaw, &localReceipt) != nil {
		return Invalid("routed local receipt malformed")
	}
	if in.Receipt.State == "acknowledged" && (localReceipt.Acknowledged == nil || localReceipt.Received == nil || *localReceipt.Acknowledged != in.Receipt.At || localReceipt.Text != "" || localReceipt.ReplyID != "") {
		return Invalid("routing acknowledgement lacks exact local poll acknowledgement")
	}

	return nil
}

// Only closed internal column names reach this helper. SQLite TEXT substr
// counts characters and hides embedded-NUL suffixes, so both type and complete
// byte length must pass before a bounded raw byte slice is compared to JSON.
func rawTextKey(column string, limit int) string {
	return fmt.Sprintf("CASE WHEN typeof(%s)='text' AND length(CAST(%s AS BLOB)) BETWEEN 1 AND %d THEN substr(CAST(%s AS BLOB),1,%d) ELSE NULL END", column, column, limit, column, limit+1)
}
