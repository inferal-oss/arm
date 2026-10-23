package published

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/agents"
	"github.com/yrashk/arm/internal/workspace"
)

var Tables = []string{"published_agents", "published_agent_changes", "routed_agent_links"}
var DDL = []string{
	`CREATE TABLE published_agents(workspace TEXT NOT NULL,endpoint TEXT NOT NULL,generation TEXT NOT NULL,key TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,endpoint,generation),UNIQUE(workspace,key))`,
	`CREATE TABLE published_agent_changes(workspace TEXT NOT NULL,sequence INTEGER NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,sequence))`,
	`CREATE TABLE routed_agent_links(workspace TEXT NOT NULL,message TEXT NOT NULL,local_message TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(workspace,message),UNIQUE(local_message))`,
}

func schema(ctx context.Context, tx *sql.Tx, create bool) (bool, error) {
	rows, e := tx.QueryContext(ctx, `SELECT type,name,sql FROM sqlite_schema WHERE name LIKE 'published_%' OR tbl_name LIKE 'published_%' OR name LIKE 'routed_agent_%' OR tbl_name LIKE 'routed_agent_%' ORDER BY name LIMIT 9`)
	if e != nil {
		return false, e
	}
	count := 0
	indexes := map[string]bool{}
	seen := map[string]string{}
	for rows.Next() {
		count++
		var typ, name string
		var ddl sql.NullString
		if e = rows.Scan(&typ, &name, &ddl); e != nil {
			rows.Close()
			return false, e
		}
		if typ == "table" && ddl.Valid {
			seen[name] = ddl.String
		} else if typ == "index" && !ddl.Valid {
			indexes[name] = true
		} else {
			rows.Close()
			return false, Invalid("unknown published-agent schema object")
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	if count > 8 {
		return false, Invalid("published-agent catalog exceeds closed bound")
	}
	var marker string
	e = tx.QueryRowContext(ctx, `SELECT value FROM metadata WHERE key='routed_agents_schema'`).Scan(&marker)
	if e == sql.ErrNoRows && len(seen) == 0 {
		if count != 0 {
			return false, Invalid("partial publication indexes without tables")
		}
		if !create {
			return false, nil
		}
		for _, ddl := range DDL {
			if _, e = tx.ExecContext(ctx, ddl); e != nil {
				return false, e
			}
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO metadata(key,value) VALUES('routed_agents_schema','1')`)
		return e == nil, e
	}
	if e != nil || marker != "1" || len(seen) != len(Tables) {
		return false, Invalid("incomplete publication schema/marker")
	}
	for _, name := range []string{"sqlite_autoindex_published_agents_1", "sqlite_autoindex_published_agents_2", "sqlite_autoindex_published_agent_changes_1", "sqlite_autoindex_routed_agent_links_1", "sqlite_autoindex_routed_agent_links_2"} {
		if !indexes[name] {
			return false, Invalid("publication automatic index differs")
		}
		delete(indexes, name)
	}
	if len(indexes) != 0 {
		return false, Invalid("unknown publication index")
	}
	canonical := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	for i, name := range Tables {
		if canonical(seen[name]) != canonical(DDL[i]) {
			return false, Invalid("changed publication DDL")
		}
	}
	return true, nil
}
func scope(ctx context.Context, tx *sql.Tx, a workspace.Authority, id string) (Scope, error) {
	var raw []byte
	if e := tx.QueryRowContext(ctx, `SELECT substr(CAST(data AS BLOB),1,1048577) FROM workspace_instances WHERE id=?`, id).Scan(&raw); e != nil {
		return Scope{}, e
	}
	var root workspace.Instance
	if e := workspace.Decode(raw, &root); e != nil {
		return Scope{}, e
	}
	if root.ID != id || root.Authority != a || root.Self.Host != a.Host || root.State != "active" {
		return Scope{}, Invalid("active local workspace authority required")
	}
	generation := root.SelfGeneration
	if root.Role == "directory" {
		generation = 1
	}
	if generation < 1 {
		return Scope{}, Invalid("current local membership generation required")
	}
	return Scope{id, root.Generation, a, root.Self.Fingerprint, generation, root.Revision}, nil
}
func contact(ctx context.Context, tx *sql.Tx, id string) (agents.Contact, error) {
	var c agents.Contact
	var raw []byte
	e := tx.QueryRowContext(ctx, `SELECT substr(CAST(data AS BLOB),1,8193) FROM agents WHERE id=?`, id).Scan(&raw)
	if e != nil {
		return c, e
	}
	if len(raw) > MaxContactRecord || workspace.Decode(raw, &c) != nil || !ContactValid(c) {
		return c, Invalid("invalid bounded exact local contact")
	}
	return c, nil
}
func MakePlan(ctx context.Context, tx *sql.Tx, a workspace.Authority, q Request) (Plan, error) {
	if e := q.Validate(); e != nil {
		return Plan{}, e
	}
	s, e := scope(ctx, tx, a, q.Workspace)
	if e != nil {
		return Plan{}, e
	}
	c, e := contact(ctx, tx, q.ID)
	if e != nil {
		return Plan{}, e
	}
	if c.Session != q.Session || c.Registration != q.Registration {
		return Plan{}, Invalid("current contact session/registration differs")
	}
	p := Plan{Request: q, Scope: s, ContactSHA: Hash(c)}
	p.ReviewSHA = p.Digest()
	return p, nil
}
func put(ctx context.Context, tx *sql.Tx, query string, v any, args ...any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	limit := MaxRecord
	if _, ok := v.(Change); ok {
		limit = MaxChangeRecord
	}
	if len(raw) > limit {
		return Invalid("publication record exceeds bounded size")
	}
	if strings.HasPrefix(query, "UPDATE ") {
		args = append([]any{string(raw)}, args...)
	} else {
		args = append(args, string(raw))
	}
	_, e = tx.ExecContext(ctx, query, args...)
	return e
}
func insertChange(ctx context.Context, tx *sql.Tx, before *Publication, after Publication) error {
	var seq int64
	if e := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM published_agent_changes WHERE workspace=?`, after.Scope.Workspace).Scan(&seq); e != nil {
		return e
	}
	return put(ctx, tx, `INSERT INTO published_agent_changes(workspace,sequence,data) VALUES(?,?,?)`, Change{after.Scope.Workspace, seq, before, after}, after.Scope.Workspace, seq)
}
func Create(ctx context.Context, tx *sql.Tx, a workspace.Authority, q Request, expected string, actor Actor, at time.Time) (Publication, error) {
	if e := q.Validate(); e != nil {
		return Publication{}, e
	}
	if _, e := schema(ctx, tx, true); e != nil {
		return Publication{}, e
	}
	v, e := Read(ctx, tx)
	if e != nil {
		return Publication{}, e
	}
	for _, p := range v.Publications {
		if p.Scope.Workspace == q.Workspace && p.Request.Key == q.Key {
			if !reflect.DeepEqual(p.Request, q) || expected != "" && expected != p.ReviewSHA {
				return p, Invalid("publication key belongs to different immutable review")
			}
			return p, nil
		}
	}
	plan, e := MakePlan(ctx, tx, a, q)
	if e != nil {
		return Publication{}, e
	}
	if expected != plan.ReviewSHA {
		return Publication{}, Invalid("publication review changed")
	}
	c, e := contact(ctx, tx, q.ID)
	if e != nil {
		return Publication{}, e
	}
	for _, p := range v.Publications {
		if p.Scope.Workspace == q.Workspace && p.State == "active" && p.Contact.ID == c.ID {
			return Publication{}, Invalid("contact already has an active publication; withdraw it before replacement")
		}
	}
	p := Publication{Schema: 1, Scope: plan.Scope, Request: q, Endpoint: Address{"agent", Hash([]string{a.Host, q.Workspace, q.Key})[:32], Hash(plan)[:32]}, Contact: c, ContactSHA: Hash(c), ReviewSHA: plan.ReviewSHA, Actor: actor, State: "active", Revision: 1, Created: at, Updated: at}
	if e = put(ctx, tx, `INSERT INTO published_agents(workspace,endpoint,generation,key,data) VALUES(?,?,?,?,?)`, p, q.Workspace, p.Endpoint.ID, p.Endpoint.Generation, q.Key); e != nil {
		return p, e
	}
	if e = insertChange(ctx, tx, nil, p); e != nil {
		return p, e
	}
	_, e = Read(ctx, tx)
	return p, e
}
func Withdraw(ctx context.Context, tx *sql.Tx, a workspace.Authority, id, endpoint, generation string, q Withdrawal, at time.Time) (Publication, error) {
	v, e := Read(ctx, tx)
	if e != nil {
		return Publication{}, e
	}
	for _, p := range v.Publications {
		if p.Scope.Workspace == id && p.Endpoint.ID == endpoint && p.Endpoint.Generation == generation {
			if p.Scope.Authority != a || !Text(q.Key, 128) || !Text(q.Reason, 512) {
				return p, Invalid("exact owner withdrawal scope required")
			}
			if p.State == "withdrawn" {
				if p.Withdrawal != nil && *p.Withdrawal == q {
					return p, nil
				}
				return p, Invalid("publication already withdrawn under another request")
			}
			if p.Revision != q.Revision || at.Before(p.Updated) {
				return p, Invalid("publication revision or clock changed")
			}
			old := p
			p.State = "withdrawn"
			p.Revision++
			p.Updated = at
			p.Withdrawal = &q
			if e = put(ctx, tx, `UPDATE published_agents SET data=? WHERE workspace=? AND endpoint=? AND generation=?`, p, id, endpoint, generation); e != nil {
				return p, e
			}
			if e = insertChange(ctx, tx, &old, p); e != nil {
				return p, e
			}
			_, e = Read(ctx, tx)
			return p, e
		}
	}
	return Publication{}, sql.ErrNoRows
}

// Called within the SAME transaction that changes the Contact. No manager or
// workspace lock is acquired and no filesystem/native adapter work is done.
func WithdrawContact(ctx context.Context, tx *sql.Tx, old, next agents.Contact, at time.Time) error {
	if old == next {
		return nil
	}
	v, e := Read(ctx, tx)
	if e != nil {
		return e
	}
	for _, p := range v.Publications {
		if p.State == "active" && p.Contact.ID == old.ID {
			q := Withdrawal{Key: "contact-change-" + Hash(next)[:32], Reason: "Exact local contact configuration changed", Revision: p.Revision}
			if _, e = Withdraw(ctx, tx, p.Scope.Authority, p.Scope.Workspace, p.Endpoint.ID, p.Endpoint.Generation, q, at); e != nil {
				return e
			}
		}
	}
	return nil
}
func Find(v Records, id string, e Address) (Publication, bool) {
	for _, p := range v.Publications {
		if p.Scope.Workspace == id && p.Endpoint == e {
			return p, true
		}
	}
	return Publication{}, false
}
func Current(ctx context.Context, tx *sql.Tx, id string, e Address) (Publication, error) {
	v, err := Read(ctx, tx)
	if err != nil {
		return Publication{}, err
	}
	p, ok := Find(v, id, e)
	if !ok || p.State != "active" {
		return p, Invalid("exact published endpoint is unavailable")
	}
	s, err := scope(ctx, tx, p.Scope.Authority, id)
	if err != nil || s.Generation != p.Scope.Generation || s.Membership != p.Scope.Membership || s.Pin != p.Scope.Pin {
		return p, Invalid("publication belongs to a replaced workspace membership")
	}
	c, err := contact(ctx, tx, p.Contact.ID)
	if err != nil || Hash(c) != p.ContactSHA {
		return p, Invalid("published exact contact changed")
	}
	return p, nil
}
func Source(ctx context.Context, tx *sql.Tx, id, contactID, session, registration string) (Publication, error) {
	v, e := Read(ctx, tx)
	if e != nil {
		return Publication{}, e
	}
	for _, p := range v.Publications {
		if p.Scope.Workspace == id && p.State == "active" && p.Contact.ID == contactID && p.Contact.Session == session && p.Contact.Registration == registration {
			return Current(ctx, tx, id, p.Endpoint)
		}
	}
	return Publication{}, Invalid("sender requires its exact current published registration")
}
func PublicPage(ctx context.Context, tx *sql.Tx, a workspace.RouteAccess, q Query) (Page, error) {
	if e := q.Validate(); e != nil {
		return Page{}, e
	}
	if q.Workspace != a.Workspace || a.Local != a.Destination {
		return Page{}, Invalid("endpoint page target differs")
	}
	v, e := Read(ctx, tx)
	if e != nil {
		return Page{}, e
	}
	p := Page{Workspace: q.Workspace, Generation: a.Generation, Host: a.Local.Host, Nonce: q.Nonce, Expires: a.Expires, Offset: q.Offset, Cards: []Card{}}
	cards := []Card{}
	for _, c := range v.Changes {
		if c.Workspace == q.Workspace {
			p.Revision = max(p.Revision, c.Sequence)
		}
	}
	for _, pub := range v.Publications {
		if pub.Scope.Workspace == q.Workspace && pub.State == "active" && pub.Scope.Generation == a.Generation && pub.Scope.Membership == a.DestinationGeneration {
			// Read already checked every active private contact. Do not rescan
			// the complete retained inbox for each individual public card.
			if pub.Scope.Pin != a.Destination.Fingerprint {
				return p, Invalid("endpoint card pin differs from current final host")
			}
			cards = append(cards, pub.Card())
		}
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Endpoint.ID < cards[j].Endpoint.ID })
	p.Total = len(cards)
	if q.Revision != 0 && q.Revision != p.Revision || q.Offset > len(cards) {
		return p, Invalid("endpoint page revision/offset changed")
	}
	p.Next = min(q.Offset+q.Limit, len(cards))
	p.Cards = append(p.Cards, cards[q.Offset:p.Next]...)
	return p, p.Validate()
}
func InsertLink(ctx context.Context, tx *sql.Tx, l Link) error {
	if e := put(ctx, tx, `INSERT INTO routed_agent_links(workspace,message,local_message,data) VALUES(?,?,?,?)`, l, l.Context.Workspace, l.Context.Message, l.LocalMessage); e != nil {
		return e
	}
	_, e := Read(ctx, tx)
	return e
}

// WithdrawWorkspace executes on the caller's retirement transaction.
func WithdrawWorkspace(ctx context.Context, tx *sql.Tx, a workspace.Authority, id, key string, at time.Time) error {
	v, e := Read(ctx, tx)
	if e != nil {
		return e
	}
	for _, p := range v.Publications {
		if p.Scope.Workspace == id && p.State == "active" {
			if _, e = Withdraw(ctx, tx, a, id, p.Endpoint.ID, p.Endpoint.Generation, Withdrawal{Key: key, Reason: "Workspace retired", Revision: p.Revision}, at); e != nil {
				return e
			}
		}
	}
	return nil
}
