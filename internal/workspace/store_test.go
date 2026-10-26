package workspace

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/model"
	_ "modernc.org/sqlite"
)

type fakeClock struct {
	sync.Mutex
	wall    time.Time
	elapsed time.Duration
}

func (c *fakeClock) Wall() time.Time        { c.Lock(); defer c.Unlock(); return c.wall }
func (c *fakeClock) Elapsed() time.Duration { c.Lock(); defer c.Unlock(); return c.elapsed }
func (c *fakeClock) advance(wall, elapsed time.Duration) {
	c.Lock()
	defer c.Unlock()
	c.wall = c.wall.Add(wall)
	c.elapsed += elapsed
}
func identityFixture(t *testing.T, now time.Time) Identity {
	t.Helper()
	host := local.ID()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{"node-" + host + ".arm.invalid"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	fingerprint := sha256.Sum256(der)
	spki := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	return Identity{Host: host, Name: "fixture node", Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), Fingerprint: hex.EncodeToString(fingerprint[:]), SPKI: hex.EncodeToString(spki[:]), Expires: parsed.NotAfter}
}

type fixture struct {
	s         *Store
	db        *sql.DB
	clock     *fakeClock
	ctx       context.Context
	authority Authority
	self      Identity
}

func newStoreFixture(t *testing.T) fixture {
	t.Helper()
	clock := &fakeClock{wall: time.Now().UTC().Truncate(time.Second)}
	self := identityFixture(t, clock.Wall())
	a := Authority{Host: self.Host, UID: local.CurrentIdentity().UID}
	db, e := sql.Open("sqlite", filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, e = db.Exec(`PRAGMA journal_mode=WAL;PRAGMA busy_timeout=5000;CREATE TABLE metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO metadata(key,value) VALUES('host',?)`, a.Host); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s, e := Open(ctx, db, a, self, clock)
	if e != nil {
		t.Fatal(e)
	}
	return fixture{s, db, clock, ctx, a, self}
}
func code(e error) string {
	var m *model.Error
	if errors.As(e, &m) {
		return m.Code
	}
	return ""
}
func activateDirectory(t *testing.T, f fixture) (Instance, Operation) {
	t.Helper()
	op, e := f.s.PrepareCreate(f.ctx, CreateRequest{Key: "create", Name: "fixture workspace", Policy: DefaultPolicy(), Reason: "isolated policy fixture"})
	if e != nil {
		t.Fatal(e)
	}
	op, e = f.s.Decide(f.ctx, op.Workspace, op.Key, op.Revision, op.ReviewHash, true)
	if e != nil {
		t.Fatal(e)
	}
	v, e := f.s.Read(f.ctx)
	if e != nil {
		t.Fatal(e)
	}
	return v.Instances[0], op
}

func TestReadDoesNotInitializeAndCreateApprovalIsAtomic(t *testing.T) {
	f := newStoreFixture(t)
	v, e := f.s.Read(f.ctx)
	if e != nil || v.Initialized {
		t.Fatal(v, e)
	}
	var count int
	if e = f.db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name LIKE 'workspace_%'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("read initialized workspace schema", count, e)
	}
	q := CreateRequest{Key: "create", Name: "fixture workspace", Policy: DefaultPolicy(), Reason: "operator reviewed directory"}
	prepared, e := f.s.PrepareCreate(f.ctx, q)
	if e != nil {
		t.Fatal(e)
	}
	if prepared.Private != nil || prepared.Phase != "awaiting_review" {
		t.Fatal(prepared)
	}
	v, e = f.s.Read(f.ctx)
	if e != nil || len(v.Members) != 0 || len(v.Instances) != 1 || v.Instances[0].State != "prepared" {
		t.Fatal(v, e)
	}
	again, e := f.s.PrepareCreate(f.ctx, q)
	if e != nil || again.Workspace != prepared.Workspace {
		t.Fatal("same key created a second workspace", again, e)
	}
	q.Name = "different"
	if _, e = f.s.PrepareCreate(f.ctx, q); code(e) != "idempotency_conflict" {
		t.Fatal(e)
	}
	f.s.fault = func(stage string) error {
		if stage == "create_member" {
			return errors.New("injected member journal failure")
		}
		return nil
	}
	if _, e = f.s.Decide(f.ctx, prepared.Workspace, prepared.Key, prepared.Revision, prepared.ReviewHash, true); e == nil {
		t.Fatal("injected failure succeeded")
	}
	f.s.fault = nil
	v, e = f.s.Read(f.ctx)
	if e != nil || len(v.Members) != 0 || v.Instances[0].State != "prepared" {
		t.Fatal("partial activation survived rollback", v, e)
	}
	approved, e := f.s.Decide(f.ctx, prepared.Workspace, prepared.Key, prepared.Revision, prepared.ReviewHash, true)
	if e != nil || approved.Phase != "completed" {
		t.Fatal(approved, e)
	}
	v, e = f.s.Read(f.ctx)
	if e != nil || len(v.Members) != 1 || len(v.Trusted) != 1 || v.Members[0].State != "active" || !v.Members[0].Introducer {
		t.Fatal(v, e)
	}
	if _, e = f.s.Decide(f.ctx, prepared.Workspace, prepared.Key, prepared.Revision, prepared.ReviewHash, false); code(e) != "decision_conflict" {
		t.Fatal("decline reversed recorded approval", e)
	}
}
func TestLostCommitReplyHasReadOnlyKeyedOutcome(t *testing.T) {
	f := newStoreFixture(t)
	op, e := f.s.PrepareCreate(f.ctx, CreateRequest{"key", "name", DefaultPolicy(), "review"})
	if e != nil {
		t.Fatal(e)
	}
	f.s.fault = func(stage string) error {
		if stage == "after_commit" {
			return errors.New("lost ACK")
		}
		return nil
	}
	if _, e = f.s.Decide(f.ctx, op.Workspace, op.Key, op.Revision, op.ReviewHash, true); code(e) != "workspace_outcome_unknown" {
		t.Fatal(e)
	}
	saved, found, e := f.s.Lookup(f.ctx, op.Workspace, op.Key)
	if e != nil || !found || saved.Phase != "completed" || saved.Private != nil {
		t.Fatal(saved, found, e)
	}
	f.s.fault = nil
	if _, e = f.s.Decide(f.ctx, op.Workspace, op.Key, op.Revision, op.ReviewHash, true); e != nil {
		t.Fatal(e)
	}
	v, e := f.s.Read(f.ctx)
	if e != nil || len(v.Members) != 1 || len(v.Changes) != 2 {
		t.Fatal("retry duplicated admission/journal", v, e)
	}
}
func TestRestartDoesNotRevivePreparedReviewAndExpiredHistoryRemainsReadable(t *testing.T) {
	f := newStoreFixture(t)
	op, e := f.s.PrepareCreate(f.ctx, CreateRequest{"key", "name", DefaultPolicy(), "review"})
	if e != nil {
		t.Fatal(e)
	}
	restarted, e := Open(f.ctx, f.db, f.authority, f.self, f.clock)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.Decide(f.ctx, op.Workspace, op.Key, op.Revision, op.ReviewHash, true); code(e) != "review_expired" {
		t.Fatal("restart revived review worker", e)
	}
	if _, e = restarted.Decide(f.ctx, op.Workspace, op.Key, op.Revision, op.ReviewHash, false); e != nil {
		t.Fatal("local decline unavailable after interruption", e)
	}
	g := newStoreFixture(t)
	root, _ := activateDirectory(t, g)
	g.clock.advance(25*time.Hour, 25*time.Hour)
	if _, e = g.s.Read(g.ctx); e != nil {
		t.Fatal("one expired historical member corrupted ledger", e)
	}
	restarted, e = Open(g.ctx, g.db, g.authority, g.self, g.clock)
	if e != nil {
		t.Fatal("expired history prevents startup", e)
	}
	_, found, e := restarted.Lookup(g.ctx, root.ID, "create")
	if e != nil || !found {
		t.Fatal("historical outcome unavailable", found, e)
	}
	p := Principal{root.ID, root.Generation, root.Self.Host, 1, root.Self.Fingerprint}
	if _, _, e = restarted.MemberLookup(g.ctx, p, "missing"); code(e) != "identity_expired" {
		t.Fatal("expired member authorized current call", e)
	}
}
func TestStrictRequiredPolicyAndUnknownSchemaAreNotUnlimited(t *testing.T) {
	for _, kind := range []string{"missing-policy", "null-bool", "case-alias", "unknown", "trailing", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			f := newStoreFixture(t)
			root, _ := activateDirectory(t, f)
			raw, _ := json.Marshal(root)
			var object map[string]json.RawMessage
			_ = json.Unmarshal(raw, &object)
			switch kind {
			case "missing-policy":
				delete(object, "policy")
			case "null-bool":
				var p map[string]json.RawMessage
				_ = json.Unmarshal(object["policy"], &p)
				p["host_inbox_enabled"] = json.RawMessage(`null`)
				object["policy"], _ = json.Marshal(p)
			case "case-alias":
				object["State"] = json.RawMessage(`"retired"`)
			case "unknown":
				object["arbitrary"] = json.RawMessage(`true`)
			}
			raw, _ = json.Marshal(object)
			if kind == "trailing" {
				raw = append(raw, []byte(` {}`)...)
			}
			if kind == "duplicate" {
				raw = append([]byte(`{"state":"retired",`), raw[1:]...)
			}
			if _, e := f.db.Exec(`UPDATE workspace_instances SET data=? WHERE id=?`, string(raw), root.ID); e != nil {
				t.Fatal(e)
			}
			if _, e := f.s.Read(f.ctx); code(e) != "workspace_state_invalid" {
				t.Fatal("corrupt policy accepted", kind, e)
			}
			if _, e := Open(f.ctx, f.db, f.authority, f.self, f.clock); code(e) != "workspace_state_invalid" {
				t.Fatal("corrupt schema reopened", e)
			}
		})
	}
}
