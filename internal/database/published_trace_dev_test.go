//go:build arm_devtrace

package database_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/agents"
	"github.com/inferal-oss/arm/internal/manager"
	"github.com/inferal-oss/arm/internal/published"
	"github.com/inferal-oss/arm/internal/routing"
	"github.com/inferal-oss/arm/internal/workspace"
)

type publishedFormalState struct {
	Schema          string `json:"schema"`
	Action          string `json:"action"`
	Contact         int    `json:"contact"`
	Pub1            string `json:"pub1"`
	Pub2            string `json:"pub2"`
	Bound1          int    `json:"bound1"`
	Bound2          int    `json:"bound2"`
	PubRows         int    `json:"pub_rows"`
	JournalRows     int    `json:"journal_rows"`
	Source          string `json:"source"`
	SourceLive      bool   `json:"source_live"`
	Dispatches      int    `json:"dispatches"`
	Result1         string `json:"result1"`
	Result2         string `json:"result2"`
	Generation1     int    `json:"generation1"`
	Generation2     int    `json:"generation2"`
	Message1        bool   `json:"message1"`
	Inbox1          bool   `json:"inbox1"`
	Link1           bool   `json:"link1"`
	Message2        bool   `json:"message2"`
	Inbox2          bool   `json:"inbox2"`
	Link2           bool   `json:"link2"`
	LocalAck1       bool   `json:"local_ack1"`
	RoutedAck1      bool   `json:"routed_ack1"`
	LocalAck2       bool   `json:"local_ack2"`
	RoutedAck2      bool   `json:"routed_ack2"`
	ReplyFrom       int    `json:"reply_from"`
	ReplyTo         int    `json:"reply_to"`
	NativeCalls     int    `json:"native_calls"`
	ImplicitReplies int    `json:"implicit_replies"`
	BudgetOK        bool   `json:"budget_ok"`
	PollOnly        bool   `json:"poll_only"`
	IdentitiesOK    bool   `json:"identities_ok"`
}
type publishedFormalBytes struct {
	PublicationRaw     int `json:"publication_raw"`
	PublicationReserve int `json:"publication_reserve"`
	RoutingRaw         int `json:"routing_raw"`
	RoutingReserve     int `json:"routing_reserve"`
	FutureEvents       int `json:"future_events"`
}
type publishedFormalFixture struct {
	source, target                                            *manager.Manager
	sourceHome, targetHome, workspace, sourceHost, targetHost string
	identity                                                  workspace.Identity
	contact, sourceContact                                    agents.Contact
	contactDigests                                            [2]string
	sender                                                    published.Publication
	native                                                    *agents.NativeDeliveryTrace
	out                                                       [2]routing.Outbox
}

func publishedFormalTx(t *testing.T, home string, fn func(*sql.Tx)) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(home, "state.db")}
	db, e := sql.Open("sqlite", u.String()+"?mode=ro")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, e := db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	fn(tx)
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
func publishedFormalRows(t *testing.T, tx *sql.Tx, table string, fn func([]byte)) (int, int) {
	t.Helper()
	var present int
	if e := tx.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&present); e != nil {
		t.Fatal(e)
	}
	if present == 0 {
		return 0, 0
	}
	if e := publishedFormalKeys(tx, table); e != nil {
		t.Fatal(e)
	}
	rows, e := tx.Query("SELECT substr(CAST(data AS BLOB),1,65537),length(CAST(data AS BLOB)) FROM " + table + " LIMIT 1025")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	n, bytes := 0, 0
	for rows.Next() {
		n++
		var raw []byte
		var size int
		if e = rows.Scan(&raw, &size); e != nil {
			t.Fatal(e)
		}
		if n > 1024 || size > 65536 || len(raw) != size {
			t.Fatal("bounded raw formal row required")
		}
		bytes += size
		if fn != nil {
			fn(raw)
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return n, bytes
}

// Raw keys are not normalized through SQLite TEXT substr: embedded NULs,
// alternate storage classes and retargeted keys must never become valid
// symbolic identities. Only this closed three-table catalog is interpreted.
func publishedFormalKeys(tx *sql.Tx, table string) error {
	columns := map[string][]string{
		"published_agents":        {"workspace", "endpoint", "generation", "key"},
		"published_agent_changes": {"workspace", "sequence"},
		"routed_agent_links":      {"workspace", "message", "local_message"},
	}[table]
	if columns == nil {
		return nil
	}
	fields := []string{"substr(CAST(data AS BLOB),1,65537)", "length(CAST(data AS BLOB))"}
	for _, c := range columns {
		fields = append(fields, "typeof("+c+")", "length(CAST("+c+" AS BLOB))", "substr(CAST("+c+" AS BLOB),1,129)")
	}
	rows, e := tx.Query("SELECT " + strings.Join(fields, ",") + " FROM " + table + " LIMIT 1025")
	if e != nil {
		return e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > 1024 {
			return fmt.Errorf("formal key roster exceeds bound")
		}
		var raw []byte
		var size int
		types := make([]string, len(columns))
		sizes := make([]int, len(columns))
		keys := make([][]byte, len(columns))
		args := []any{&raw, &size}
		for i := range columns {
			args = append(args, &types[i], &sizes[i], &keys[i])
		}
		if e = rows.Scan(args...); e != nil {
			return e
		}
		if size > 65536 || len(raw) != size {
			return fmt.Errorf("formal keyed row exceeds bound")
		}
		var expected []string
		switch table {
		case "published_agents":
			var v published.Publication
			if e = publishedTraceJSON(raw, &v); e != nil {
				return e
			}
			expected = []string{v.Scope.Workspace, v.Endpoint.ID, v.Endpoint.Generation, v.Request.Key}
		case "published_agent_changes":
			var v published.Change
			if e = publishedTraceJSON(raw, &v); e != nil {
				return e
			}
			expected = []string{v.Workspace, fmt.Sprint(v.Sequence)}
		case "routed_agent_links":
			var v published.Link
			if e = publishedTraceJSON(raw, &v); e != nil {
				return e
			}
			expected = []string{v.Context.Workspace, v.Context.Message, v.LocalMessage}
		}
		for i, c := range columns {
			wantType := "text"
			limit := 32
			if c == "sequence" {
				wantType = "integer"
				limit = 20
			}
			if c == "key" {
				limit = 128
			}
			if types[i] != wantType || sizes[i] > limit || sizes[i] != len(keys[i]) || string(keys[i]) != expected[i] {
				return fmt.Errorf("raw formal %s.%s identity differs", table, c)
			}
		}
	}
	return rows.Err()
}

// Observations are read from committed SQL and the actual Manager lifecycle.
// They do not call producer validation: deliberate partial-row mutations must
// remain observable so imported Next can reject their actual broken states.
func (f *publishedFormalFixture) observe(t *testing.T, action string) (publishedFormalState, publishedFormalBytes) {
	t.Helper()
	s := publishedFormalState{Schema: "arm.published-agents/v1", Action: action, Pub1: "absent", Pub2: "absent", Source: "absent", Result1: "absent", Result2: "absent", PollOnly: true, IdentitiesOK: true, BudgetOK: true}
	facts := publishedFormalBytes{}
	_, liveErr := f.source.Status()
	s.SourceLive = liveErr == nil
	s.NativeCalls = int(f.native.Count())
	var pubs [2]published.Publication
	inboxes := [2]*routing.Inbox{}
	links := [2]*published.Link{}
	messages := map[string]manager.Message{}
	active := 0
	publishedFormalTx(t, f.targetHome, func(tx *sql.Tx) {
		var host string
		if e := tx.QueryRow(`SELECT value FROM metadata WHERE key='host'`).Scan(&host); e != nil || host != f.targetHost {
			t.Fatal("formal target authority differs", e)
		}
		var parentRaw []byte
		if e := tx.QueryRow(`SELECT data FROM workspace_instances WHERE id=?`, f.workspace).Scan(&parentRaw); e != nil {
			t.Fatal(e)
		}
		var parent workspace.Instance
		if e := workspace.Decode(parentRaw, &parent); e != nil || parent.Self.Host != f.targetHost {
			t.Fatal("formal workspace authority differs", e)
		}
		var currentRaw []byte
		if e := tx.QueryRow(`SELECT data FROM agents WHERE id=?`, f.contact.ID).Scan(&currentRaw); e != nil {
			t.Fatal(e)
		}
		var current agents.Contact
		if e := workspace.Decode(currentRaw, &current); e != nil {
			t.Fatal(e)
		}
		digest := published.Hash(current)
		switch digest {
		case f.contactDigests[0]:
			s.Contact = 0
		case f.contactDigests[1]:
			s.Contact = 1
		default:
			t.Fatal("unknown contact digest cannot be normalized")
		}
		n, b := publishedFormalRows(t, tx, "published_agents", func(raw []byte) {
			var p published.Publication
			if e := workspace.Decode(raw, &p); e != nil {
				t.Fatal(e)
			}
			if p.Scope.Workspace != f.workspace || p.Contact.ID != f.contact.ID {
				t.Fatal("foreign publication normalization refused")
			}
			i := 0
			switch p.Request.Key {
			case "publish1":
				i = 0
			case "publish2":
				i = 1
			default:
				t.Fatal("unknown immutable publication key")
			}
			if pubs[i].Schema != 0 {
				t.Fatal("duplicate publication generation")
			}
			pubs[i] = p
			if p.State == "active" {
				active++
			}
			if len(raw) > published.MaxRecord {
				s.BudgetOK = false
			}
		})
		s.PubRows = n
		facts.PublicationRaw += b
		n, b = publishedFormalRows(t, tx, "published_agent_changes", func(raw []byte) {
			var c published.Change
			if e := workspace.Decode(raw, &c); e != nil || c.Workspace != f.workspace {
				t.Fatal("foreign journal normalization", e)
			}
			if len(raw) > published.MaxChangeRecord {
				s.BudgetOK = false
			}
		})
		s.JournalRows = n
		facts.PublicationRaw += b
		n, b = publishedFormalRows(t, tx, "routed_agent_links", func(raw []byte) {
			var l published.Link
			if e := workspace.Decode(raw, &l); e != nil {
				t.Fatal(e)
			}
			if l.Context.Workspace != f.workspace {
				t.Fatal("foreign routed link")
			}
			found := -1
			for i, p := range pubs {
				if p.Schema != 0 && l.Context.To == p.Endpoint {
					found = i
				}
			}
			if found < 0 {
				t.Fatal("unknown generation cannot be normalized")
			}
			if links[found] != nil {
				t.Fatal("duplicate link in finite normalization")
			}
			copy := l
			links[found] = &copy
		})
		facts.PublicationRaw += b
		if n > published.MaxLinks {
			s.BudgetOK = false
		}
		_, b = publishedFormalRows(t, tx, "routing_inbox", func(raw []byte) {
			var in routing.Inbox
			if e := workspace.Decode(raw, &in); e != nil {
				t.Fatal(e)
			}
			if in.Workspace != f.workspace || in.Envelope.Origin != f.sourceHost {
				t.Fatal("wrong original host normalization refused")
			}
			i := 0
			switch in.Envelope.Key {
			case "one":
				i = 0
			case "two":
				i = 1
			default:
				t.Fatal("unknown original key")
			}
			copy := in
			inboxes[i] = &copy
			if in.Envelope.From != routing.Endpoint(f.sender.Endpoint) || in.Envelope.Target != f.targetHost || pubs[i].Schema == 0 || in.Envelope.To != routing.Endpoint(pubs[i].Endpoint) {
				s.IdentitiesOK = false
			}
			if in.Receipt.State == "accepted" {
				facts.RoutingReserve += 4096
				facts.FutureEvents++
			}
		})
		facts.RoutingRaw += b
		_, b = publishedFormalRows(t, tx, "routing_outbox", func(raw []byte) {
			var o routing.Outbox
			if e := workspace.Decode(raw, &o); e != nil {
				t.Fatal(e)
			}
			if o.Workspace != f.workspace || o.Intent.Key != "reply" {
				s.ImplicitReplies++
				return
			}
			for i, p := range pubs {
				if p.Schema != 0 && o.Envelope.From == routing.Endpoint(p.Endpoint) {
					s.ReplyFrom = i + 1
				}
			}
			if o.Envelope.Target == f.sourceHost && o.Envelope.To == routing.Endpoint(f.sender.Endpoint) {
				s.ReplyTo = 1
			} else {
				s.IdentitiesOK = false
			}
			if o.Phase != "completed" && o.Phase != "cancelled" {
				facts.RoutingReserve += 16384
			}
			switch o.Phase {
			case "prepared":
				facts.FutureEvents += 3
			case "dispatching":
				facts.FutureEvents += 2
			case "unknown":
				facts.FutureEvents++
			}
			if s.ReplyFrom == 0 || o.Intent.ReplyTo == nil || inboxes[s.ReplyFrom-1] == nil || *o.Intent.ReplyTo != inboxes[s.ReplyFrom-1].Envelope.Reference() {
				s.IdentitiesOK = false
			}
		})
		facts.RoutingRaw += b
		_, b = publishedFormalRows(t, tx, "routing_roots", func(raw []byte) {
			var r routing.Root
			if e := workspace.Decode(raw, &r); e != nil {
				t.Fatal(e)
			}
			if r.Workspace != f.workspace {
				t.Fatal("foreign routing authority")
			}
			if r.State == "active" {
				facts.RoutingReserve += 4096
				facts.FutureEvents++
			}
		})
		facts.RoutingRaw += b
		n, b = publishedFormalRows(t, tx, "routing_events", nil)
		facts.RoutingRaw += b
		facts.FutureEvents += n
		var requestBytes int
		if n > 0 {
			if e := tx.QueryRow(`SELECT COALESCE(SUM(length(CAST(request AS BLOB))),0) FROM routing_outbox`).Scan(&requestBytes); e != nil {
				t.Fatal(e)
			}
			facts.RoutingRaw += requestBytes
		}
		n, _ = publishedFormalRows(t, tx, "messages", func(raw []byte) {
			var m manager.Message
			if e := publishedTraceJSON(raw, &m); e != nil {
				t.Fatal(e)
			}
			messages[m.ID] = m
		})
		linkedMessages := 0
		for i := 0; i < 2; i++ {
			if pubs[i].Schema != 0 {
				bound := -1
				switch pubs[i].ContactSHA {
				case f.contactDigests[0]:
					bound = 0
				case f.contactDigests[1]:
					bound = 1
				default:
					t.Fatal("unknown original bound digest")
				}
				if i == 0 {
					s.Pub1 = pubs[i].State
					s.Bound1 = bound
				} else {
					s.Pub2 = pubs[i].State
					s.Bound2 = bound
				}
			}
			in, l := inboxes[i], links[i]
			result, generation := "absent", 0
			message, inbox, link, ack, routedAck := false, false, false, false, false
			if in != nil {
				result = in.Receipt.State
				if result == "acknowledged" {
					result = "accepted"
					routedAck = true
				}
				generation = i + 1
			}
			if l != nil {
				link = true
				msg, exists := messages[l.LocalMessage]
				message = exists
				if exists {
					linkedMessages++
					expected := published.Context{Workspace: f.workspace, Generation: parent.Generation, Message: in.Envelope.ID, Digest: in.Envelope.Digest(), Origin: f.sourceHost, Target: f.targetHost, From: published.Address(f.sender.Endpoint), To: pubs[i].Endpoint}
					if msg.RoutedOrigin == nil || *msg.RoutedOrigin != expected || l.Context != expected || msg.Sender != "workspace:"+f.sourceHost+"/"+f.sender.Endpoint.ID || msg.Audience != f.contact.ID || msg.Body != in.Envelope.Body {
						s.IdentitiesOK = false
					}
					d, ok := msg.Deliveries[f.contact.ID]
					if !ok || len(msg.Deliveries) != 1 || d.Mode != "poll" || d.State != "available" {
						s.PollOnly = false
					}
				}
				var receiptRaw []byte
				e := tx.QueryRow(`SELECT receipt FROM agent_inbox WHERE message=? AND recipient=? AND registration=?`, l.LocalMessage, f.contact.ID, f.contact.Registration).Scan(&receiptRaw)
				if e == nil {
					inbox = true
					var r agents.Receipt
					if e = workspace.Decode(receiptRaw, &r); e != nil {
						t.Fatal(e)
					}
					ack = r.Acknowledged != nil
				} else if e != sql.ErrNoRows {
					t.Fatal(e)
				}
			}
			if i == 0 {
				s.Result1 = result
				s.Generation1 = generation
				s.Message1 = message
				s.Inbox1 = inbox
				s.Link1 = link
				s.LocalAck1 = ack
				s.RoutedAck1 = routedAck
			} else {
				s.Result2 = result
				s.Generation2 = generation
				s.Message2 = message
				s.Inbox2 = inbox
				s.Link2 = link
				s.LocalAck2 = ack
				s.RoutedAck2 = routedAck
			}
		}
		s.ImplicitReplies += n - linkedMessages
		facts.PublicationReserve = active * published.FinalizationReserve
	})
	publishedFormalTx(t, f.sourceHome, func(tx *sql.Tx) {
		v, e := routing.Read(t.Context(), tx)
		if e != nil {
			t.Fatal(e)
		}
		var host string
		if e = tx.QueryRow(`SELECT value FROM metadata WHERE key='host'`).Scan(&host); e != nil || host != f.sourceHost {
			t.Fatal("source authority normalization refused", e)
		}
		o, found, e := routing.FindOutbox(v, f.workspace, "one")
		if e != nil {
			t.Fatal(e)
		}
		if found {
			s.Source = o.Phase
			for _, c := range v.Events {
				if c.Message == o.Envelope.ID && c.Kind == "outbound_dispatching" {
					s.Dispatches++
				}
			}
		}
	})
	s.BudgetOK = s.BudgetOK && facts.PublicationRaw+facts.PublicationReserve <= published.MaxBytes && facts.PublicationRaw+facts.PublicationReserve+facts.RoutingRaw+facts.RoutingReserve <= routing.MaxLedgerBytes && facts.FutureEvents <= routing.MaxEvents
	return s, facts
}

var _ = json.Marshal
var _ = reflect.DeepEqual

func newPublishedFormalFixture(t *testing.T) *publishedFormalFixture {
	t.Helper()
	ctx := t.Context()
	home := publishedFormalHome(t)
	id := ""
	d, e := manager.Open(home, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	statusSource, err := d.Status()
	if err != nil {
		t.Fatal(err)
	}
	nativeSource, err := mesh.InitPrepared(home, statusSource.Host, "formal-source", "127.0.0.1:23458", "127.0.0.1:23458")
	if err != nil {
		t.Fatal(err)
	}
	nativeSource.Close()
	create, err := d.WorkspacePrepareCreate(ctx, workspace.CreateRequest{Key: "formal-create", Name: "published formal exact scope", Policy: workspace.DefaultPolicy(), Reason: "private actual producer fixture"})
	if err != nil {
		t.Fatal(err)
	}
	create, err = d.WorkspaceDecide(ctx, create.Workspace, create.Key, create.Revision, create.ReviewHash, true)
	if err != nil {
		t.Fatal(err)
	}
	id = create.Workspace
	v, e := d.WorkspaceRead(ctx)
	if e != nil {
		t.Fatal(e)
	}
	root := v.Instances[0]
	memberHome := publishedFormalHome(t)
	m, e := manager.Open(memberHome, model.Resources{CPU: 2, Disk: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	status, e := m.Status()
	if e != nil {
		t.Fatal(e)
	}
	identity, e := mesh.InitPrepared(memberHome, status.Host, "routing-proof-fixture", "127.0.0.1:23459", "127.0.0.1:23459")
	if e != nil {
		t.Fatal(e)
	}
	identity.Close()
	join, e := m.WorkspacePrepareJoin(ctx, workspace.JoinRequest{Key: "routing-join", Workspace: id, Generation: root.Generation, Name: root.Name, Directory: root.Directory, Policy: root.Policy, PolicyRevision: root.PolicyRevision, Introducer: root.Self.Host, Reason: "explicit routing proof candidate"})
	if e != nil {
		t.Fatal(e)
	}
	join, e = m.WorkspaceDecide(ctx, id, join.Key, join.Revision, join.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	principal := workspace.Principal{Workspace: id, Generation: root.Generation, Host: root.Self.Host, MembershipGeneration: 1, Fingerprint: root.Self.Fingerprint}
	admit, e := d.WorkspacePrepareAdmission(ctx, principal, workspace.AdmissionRequest{Key: "routing-admit", Candidate: join.Review.Source, ConsentHash: join.ReviewHash, HostInbox: true, PolicyRevision: root.PolicyRevision, Reason: "explicit directory message recipient"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = d.WorkspaceActivate(ctx, principal, admit.Key, proofWorkspaceTransport(func(ctx context.Context, c workspace.Challenge) (workspace.Proof, error) {
		return m.WorkspaceProveJoin(ctx, root.Directory, c)
	})); e != nil {
		t.Fatal(e)
	}
	{
		ticket, e := m.WorkspaceBeginRefresh(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		snapshot, e := d.WorkspaceIssue(ctx, ticket)
		if e != nil {
			t.Fatal(e)
		}
		if e = m.WorkspaceAccept(ctx, root.Directory, snapshot); e != nil {
			t.Fatal(e)
		}
		current, _, e := m.WorkspaceLookup(ctx, id, join.Key)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = m.WorkspaceFinalize(ctx, id, join.Key, current.Revision); e != nil {
			t.Fatal(e)
		}
	}

	f := &publishedFormalFixture{source: d, target: m, sourceHome: home, targetHome: memberHome, workspace: id, sourceHost: root.Self.Host, targetHost: status.Host, identity: root.Self}
	t.Cleanup(func() {
		if f.source != nil {
			if _, e := f.source.Status(); e == nil {
				f.source.Close()
			}
		}
		f.target.Close()
		if f.native != nil {
			f.native.Close()
		}
	})
	f.sourceContact, err = d.RegisterContact(agents.Contact{ID: "origin", Adapter: "generic", Delivery: "poll", Session: uuid.NewString(), CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	f.contact, err = m.RegisterContact(agents.Contact{ID: "recipient", Adapter: "codex", Delivery: "native", Session: uuid.NewString(), CWD: t.TempDir(), ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	f.contactDigests[0] = published.Hash(f.contact)
	replacement := f.contact
	replacement.Idle = true
	f.contactDigests[1] = published.Hash(replacement)
	f.native, err = agents.BeginNativeDeliveryTrace(f.contact)
	if err != nil {
		t.Fatal(err)
	}
	q := published.Request{Workspace: id, ID: f.sourceContact.ID, Session: f.sourceContact.Session, Registration: f.sourceContact.Registration, Label: "Original source", Delivery: "poll", Key: "source-publication", Reason: "fixed external admitted sender"}
	plan, err := d.AgentPublicationPlan(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	f.sender, err = d.AgentPublicationCreate(ctx, q, plan.ReviewSHA)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *publishedFormalFixture) publication(t *testing.T, index int) published.Publication {
	t.Helper()
	v, e := f.target.AgentPublicationRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range v.Publications {
		if p.Request.Key == fmt.Sprintf("publish%d", index) {
			return p
		}
	}
	t.Fatal("missing exact publication", index)
	return published.Publication{}
}
func (f *publishedFormalFixture) localMessage(t *testing.T, index int) string {
	t.Helper()
	v, e := f.target.AgentPublicationRead(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	for _, l := range v.Links {
		if l.Context.Message == f.out[index-1].Envelope.ID {
			return l.LocalMessage
		}
	}
	t.Fatal("missing original local message")
	return ""
}
func (f *publishedFormalFixture) prepare(t *testing.T, index int) {
	t.Helper()
	p := f.publication(t, index)
	q := routing.Intent{Workspace: f.workspace, Key: map[int]string{1: "one", 2: "two"}[index], Target: f.targetHost, To: routing.Endpoint(p.Endpoint), Body: fmt.Sprintf("original message %d", index), TTL: time.Hour}
	out, created, e := f.source.WorkspaceAgentPrepare(t.Context(), q, f.sourceContact.ID, f.sourceContact.Session, f.sourceContact.Registration)
	if e != nil || !created {
		t.Fatal(out, created, e)
	}
	f.out[index-1] = out
}
func (f *publishedFormalFixture) step(t *testing.T, action string) {
	t.Helper()
	ctx := t.Context()
	var e error
	switch action {
	case "publish1", "publish2":
		index := 1
		if action == "publish2" {
			index = 2
		}
		contacts, err := f.target.Contacts()
		if err != nil {
			t.Fatal(err)
		}
		current := contacts[0]
		q := published.Request{Workspace: f.workspace, ID: current.ID, Session: current.Session, Registration: current.Registration, Label: strings.Repeat("<", 128), Delivery: "poll", Key: fmt.Sprintf("publish%d", index), Reason: strings.Repeat("<", 512)}
		plan, err := f.target.AgentPublicationPlan(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		_, e = f.target.AgentPublicationCreate(ctx, q, plan.ReviewSHA)
	case "withdraw1", "withdraw2":
		index := 1
		if action == "withdraw2" {
			index = 2
		}
		p := f.publication(t, index)
		_, e = f.target.AgentPublicationWithdraw(ctx, f.workspace, p.Endpoint, published.Withdrawal{Key: strings.Repeat("<", 128), Reason: strings.Repeat("<", 512), Revision: 1})
	case "change":
		next := f.contact
		next.Idle = true
		current, err := f.target.RegisterContact(next)
		e = err
		if e == nil && current.Registration != f.contact.Registration {
			t.Fatal("policy-only change replaced registration")
		}
	case "prepare":
		f.prepare(t, 1)
	case "begin":
		f.out[0], e = f.source.WorkspaceRoutingBegin(ctx, f.out[0])
	case "accept1", "stale1":
		_, e = f.target.WorkspaceRoutingAccept(ctx, f.identity, f.out[0].Envelope)
	case "accept2":
		f.prepare(t, 2)
		f.out[1], e = f.source.WorkspaceRoutingBegin(ctx, f.out[1])
		if e == nil {
			var in routing.Inbox
			in, e = f.target.WorkspaceRoutingAccept(ctx, f.identity, f.out[1].Envelope)
			if e == nil {
				f.out[1], e = f.source.WorkspaceRoutingComplete(ctx, f.out[1], in.Receipt)
			}
		}
	case "unknown":
		f.out[0], e = f.source.WorkspaceRoutingUnknown(ctx, f.out[0])
	case "crash":
		e = f.source.Close()
	case "restart":
		f.source, e = manager.Open(f.sourceHome, model.Resources{CPU: 2, Disk: 1 << 20})
	case "complete":
		var receipt *routing.Receipt
		receipt, e = f.target.WorkspaceRoutingLookup(ctx, f.identity, f.out[0].Envelope)
		if e == nil && receipt == nil {
			t.Fatal("completion lookup fabricated absence")
		}
		if e == nil {
			f.out[0], e = f.source.WorkspaceRoutingComplete(ctx, f.out[0], *receipt)
		}
	case "retry":
		v, err := f.source.WorkspaceRoutingRead(ctx)
		e = err
		if e == nil {
			old, found, err := routing.FindOutbox(v, f.workspace, "one")
			e = err
			if !found || old.Envelope.Digest() != f.out[0].Envelope.Digest() {
				t.Fatal("retry lost original key")
			}
		}
	case "duplicate1", "duplicate2":
		index := 0
		if action == "duplicate2" {
			index = 1
		}
		_, e = f.target.WorkspaceRoutingAccept(ctx, f.identity, f.out[index].Envelope)
	case "ack1", "ack2":
		index := 1
		if action == "ack2" {
			index = 2
		}
		_, e = f.target.AgentReceipt(manager.ReceiptRequest{ID: f.contact.ID, Session: f.contact.Session, Registration: f.contact.Registration, Message: f.localMessage(t, index), Acknowledge: true})
	case "reply1", "reply2", "reply_refused1":
		index := 1
		if action == "reply2" {
			index = 2
		}
		var q routing.Intent
		q, e = f.target.WorkspaceAgentReplyIntent(ctx, f.workspace, f.localMessage(t, index), f.contact.ID, f.contact.Session, f.contact.Registration, "reply", "original explicit reply", time.Hour)
		if action == "reply_refused1" {
			if e == nil {
				t.Fatal("replacement silently selected original reply recipient")
			}
			return
		}
		if e == nil {
			var created bool
			_, created, e = f.target.WorkspaceAgentPrepare(ctx, q, f.contact.ID, f.contact.Session, f.contact.Registration)
			if e == nil && !created {
				t.Fatal("reply key unexpectedly reused")
			}
		}
	case "native_refused":
		q := published.Request{Workspace: f.workspace, ID: f.contact.ID, Session: f.contact.Session, Registration: f.contact.Registration, Label: "Unsupported native", Delivery: "native", Key: "native-refused", Reason: "must not invoke any native turn"}
		_, e = f.target.AgentPublicationPlan(ctx, q)
		if e == nil {
			t.Fatal("native delivery was advertised")
		}
		return
	case "discover":
		_, e = f.target.AgentEndpointPage(ctx, f.identity, published.Query{Workspace: f.workspace, Nonce: local.ID(), Limit: 16})
	default:
		t.Fatal("unknown formal action", action)
	}
	if e != nil {
		t.Fatal(action, e)
	}
}

type publishedFormalCase struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	Output string `json:"output"`
	Facts  string `json:"facts"`
}

func TestPublishedFormalTLCWitnesses(t *testing.T) {
	path := os.Getenv("ARM_PUBLISHED_FORMAL_WITNESSES")
	if path == "" {
		t.Fatal("required generated TLC manifest missing")
	}
	var cases []publishedFormalCase
	if e := publishedFormalReadJSON(path, &cases); e != nil || len(cases) == 0 || len(cases) > 32 {
		t.Fatal("complete bounded witness manifest required", e)
	}
	for _, c := range cases {
		t.Run(c.Target, func(t *testing.T) {
			var expected []publishedFormalState
			if e := publishedFormalReadJSON(c.Path, &expected); e != nil || len(expected) < 2 || len(expected) > 128 {
				t.Fatal(e)
			}
			f := newPublishedFormalFixture(t)
			observed := []publishedFormalState{}
			facts := []publishedFormalBytes{}
			for i, want := range expected {
				if i > 0 {
					if (c.Target == "late_accept" || c.Target == "late_superseded") && (want.Action == "accept1" || want.Action == "stale1") {
						before, _ := f.observe(t, want.Action)
						if before.Source != "unknown" || before.SourceLive || before.Result1 != "absent" {
							t.Fatal("late destination transaction requires independently committed source unknown and stopped source, without a prior destination result")
						}
					}
					f.step(t, want.Action)
				}
				actual, bytes := f.observe(t, want.Action)
				if !reflect.DeepEqual(actual, want) {
					a, _ := json.Marshal(actual)
					w, _ := json.Marshal(want)
					t.Fatalf("actual SQL/runtime mismatch at %d\nactual %s\nTLC    %s", i, a, w)
				}
				observed = append(observed, actual)
				facts = append(facts, bytes)
			}
			if c.Target == "late_accept" || c.Target == "late_superseded" {
				last := observed[len(observed)-1]
				if last.Source != "unknown" || last.SourceLive {
					t.Fatal("late receipt must not resolve or restart the independent source")
				}
				for _, action := range []string{"restart", "complete"} {
					f.step(t, action)
					actual, bytes := f.observe(t, action)
					observed = append(observed, actual)
					facts = append(facts, bytes)
				}
				if observed[len(observed)-1].Source != "completed" || observed[len(observed)-1].Dispatches != 1 {
					t.Fatal("late receipt reconciliation redispatched or failed")
				}
			}
			if e := publishedFormalWriteJSON(c.Output, observed); e != nil {
				t.Fatal(e)
			}
			if e := publishedFormalWriteJSON(c.Facts, facts); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func publishedTraceJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if _, e := publishedFormalJSONValue(d); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing SQL trace JSON")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

func publishedFormalHome(t *testing.T) string {
	t.Helper()
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(home, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(home, "daemon.lock"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	return home
}

func TestPublishedFormalObservationNegatives(t *testing.T) {
	output := os.Getenv("ARM_PUBLISHED_FORMAL_NEGATIVES")
	if output == "" {
		t.Fatal("required negative output path missing")
	}
	observed := map[string][]publishedFormalState{}
	for _, name := range []string{"actual-partial-inbox", "actual-native-entry", "actual-native-promotion", "actual-unwithdrawn-contact", "actual-erased-local-ack", "actual-retargeted-reply", "actual-escaped-row-overflow"} {
		t.Run(name, func(t *testing.T) {
			f := newPublishedFormalFixture(t)
			states := []publishedFormalState{}
			initial, _ := f.observe(t, "init")
			states = append(states, initial)
			actions := []string{"publish1", "prepare", "begin", "accept1"}
			if name == "actual-unwithdrawn-contact" {
				actions = append(actions, "change")
			}
			if name == "actual-erased-local-ack" {
				actions = append(actions, "ack1")
			}
			if name == "actual-retargeted-reply" {
				actions = append(actions, "change", "publish2", "accept2", "reply2")
			}
			if name == "actual-escaped-row-overflow" {
				actions = []string{"publish1"}
			}
			for i, a := range actions {
				f.step(t, a)
				if i == len(actions)-1 {
					switch name {
					case "actual-partial-inbox":
						edit(t, f.targetHome, `DELETE FROM agent_inbox WHERE recipient='recipient'`)
					case "actual-native-entry":
						_ = agents.Deliver(t.Context(), f.contact, "private invalid-session native-entry observation")
					case "actual-native-promotion":
						edit(t, f.targetHome, `UPDATE messages SET data=json_set(data,'$.deliveries.recipient.mode','native')`)
					case "actual-unwithdrawn-contact":
						edit(t, f.targetHome, `UPDATE published_agents SET data=json_set(data,'$.state','active') WHERE key='publish1'`)
					case "actual-erased-local-ack":
						edit(t, f.targetHome, `UPDATE agent_inbox SET receipt='{}' WHERE recipient='recipient'`)
					case "actual-retargeted-reply":
						edit(t, f.targetHome, `UPDATE routing_outbox SET data=json_set(data,'$.envelope.target_endpoint.id',?)`, local.ID())
					case "actual-escaped-row-overflow":
						p := f.publication(t, 1)
						p.Request.Label = strings.Repeat("<", 2100)
						raw, e := json.Marshal(p)
						if e != nil {
							t.Fatal(e)
						}
						edit(t, f.targetHome, `UPDATE published_agents SET data=? WHERE key='publish1'`, string(raw))
					}
				}
				s, _ := f.observe(t, a)
				states = append(states, s)
			}
			observed[name] = states
		})
	}
	if e := publishedFormalWriteJSON(output, observed); e != nil {
		t.Fatal(e)
	}
}
