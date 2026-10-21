package mesh

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"github.com/yrashk/arm/internal/model"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testStore(t *testing.T, id, name, address string) *Store {
	t.Helper()
	s, e := Init(filepath.Join(t.TempDir(), "state"), strings.Repeat(id, 32), name, "127.0.0.1:40001", address)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func trust(t *testing.T, a, b *Store, grant Grant) {
	t.Helper()
	pair := strings.Repeat("12", 32)
	for _, entry := range []struct {
		a, b *Store
		g    Grant
	}{{a, b, grant}, {b, a, grant}} {
		if _, e := entry.a.Pair(entry.b.Config().Identity, entry.g, pair, "test explicit pairing"); e != nil {
			t.Fatal(e)
		}
		if e := entry.a.Prove(entry.b.Config().Identity.Host, pair); e != nil {
			t.Fatal(e)
		}
		if e := entry.a.Activate(entry.b.Config().Identity.Host, pair); e != nil {
			t.Fatal(e)
		}
	}
}
func TestIdentityPinsPrivateFilesAndGrantIsolation(t *testing.T) {
	a := testStore(t, "a", "alpha", "127.0.0.1:40001")
	b := testStore(t, "b", "beta", "127.0.0.1:40002")
	g := Grant{Permissions: []string{"observe-host"}, Contacts: []string{"contact"}}
	p, e := a.Pair(b.Config().Identity, g, strings.Repeat("12", 32), "test")
	if e != nil {
		t.Fatal(e)
	}
	g.Permissions[0] = "run"
	g.Contacts[0] = "unrelated"
	p.Grant.Permissions[0] = "run"
	p.Grant.Contacts[0] = "unrelated"
	resolved, e := a.Resolve("beta")
	if e != nil || resolved.Grant.Has("run") || resolved.Grant.Contacts[0] != "contact" {
		t.Fatal("grant alias changed stored authority", resolved, e)
	}
	resolved.Grant.Permissions[0] = "run"
	if a.Config().Peers[0].Grant.Has("run") {
		t.Fatal("resolve aliases stored grant")
	}
	info, e := os.Stat(filepath.Join(a.home, "mesh", "node-key.pem"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}
	if _, e = ValidateIdentity(a.Config().Identity); e != nil {
		t.Fatal(e)
	}
	if len(a.Config().History) != 1 || len(a.Config().History[0].ReviewDigest) != 64 || len(a.Config().History[0].SPKI) != 64 {
		t.Fatal("no transactional trust history")
	}
}
func TestGrantAndAddressValidation(t *testing.T) {
	for _, cpu := range []float64{math.NaN(), math.Inf(1), -1} {
		if (Grant{Limits: model.Resources{CPU: cpu}}).Validate() == nil {
			t.Fatal("invalid CPU grant accepted")
		}
	}
	for _, address := range []string{"127.0.0.1:garbage", "127.0.0.1:0", "127.0.0.1:65536", "0.0.0.0:1234", "[::]:1234"} {
		if validateListen(address) == nil {
			t.Fatal("unsafe listener accepted", address)
		}
	}
	a := testStore(t, "a", "alpha", "127.0.0.1:40001")
	b := testStore(t, "b", "beta", "127.0.0.1:40002")
	if _, e := a.Pair(b.Config().Identity, Grant{}, strings.Repeat("z", 64), "not hex"); e == nil {
		t.Fatal("nonhex pair ID accepted")
	}
}
func TestMeshPrivateFileHardlinkSymlinkAndDirectoryChangeRefusals(t *testing.T) {
	for _, mode := range []string{"symlink", "hardlink", "permissions", "directory-replaced"} {
		t.Run(mode, func(t *testing.T) {
			a := testStore(t, "a", "alpha", "127.0.0.1:40001")
			key := filepath.Join(a.home, "mesh", "node-key.pem")
			switch mode {
			case "symlink":
				raw, e := os.ReadFile(key)
				if e != nil {
					t.Fatal(e)
				}
				target := filepath.Join(t.TempDir(), "key")
				os.WriteFile(target, raw, 0600)
				os.Remove(key)
				os.Symlink(target, key)
			case "hardlink":
				if e := os.Link(key, filepath.Join(t.TempDir(), "shared")); e != nil {
					t.Fatal(e)
				}
			case "permissions":
				os.Chmod(key, 0644)
			case "directory-replaced":
				if e := os.Rename(filepath.Join(a.home, "mesh"), filepath.Join(a.home, "old")); e != nil {
					t.Fatal(e)
				}
				if e := os.Mkdir(filepath.Join(a.home, "mesh"), 0700); e != nil {
					t.Fatal(e)
				}
				if e := a.persist(a.Config()); e == nil {
					t.Fatal("changed directory accepted")
				}
				return
			}
			if _, e := Open(a.home); e == nil {
				t.Fatal("unsafe key accepted")
			}
		})
	}
}
func TestPinnedMutualTLSAndNoRedirects(t *testing.T) {
	var delivered atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { delivered.Add(1) }))
	defer plain.Close()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	a := testStore(t, "a", "alpha", "127.0.0.1:40001")
	b := testStore(t, "b", "beta", server.Listener.Addr().String())
	trust(t, a, b, Grant{Permissions: []string{"observe-host"}})
	server.TLS = b.ServerTLS()
	server.StartTLS()
	if _, e := a.Call(t.Context(), "beta", Request{Operation: "acquire", Key: "never-replay-elsewhere", Params: json.RawMessage(`{}`)}); e == nil {
		t.Fatal("redirect accepted")
	}
	if delivered.Load() != 0 {
		t.Fatal("redirect leaked request to plaintext endpoint")
	}
	peer, _ := a.Resolve("beta")
	config, e := a.ClientTLS(peer)
	if e != nil {
		t.Fatal(e)
	}
	config.ServerName = "wrong.arm.invalid"
	conn, e := tls.Dial("tcp", server.Listener.Addr().String(), config)
	if e == nil {
		conn.Close()
		t.Fatal("wrong SAN accepted")
	}
	wrong := testStore(t, "c", "charlie", "127.0.0.1:40003")
	config.Certificates = []tls.Certificate{wrong.cert}
	config.ServerName = SAN(b.Config().Identity.Host)
	conn, e = tls.Dial("tcp", server.Listener.Addr().String(), config)
	if e == nil {
		defer conn.Close()
		_, e = conn.Write([]byte("GET / HTTP/1.1\r\nHost: test\r\n\r\n"))
		if e == nil {
			buffer := make([]byte, 1)
			_, e = conn.Read(buffer)
		}
	}
	if e == nil {
		t.Fatal("untrusted client accepted")
	}
}
func TestRevocationAndExplicitRenewalRepair(t *testing.T) {
	a := testStore(t, "a", "alpha", "127.0.0.1:40001")
	b := testStore(t, "b", "beta", "127.0.0.1:40002")
	grant := Grant{Permissions: []string{"observe-host"}}
	trust(t, a, b, grant)
	old := a.Config().Identity
	renewed, e := a.Renew("explicit renewal")
	if e != nil || renewed.Fingerprint == old.Fingerprint || renewed.SPKI != old.SPKI {
		t.Fatal("renewal did not preserve key/change certificate", e)
	}
	if a.Config().Peers[0].State != "revoked" {
		t.Fatal("old grants survived certificate renewal")
	}
	if _, e = a.WithPeer(b.Config().Identity.Host, func(Peer) (any, error) { return true, nil }); e == nil {
		t.Fatal("revoked peer operation accepted")
	}
	if e = b.Revoke(old.Host, "old pin revoked before reviewed repair"); e != nil {
		t.Fatal(e)
	}
	pair := strings.Repeat("34", 32)
	if _, e = b.Pair(renewed, grant, pair, "missing replacement consent"); e == nil {
		t.Fatal("implicit revoked pin replacement accepted")
	}
	if _, e = b.PairReviewed(renewed, grant, pair, "explicit reviewed replacement", true); e != nil {
		t.Fatal(e)
	}
	if b.Config().Peers[0].State != "pending" || b.Config().Peers[0].Proven {
		t.Fatal("repair skipped fresh proof")
	}
	if _, e = b.WithPeer(old.Host, func(Peer) (any, error) { return true, nil }); e == nil {
		t.Fatal("pending repair has operational grant")
	}
	if e = b.Prove(old.Host, pair); e != nil {
		t.Fatal(e)
	}
	if e = b.Activate(old.Host, pair); e != nil {
		t.Fatal(e)
	}
	if e = b.Revoke(old.Host, "end test"); e != nil {
		t.Fatal(e)
	}
	if _, e = b.ClientTLS(Peer{Identity: renewed}); e != nil {
		t.Fatal(e)
	}
	cert, _ := ValidateIdentity(renewed)
	if _, e = b.PeerByCert(cert.Raw); e == nil {
		t.Fatal("revoked certificate accepted")
	}
}

func TestMutationResponseFailuresPreserveAuthorityAndKey(t *testing.T) {
	for _, body := range []string{`{}`, `{"result":null}`, `{"result":{}`, `{"result":{},"error":{"code":"forbidden","message":"both"}}`, `{"result":{}} {"result":{}}`, strings.Repeat("x", (4<<20)+1)} {
		t.Run(string([]byte(body)[:min(20, len(body))]), func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer server.Close()
			a := testStore(t, "a", "alpha", "127.0.0.1:40001")
			b := testStore(t, "b", "beta", server.Listener.Addr().String())
			trust(t, a, b, Grant{Permissions: []string{"observe-host"}})
			server.TLS = b.ServerTLS()
			server.StartTLS()
			_, e := a.Call(t.Context(), "beta", Request{Operation: "acquire", Key: "retain-this-key", Params: json.RawMessage(`{}`)})
			api, ok := e.(*model.Error)
			if !ok || api.Code != "remote_outcome_unknown" || !strings.Contains(api.Message, b.Config().Identity.Host) || !strings.Contains(api.Message, "retain-this-key") {
				t.Fatal("mutation uncertainty lost identity/key", e)
			}
		})
	}
}

func TestMeshPersistenceLimitIncludesFinalNewline(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if e := os.Mkdir(directory, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(directory, "bounded.json")
	if e := save(path, strings.Repeat("x", (1<<20)-2)); e == nil {
		t.Fatal("unreadable newline-overflowing write accepted")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("refused write changed destination", e)
	}
	if e := save(path, strings.Repeat("x", (1<<20)-3)); e != nil {
		t.Fatal(e)
	}
	if data, e := privateFile(path); e != nil || len(data) != 1<<20 {
		t.Fatal("exact final-size boundary unreadable", len(data), e)
	}
}

func expiredNodeIdentity(t *testing.T, s *Store) Identity {
	t.Helper()
	identity := s.Config().Identity
	old, e := x509.ParseCertificate(s.cert.Certificate[0])
	if e != nil {
		t.Fatal(e)
	}
	template := *old
	now := time.Now().UTC().Truncate(time.Second)
	template.NotBefore = now.Add(-2 * time.Hour)
	template.NotAfter = now.Add(-time.Hour)
	der, e := x509.CreateCertificate(rand.Reader, &template, &template, old.PublicKey, s.cert.PrivateKey)
	if e != nil {
		t.Fatal(e)
	}
	digest := sha256.Sum256(der)
	identity.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	identity.Fingerprint = hex.EncodeToString(digest[:])
	identity.Expires = template.NotAfter
	return identity
}
func TestExpiredLocalCertificateHasExplicitRecoveryAndPeerExpiryIsIsolated(t *testing.T) {
	a := testStore(t, "a", "alpha", "127.0.0.1:40001")
	b := testStore(t, "b", "beta", "127.0.0.1:40002")
	trust(t, a, b, Grant{Permissions: []string{"observe-host"}})
	expiredPeer := expiredNodeIdentity(t, b)
	a.mu.Lock()
	e := a.update(func(c *Config) error { c.Peers[0].Identity = expiredPeer; return nil })
	a.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(a.home)
	if e != nil {
		t.Fatal("one expired peer disabled healthy mesh", e)
	}
	defer reopened.Close()
	cert, e := parseIdentity(expiredPeer, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = reopened.PeerByCert(cert.Raw); e == nil {
		t.Fatal("expired peer authenticated")
	}
	expiredLocal := expiredNodeIdentity(t, b)
	b.mu.Lock()
	e = b.update(func(c *Config) error { c.Identity = expiredLocal; return nil })
	b.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	b.Close()
	if store, e := Open(b.home); e == nil {
		store.Close()
		t.Fatal("expired local certificate enabled listener")
	}
	recovery, e := OpenRecovery(b.home)
	if e != nil {
		t.Fatal("expired identity recovery unavailable", e)
	}
	defer recovery.Close()
	renewed, e := recovery.Renew("explicit expired certificate recovery")
	if e != nil || renewed.SPKI != expiredLocal.SPKI {
		t.Fatal("recovery replaced private identity", e)
	}
	if store, e := Open(b.home); e != nil {
		t.Fatal("recovered identity unavailable", e)
	} else {
		store.Close()
	}
}

func TestPairingProofAndActivationRetriesAreIdempotent(t *testing.T) {
	a := testStore(t, "a", "alpha", "127.0.0.1:40001")
	b := testStore(t, "b", "beta", "127.0.0.1:40002")
	grant := Grant{Permissions: []string{"observe-host"}}
	trust(t, a, b, grant)
	before := len(a.Config().History)
	pair := strings.Repeat("12", 32)
	if _, e := a.Pair(b.Config().Identity, grant, pair, "test explicit pairing"); e != nil {
		t.Fatal(e)
	}
	if e := a.Prove(b.Config().Identity.Host, pair); e != nil {
		t.Fatal("completed pairing cannot be recovered by exact retry", e)
	}
	if e := a.Activate(b.Config().Identity.Host, pair); e != nil {
		t.Fatal(e)
	}
	if len(a.Config().History) != before {
		t.Fatal("completed retry duplicated trust history")
	}
}
