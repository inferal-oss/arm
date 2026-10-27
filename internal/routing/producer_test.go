package routing

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/workspace"
	_ "modernc.org/sqlite"
)

type messageClock struct {
	mu                        sync.Mutex
	start                     time.Time
	wallOffset, elapsedOffset time.Duration
}

func (c *messageClock) Wall() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().UTC().Add(c.wallOffset)
}
func (c *messageClock) Elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.start) + c.elapsedOffset
}
func (c *messageClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wallOffset += d
	c.elapsedOffset += d
}

type msgNode struct {
	clock    *messageClock
	db       *sql.DB
	store    *workspace.Store
	runtime  *workspace.Runtime
	identity workspace.Identity
}

func messageNode(t *testing.T) *msgNode {
	t.Helper()
	host := local.ID()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{"node-" + host + ".arm.invalid"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pin := sha256.Sum256(der)
	spki := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	identity := workspace.Identity{Host: host, Name: "private message fixture", Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Fingerprint: hex.EncodeToString(pin[:]), SPKI: hex.EncodeToString(spki[:]), Expires: parsed.NotAfter}
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	if _, e = db.Exec(`CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO metadata(key,value) VALUES('host',?)`, host); e != nil {
		t.Fatal(e)
	}
	clock := &messageClock{start: time.Now()}
	s, e := workspace.Open(t.Context(), db, workspace.Authority{Host: host, UID: local.CurrentIdentity().UID}, identity, clock)
	if e != nil {
		t.Fatal(e)
	}
	n := &msgNode{clock: clock, db: db, store: s, runtime: workspace.NewRuntime(s), identity: identity}
	t.Cleanup(func() { n.runtime.Close(); db.Close() })
	return n
}
func messageFixture(t *testing.T) (string, *msgNode, *msgNode, *msgNode) {
	t.Helper()
	d, b, c := messageNode(t), messageNode(t), messageNode(t)
	root, e := d.store.PrepareCreate(t.Context(), workspace.CreateRequest{Key: "create", Name: "routed message fixture", Policy: workspace.DefaultPolicy(), Reason: "private exact message transaction"})
	if e != nil {
		t.Fatal(e)
	}
	root, e = d.store.Decide(t.Context(), root.Workspace, root.Key, root.Revision, root.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	id := root.Workspace
	records, e := d.store.Read(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	r := records.Instances[0]
	for _, n := range []*msgNode{b, c} {
		q := workspace.JoinRequest{Key: "join", Workspace: id, Generation: r.Generation, Name: r.Name, Directory: d.identity, Policy: r.Policy, PolicyRevision: r.PolicyRevision, Introducer: d.identity.Host, Reason: "review local message membership"}
		join, e := n.store.PrepareJoin(t.Context(), q)
		if e != nil {
			t.Fatal(e)
		}
		join, e = n.store.Decide(t.Context(), id, join.Key, join.Revision, join.ReviewHash, true)
		if e != nil {
			t.Fatal(e)
		}
		principal := workspace.Principal{Workspace: id, Generation: r.Generation, Host: d.identity.Host, MembershipGeneration: 1, Fingerprint: d.identity.Fingerprint}
		op, e := d.store.PrepareAdmission(t.Context(), principal, workspace.AdmissionRequest{Key: n.identity.Host, Candidate: n.identity, ConsentHash: join.ReviewHash, HostInbox: true, PolicyRevision: r.PolicyRevision, Reason: "exact recipient proof"})
		if e != nil {
			t.Fatal(e)
		}
		challenge, dispatch, e := d.store.BeginProof(t.Context(), principal, op.Key)
		if e != nil {
			t.Fatal(e)
		}
		proof, e := n.store.ProveJoin(t.Context(), d.identity, challenge)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = d.store.CompleteProof(t.Context(), principal, op.Key, dispatch.Revision, proof); e != nil {
			t.Fatal(e)
		}
	}
	for _, n := range []*msgNode{b, c} {
		request, e := n.runtime.Begin(t.Context(), id)
		if e != nil {
			t.Fatal(e)
		}
		snapshot, e := d.store.Issue(t.Context(), request)
		if e != nil {
			t.Fatal(e)
		}
		if e = n.runtime.Accept(t.Context(), d.identity, snapshot); e != nil {
			t.Fatal(e)
		}
		op, _, e := n.store.Lookup(t.Context(), id, "join")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = n.runtime.Finalize(t.Context(), id, "join", op.Revision); e != nil {
			t.Fatal(e)
		}
	}
	return id, d, b, c
}
func messageTx(t *testing.T, n *msgNode, id, from, to string, fn func(*sql.Tx, workspace.RouteAccess) error) {
	t.Helper()
	if _, e := n.runtime.RouteTransaction(t.Context(), id, from, to, workspace.RouteMessage, fn); e != nil {
		t.Fatal(e)
	}
}
func readMessageRecords(t *testing.T, n *msgNode) Records {
	t.Helper()
	tx, e := n.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	v, e := Read(t.Context(), tx)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestAtomicHostInboxDedupUnknownLookupAndOriginalReply(t *testing.T) {
	id, _, b, c := messageFixture(t)
	from, to := b.identity.Host, c.identity.Host
	q := Intent{Workspace: id, Key: "send", Target: to, From: HostEndpoint(1), To: HostEndpoint(1), Body: "original B to C", TTL: time.Minute}
	var sent Outbox
	messageTx(t, b, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		o, created, e := Prepare(t.Context(), tx, a, q)
		sent = o
		if e == nil && !created {
			t.Fatal("first message not prepared")
		}
		return e
	})
	messageTx(t, b, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		o, created, e := Prepare(t.Context(), tx, a, q)
		if e == nil && (created || o.Envelope.ID != sent.Envelope.ID) {
			t.Fatal("known key recreated message")
		}
		return e
	})
	messageTx(t, b, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		o, e := Begin(t.Context(), tx, a, q.From, q.Key, sent.Revision)
		sent = o
		return e
	})
	var received Inbox
	messageTx(t, c, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		in, created, e := Accept(t.Context(), tx, a, sent.Envelope)
		received = in
		if e == nil && !created {
			t.Fatal("new message not accepted")
		}
		return e
	})
	// Lost receipt: origin remains unknown; destination has one exact inbox item.
	messageTx(t, b, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		o, e := Unknown(t.Context(), tx, a, q.From, q.Key, sent.Revision)
		sent = o
		return e
	})
	messageTx(t, c, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		in, created, e := Accept(t.Context(), tx, a, sent.Envelope)
		if e == nil && (created || in.Receipt != received.Receipt) {
			t.Fatal("duplicate executed twice")
		}
		return e
	})
	messageTx(t, c, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		r, e := Lookup(t.Context(), tx, a, sent.Envelope)
		if e == nil && (r == nil || *r != received.Receipt) {
			t.Fatal("lookup differs")
		}
		return e
	})
	messageTx(t, b, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		o, e := Complete(t.Context(), tx, a, q.From, q.Key, received.Receipt)
		sent = o
		return e
	})
	if sent.Phase != "completed" || sent.Receipt == nil || len(readMessageRecords(t, c).Inbox) != 1 {
		t.Fatal("receipt/inbox not durable")
	}
	ref := received.Envelope.Reference()
	reply := Intent{Workspace: id, Key: "reply", Target: from, From: HostEndpoint(1), To: HostEndpoint(1), Body: "C replies to original B", TTL: time.Minute, ReplyTo: &ref}
	var response Outbox
	messageTx(t, c, id, to, from, func(tx *sql.Tx, a workspace.RouteAccess) error {
		o, _, e := Prepare(t.Context(), tx, a, reply)
		response = o
		return e
	})
	messageTx(t, c, id, to, from, func(tx *sql.Tx, a workspace.RouteAccess) error {
		o, e := Begin(t.Context(), tx, a, reply.From, reply.Key, response.Revision)
		response = o
		return e
	})
	messageTx(t, b, id, to, from, func(tx *sql.Tx, a workspace.RouteAccess) error {
		in, _, e := Accept(t.Context(), tx, a, response.Envelope)
		if e == nil && in.Receipt.State != "accepted" {
			t.Fatal("original sender did not accept exact original recipient reply", in.Receipt)
		}
		return e
	})
	if len(readMessageRecords(t, b).Inbox) != 1 {
		t.Fatal("reply missed original sender")
	}
}
func TestRoutedMessageRejectsClaimedOriginChangedPayloadAndForgedReply(t *testing.T) {
	id, d, b, c := messageFixture(t)
	from, to := b.identity.Host, c.identity.Host
	q := Intent{Workspace: id, Key: "same", Target: to, From: HostEndpoint(1), To: HostEndpoint(1), Body: "body", TTL: time.Minute}
	var o Outbox
	messageTx(t, b, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		var e error
		o, _, e = Prepare(t.Context(), tx, a, q)
		return e
	})
	_, e := c.runtime.RouteTransaction(t.Context(), id, d.identity.Host, to, workspace.RouteMessage, func(tx *sql.Tx, a workspace.RouteAccess) error {
		_, _, e := Accept(t.Context(), tx, a, o.Envelope)
		return e
	})
	if e == nil {
		t.Fatal("relay substituted original source")
	}
	messageTx(t, c, id, from, to, func(tx *sql.Tx, a workspace.RouteAccess) error {
		_, _, e := Accept(t.Context(), tx, a, o.Envelope)
		return e
	})
	changed := o.Envelope
	changed.Body = "other"
	_, e = c.runtime.RouteTransaction(t.Context(), id, from, to, workspace.RouteMessage, func(tx *sql.Tx, a workspace.RouteAccess) error {
		_, _, e := Accept(t.Context(), tx, a, changed)
		return e
	})
	if e == nil {
		t.Fatal("same key changed body")
	}
	forged := q
	forged.Target = from
	forged.Key = "fake-reply"
	r := o.Envelope.Reference()
	r.ID = local.ID()
	forged.ReplyTo = &r
	_, e = c.runtime.RouteTransaction(t.Context(), id, to, from, workspace.RouteMessage, func(tx *sql.Tx, a workspace.RouteAccess) error {
		_, _, e := Prepare(t.Context(), tx, a, forged)
		return e
	})
	if e == nil {
		t.Fatal("message ID possession forged original recipient")
	}
	if len(readMessageRecords(t, c).Inbox) != 1 || len(readMessageRecords(t, c).Outbox) != 0 {
		t.Fatal("failed producer changed records")
	}
}
func TestRoutingReadDoesNotInitializeAndUnknownDoesNotReplay(t *testing.T) {
	id, _, b, c := messageFixture(t)
	if readMessageRecords(t, b).Initialized {
		t.Fatal("read installed schema")
	}
	q := Intent{Workspace: id, Key: "once", Target: c.identity.Host, From: HostEndpoint(1), To: HostEndpoint(1), Body: "retain uncertainty", TTL: time.Minute}
	var o Outbox
	messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
		var e error
		o, _, e = Prepare(t.Context(), tx, a, q)
		return e
	})
	messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
		var e error
		o, e = Begin(t.Context(), tx, a, q.From, q.Key, o.Revision)
		return e
	})
	messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
		var e error
		o, e = Unknown(t.Context(), tx, a, q.From, q.Key, o.Revision)
		return e
	})
	_, e := b.runtime.RouteTransaction(t.Context(), id, b.identity.Host, c.identity.Host, workspace.RouteMessage, func(tx *sql.Tx, a workspace.RouteAccess) error {
		_, e := Begin(t.Context(), tx, a, q.From, q.Key, o.Revision)
		return e
	})
	if e == nil {
		t.Fatal("unknown intent resent")
	}
	messageTx(t, c, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
		r, e := Lookup(t.Context(), tx, a, o.Envelope)
		if e == nil && r != nil {
			t.Fatal("missing destination invented receipt")
		}
		return e
	})
	if readMessageRecords(t, c).Initialized {
		t.Fatal("absent lookup initialized destination routing state")
	}
	if len(readMessageRecords(t, b).Outbox) != 1 {
		t.Fatal("unknown source intent lost")
	}
}
func TestRoutingStrictRawAliasAndCrossLinkCorruption(t *testing.T) {
	for _, damage := range []string{"alias", "request", "journal"} {
		t.Run(damage, func(t *testing.T) {
			id, _, b, c := messageFixture(t)
			q := Intent{Workspace: id, Key: "one", Target: c.identity.Host, From: HostEndpoint(1), To: HostEndpoint(1), Body: "strict evidence", TTL: time.Minute}
			messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error { _, _, e := Prepare(t.Context(), tx, a, q); return e })
			var e error
			switch damage {
			case "alias":
				_, e = b.db.Exec(`UPDATE routing_outbox SET data=replace(data,'"phase":','"Phase":')`)
			case "request":
				_, e = b.db.Exec(`UPDATE routing_outbox SET request='{}'`)
			case "journal":
				_, e = b.db.Exec(`DELETE FROM routing_events WHERE sequence=2`)
			}
			if e != nil {
				t.Fatal(e)
			}
			tx, e := b.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			if _, e = Read(t.Context(), tx); e == nil {
				t.Fatal("damaged routing evidence accepted")
			}
		})
	}
}
func TestRoutingPendingLimitRollbackAndSettlementReserve(t *testing.T) {
	id, _, b, c := messageFixture(t)
	var first Outbox
	for i := 0; i < MaxPending; i++ {
		q := Intent{Workspace: id, Key: "pending-" + strconv.Itoa(i), Target: c.identity.Host, From: HostEndpoint(1), To: HostEndpoint(1), Body: "bounded pending sender intent", TTL: time.Minute}
		messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
			o, _, e := Prepare(t.Context(), tx, a, q)
			if i == 0 {
				first = o
			}
			return e
		})
	}
	before := readMessageRecords(t, b)
	q := first.Intent
	q.Key = "one-too-many"
	_, e := b.runtime.RouteTransaction(t.Context(), id, b.identity.Host, c.identity.Host, workspace.RouteMessage, func(tx *sql.Tx, a workspace.RouteAccess) error { _, _, e := Prepare(t.Context(), tx, a, q); return e })
	if e == nil {
		t.Fatal("ninth pending delivery admitted")
	}
	after := readMessageRecords(t, b)
	if hash(before) != hash(after) {
		t.Fatal("refused admission changed routing records/journal")
	}
	messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
		o, e := Begin(t.Context(), tx, a, first.Intent.From, first.Intent.Key, first.Revision)
		first = o
		return e
	})
	var result Inbox
	messageTx(t, c, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
		in, _, e := Accept(t.Context(), tx, a, first.Envelope)
		result = in
		return e
	})
	messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
		o, e := Unknown(t.Context(), tx, a, first.Intent.From, first.Intent.Key, first.Revision)
		first = o
		return e
	})
	messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
		_, e := Complete(t.Context(), tx, a, first.Intent.From, first.Intent.Key, result.Receipt)
		return e
	})
	messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error { _, _, e := Prepare(t.Context(), tx, a, q); return e })
	if len(readMessageRecords(t, b).Outbox) != MaxPending+1 {
		t.Fatal("settlement failed to free source admission slot")
	}
}

func settlementTx(t *testing.T, n *msgNode, fn func(*sql.Tx) error) error {
	t.Helper()
	tx, e := n.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit()
}
func TestRoutingLocalRetirementNeverSettlesDispatchedUnknown(t *testing.T) {
	for _, phase := range []string{"prepared", "unknown"} {
		t.Run(phase, func(t *testing.T) {
			id, _, b, c := messageFixture(t)
			var out Outbox
			messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
				var e error
				out, _, e = Prepare(t.Context(), tx, a, Intent{Workspace: id, Key: "intent", Target: c.identity.Host, From: HostEndpoint(1), To: HostEndpoint(1), Body: "retained owner intent", TTL: time.Minute})
				return e
			})
			if phase == "unknown" {
				messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
					var e error
					out, e = Begin(t.Context(), tx, a, out.Envelope.From, out.Intent.Key, out.Revision)
					return e
				})
				messageTx(t, b, id, b.identity.Host, c.identity.Host, func(tx *sql.Tx, a workspace.RouteAccess) error {
					var e error
					out, e = Unknown(t.Context(), tx, a, out.Envelope.From, out.Intent.Key, out.Revision)
					return e
				})
			}
			a := workspace.Authority{Host: b.identity.Host, UID: local.CurrentIdentity().UID}
			e := settlementTx(t, b, func(tx *sql.Tx) error {
				_, e := CancelPrepared(t.Context(), tx, a, id, out.Intent.Key, out.Revision, time.Now().UTC())
				return e
			})
			if (e == nil) != (phase == "prepared") {
				t.Fatal("incorrect cancellation outcome", phase, e)
			}
			v, e := b.store.Read(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			if _, e = b.store.Retire(t.Context(), id, workspace.MutationRequest{Key: "retire-workspace", Revision: v.Instances[0].Revision, Reason: "explicit local retirement preserving remote state"}); e != nil {
				t.Fatal(e)
			}
			before := readMessageRecords(t, b)
			root := before.Roots[0]
			e = settlementTx(t, b, func(tx *sql.Tx) error {
				_, e := Retire(t.Context(), tx, a, id, "retire-routing", root.Revision, time.Now().UTC())
				return e
			})
			if (e == nil) != (phase == "prepared") {
				t.Fatal("incorrect routing retirement", phase, e)
			}
			after := readMessageRecords(t, b)
			if phase == "unknown" && hash(before) != hash(after) {
				t.Fatal("refused retirement changed retained uncertainty")
			}
			if phase == "prepared" {
				if after.Roots[0].State != "retired" {
					t.Fatal("explicit local routing retirement not recorded")
				}
				if e = settlementTx(t, b, func(tx *sql.Tx) error {
					_, e := Retire(t.Context(), tx, a, id, "retire-routing", root.Revision, time.Now().UTC())
					return e
				}); e != nil {
					t.Fatal("exact retirement retry failed", e)
				}
			}
		})
	}
}
