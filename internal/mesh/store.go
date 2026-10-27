package mesh

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"math"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

type Grant struct {
	Contacts      []string        `json:"contact_ids"`
	OperatorInbox bool            `json:"operator_inbox"`
	Permissions   []string        `json:"permissions"`
	Limits        model.Resources `json:"limits"`
	MaxTTL        time.Duration   `json:"max_ttl_ns"`
}

var permissions = []string{"observe-host", "observe-own", "observe-all", "lease-own", "run", "message", "contact-read", "ssh-exec", "screen-view", "node-stop", "node-uninstall"}

func (g Grant) Validate() error {
	if len(g.Contacts) > 64 {
		return fmt.Errorf("too many peer contacts")
	}
	for _, id := range g.Contacts {
		if id == "" || len(id) > 128 {
			return fmt.Errorf("invalid peer contact ID")
		}
	}
	if len(g.Permissions) > len(permissions) {
		return fmt.Errorf("too many grants")
	}
	seen := map[string]bool{}
	for _, p := range g.Permissions {
		if !slices.Contains(permissions, p) || seen[p] {
			return fmt.Errorf("unknown/duplicate mesh grant %q", p)
		}
		seen[p] = true
	}
	if math.IsNaN(g.Limits.CPU) || math.IsInf(g.Limits.CPU, 0) || g.Limits.CPU < 0 || g.Limits.CPU > 65536 || g.Limits.Memory < 0 || g.Limits.Disk < 0 || g.MaxTTL < 0 || g.MaxTTL > 7*24*time.Hour {
		return fmt.Errorf("invalid mesh ceilings")
	}
	if g.Has("lease-own") && (g.MaxTTL <= 0 || g.Limits == (model.Resources{})) {
		return fmt.Errorf("lease-own requires explicit positive ceilings and TTL")
	}
	if g.Has("ssh-exec") && !g.Has("lease-own") {
		return fmt.Errorf("ssh-exec requires lease-own")
	}
	if g.Has("run") && !g.Has("lease-own") {
		return fmt.Errorf("run requires lease-own")
	}
	return nil
}
func (g Grant) Has(p string) bool { return slices.Contains(g.Permissions, p) }

type Peer struct {
	Identity Identity  `json:"identity"`
	Grant    Grant     `json:"grant"`
	Pair     string    `json:"pair"`
	State    string    `json:"state"`
	Deadline time.Time `json:"deadline"`
	Proven   bool      `json:"proven"`
	Revision int       `json:"revision"`
	Reason   string    `json:"reason"`
}
type TrustEvent struct {
	SPKI         string    `json:"spki_sha256"`
	ReviewDigest string    `json:"reviewed_identity_grant_sha256"`
	At           time.Time `json:"at"`
	Actor        string    `json:"actor"`
	Action       string    `json:"action"`
	Host         string    `json:"host"`
	Pair         string    `json:"pair"`
	Fingerprint  string    `json:"fingerprint"`
	Grant        Grant     `json:"grant"`
	Reason       string    `json:"reason"`
	Revision     int       `json:"revision"`
}
type Config struct {
	History     []TrustEvent        `json:"history"`
	Identity    Identity            `json:"identity"`
	Listen      string              `json:"listen"`
	Enabled     *bool               `json:"listener_enabled,omitempty"`
	Managed     bool                `json:"automatic_listener,omitempty"`
	Disconnects []DisconnectReceipt `json:"disconnect_receipts,omitempty"`
	Activation  *ListenerActivation `json:"listener_activation,omitempty"`
	Peers       []Peer              `json:"peers"`
}

func (c Config) ListenerEnabled() bool { return c.Enabled == nil || *c.Enabled }

type Store struct {
	trustMu   sync.Mutex
	mu        sync.RWMutex
	home      string
	config    Config
	cert      tls.Certificate
	directory *os.File
	closed    bool
	changed   func(string)
	revoking  sync.Map
}

func Open(home string) (*Store, error)         { return open(home, false) }
func OpenRecovery(home string) (*Store, error) { return open(home, true) }
func open(home string, recovery bool) (*Store, error) {
	if e := local.Prepare(home); e != nil {
		return nil, e
	}
	dir := filepath.Join(home, "mesh")
	if _, e := os.Lstat(dir); e != nil {
		return nil, e
	}
	if e := local.Prepare(dir); e != nil {
		return nil, e
	}
	raw, e := privateFile(filepath.Join(dir, "config.json"))
	if e != nil {
		return nil, e
	}
	var c Config
	if e = Decode(raw, &c); e != nil {
		return nil, e
	}
	if _, e = parseIdentity(c.Identity, !recovery); e != nil {
		return nil, e
	}
	if e = validateListen(c.Listen); e != nil {
		return nil, e
	}
	if e = validateStoredPeerRoster(c); e != nil {
		return nil, e
	}
	cert, e := loadCertificate(home, c.Identity)
	if e != nil {
		return nil, e
	}
	dirFD, e := directory(dir)
	if e != nil {
		return nil, e
	}
	return &Store{home: home, config: c, cert: cert, directory: dirFD}, nil
}
func validateListen(address string) error {
	h, p, e := net.SplitHostPort(address)
	n, pe := strconv.Atoi(p)
	if e != nil || pe != nil || n < 1 || n > 65535 || net.ParseIP(h) == nil || h == "0.0.0.0" || h == "::" {
		return fmt.Errorf("mesh listener requires explicit non-wildcard literal IP and port")
	}
	return nil
}
func Init(home, host, name, listen, address string) (*Store, error) {
	return initListener(home, host, name, listen, address, true)
}

func InitPrepared(home, host, name, listen, address string) (*Store, error) {
	return initListener(home, host, name, listen, address, false)
}

func initListener(home, host, name, listen, address string, enabled bool) (*Store, error) {
	if e := validateListen(listen); e != nil {
		return nil, e
	}
	if e := local.Prepare(home); e != nil {
		return nil, e
	}
	dir := filepath.Join(home, "mesh")
	if _, e := os.Lstat(dir); e == nil {
		return nil, fmt.Errorf("mesh already initialized; preserve identity and inspect configuration")
	}
	identity, key, e := generate(host, name, address)
	if e != nil {
		return nil, e
	}
	if e = os.Mkdir(dir, 0700); e != nil {
		return nil, e
	}
	fd, e := directory(dir)
	if e != nil {
		return nil, e
	}
	e = writePrivate(fd, "node-key.pem", key, true)
	fd.Close()
	if e != nil {
		return nil, e
	}
	if e = save(filepath.Join(dir, "config.json"), Config{Identity: identity, Listen: listen, Enabled: &enabled, Managed: !enabled, Peers: []Peer{}}); e != nil {
		return nil, e
	}
	return Open(home)
}
func (s *Store) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, _ := json.Marshal(s.config)
	var c Config
	_ = json.Unmarshal(raw, &c)
	return c
}
func (s *Store) OnChange(fn func(string)) { s.mu.Lock(); defer s.mu.Unlock(); s.changed = fn }
func (s *Store) update(fn func(*Config) error) error {
	if s.closed {
		return fmt.Errorf("mesh store is closed")
	}
	next := s.config
	next.Peers = slices.Clone(next.Peers)
	next.History = slices.Clone(next.History)
	next.Disconnects = slices.Clone(next.Disconnects)
	if e := fn(&next); e != nil {
		return e
	}
	if e := s.persist(next); e != nil {
		return e
	}
	s.config = next
	return nil
}
func (s *Store) Pair(identity Identity, grant Grant, pair, reason string) (Peer, error) {
	return s.PairReviewed(identity, grant, pair, reason, false)
}
func (s *Store) PairReviewed(identity Identity, grant Grant, pair, reason string, replaceRevoked bool, expectedRevision ...int) (Peer, error) {
	s.trustMu.Lock()
	defer s.trustMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	var result Peer
	if _, e := ValidateIdentity(identity); e != nil {
		return result, e
	}
	if e := grant.Validate(); e != nil {
		return result, e
	}
	pairBytes, pairErr := hex.DecodeString(pair)
	if pairErr != nil || len(pairBytes) != 32 || len(reason) == 0 || len(reason) > 4096 {
		return result, fmt.Errorf("pair requires 256-bit pairing ID and reason")
	}
	e := s.update(func(c *Config) error {
		if identity.Host == c.Identity.Host || identity.Name == c.Identity.Name {
			return fmt.Errorf("peer identity/name collides with localhost")
		}
		for index, p := range c.Peers {
			if p.Identity.Host == identity.Host {
				if p.Pair == pair && p.Identity == identity {
					a, _ := json.Marshal(p.Grant)
					b, _ := json.Marshal(grant)
					if string(a) == string(b) {
						result = p
						return nil
					}
				}
				if len(expectedRevision) > 0 && p.Revision != expectedRevision[0] {
					return fmt.Errorf("peer revision changed since trust review")
				}
				if replaceRevoked && p.State == "revoked" {
					result = Peer{Identity: identity, Grant: cloneGrant(grant), Pair: pair, State: "pending", Deadline: time.Now().UTC().Add(5 * time.Minute), Revision: p.Revision + 1, Reason: reason}
					c.Peers[index] = result
					return audit(c, "repaired-pending", result, "local:"+local.CurrentIdentity().UID)
				}
				return fmt.Errorf("peer already registered; revoke/inspect instead of replacing its identity or grants")
			}
		}
		if len(expectedRevision) > 0 && expectedRevision[0] != 0 {
			return fmt.Errorf("reviewed peer disappeared before pairing")
		}
		if len(c.Peers) >= 64 {
			return fmt.Errorf("peer limit reached")
		}
		result = Peer{Identity: identity, Grant: cloneGrant(grant), Pair: pair, State: "pending", Deadline: time.Now().UTC().Add(5 * time.Minute), Revision: 1, Reason: reason}
		c.Peers = append(c.Peers, result)
		return audit(c, "paired-pending", result, "local:"+local.CurrentIdentity().UID)
	})
	if e == nil && replaceRevoked {
		s.revoking.Delete(identity.Host)
	}
	return clonePeer(result), e
}
func (s *Store) Resolve(target string) (Peer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return resolve(s.config, target)
}
func resolve(c Config, target string) (Peer, error) {
	var found []Peer
	for _, p := range c.Peers {
		if p.Identity.Host == target {
			return clonePeer(p), nil
		}
		if p.Identity.Name == target {
			found = append(found, p)
		}
	}
	if target == "localhost" || target == c.Identity.Host || target == c.Identity.Name {
		return Peer{}, model.Err("ambiguous_host", "target resolves to this host; use localhost explicitly")
	}
	if len(found) != 1 {
		return Peer{}, model.Err("host_unavailable", "unknown or ambiguous peer name; use its stable host ID")
	}
	return clonePeer(found[0]), nil
}
func (s *Store) peerByCert(raw []byte) (Peer, error) {
	if s.closed {
		return Peer{}, fmt.Errorf("mesh store closed")
	}
	for _, p := range s.config.Peers {
		cert, e := ValidateIdentity(p.Identity)
		_, blocked := s.revoking.Load(p.Identity.Host)
		if !blocked && e == nil && string(cert.Raw) == string(raw) && p.State != "revoked" && (p.State == "active" || time.Now().Before(p.Deadline)) {
			return clonePeer(p), nil
		}
	}
	return Peer{}, fmt.Errorf("untrusted, expired or revoked node")
}
func (s *Store) PeerByCert(raw []byte) (Peer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.peerByCert(raw)
}
func (s *Store) Prove(host, pair string) error {
	s.trustMu.Lock()
	defer s.trustMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.config.Peers {
		if p.Identity.Host == host && p.Pair == pair && p.Proven && (p.State == "active" || (p.State == "pending" && time.Now().Before(p.Deadline))) {
			return nil
		}
	}
	return s.update(func(c *Config) error {
		for i, p := range c.Peers {
			if p.Identity.Host == host && p.Pair == pair && p.State == "pending" && time.Now().Before(p.Deadline) {
				c.Peers[i].Proven = true
				return audit(c, "proof", c.Peers[i], "peer:"+host)
			}
		}
		return fmt.Errorf("pair not pending")
	})
}
func (s *Store) Activate(host, pair string) error {
	s.trustMu.Lock()
	defer s.trustMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(func(c *Config) error {
		for i, p := range c.Peers {
			if p.Identity.Host == host && p.Pair == pair {
				if p.State == "active" {
					return nil
				}
				if p.State == "pending" && p.Proven && time.Now().Before(p.Deadline) {
					c.Peers[i].State = "active"
					c.Peers[i].Revision++
					return audit(c, "activated", c.Peers[i], "local:"+local.CurrentIdentity().UID)
				}
			}
		}
		return fmt.Errorf("pair proof expired or absent")
	})
}
func (s *Store) Revoke(target, reason string) error {
	s.trustMu.Lock()
	defer s.trustMu.Unlock()
	peer, e := s.Resolve(target)
	if e != nil {
		return e
	}
	if reason == "" || len(reason) > 4096 {
		return fmt.Errorf("reason required")
	}
	s.revoking.Store(peer.Identity.Host, true)
	s.mu.RLock()
	changed := s.changed
	s.mu.RUnlock()
	if changed != nil {
		changed(peer.Identity.Host)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update(func(c *Config) error {
		for i, p := range c.Peers {
			if p.Identity.Host == peer.Identity.Host {
				c.Peers[i].State = "revoked"
				c.Peers[i].Revision++
				c.Peers[i].Reason = reason
				return audit(c, "revoked", c.Peers[i], "local:"+local.CurrentIdentity().UID)
			}
		}
		return fmt.Errorf("peer missing")
	})
}

func (s *Store) ServerTLS() *tls.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	base := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{s.cert}, ClientAuth: tls.RequireAndVerifyClientCert, SessionTicketsDisabled: true, NextProtos: []string{"http/1.1"}}
	base.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		c := base.Clone()
		c.GetConfigForClient = nil
		c.ClientCAs = x509.NewCertPool()
		for _, p := range s.config.Peers {
			if p.State != "revoked" {
				if cert, e := ValidateIdentity(p.Identity); e == nil {
					c.ClientCAs.AddCert(cert)
				}
			}
		}
		return c, nil
	}
	base.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) != 1 || cs.NegotiatedProtocol != "http/1.1" {
			return fmt.Errorf("invalid mesh TLS identity/protocol")
		}
		p, e := s.PeerByCert(cs.PeerCertificates[0].Raw)
		if e != nil {
			return e
		}
		return cs.PeerCertificates[0].VerifyHostname(SAN(p.Identity.Host))
	}
	return base
}
func (s *Store) ClientTLS(peer Peer) (*tls.Config, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, fmt.Errorf("mesh store closed")
	}
	cert, e := ValidateIdentity(peer.Identity)
	if e != nil {
		return nil, e
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{s.cert}, RootCAs: roots, ServerName: SAN(peer.Identity.Host), SessionTicketsDisabled: true, NextProtos: []string{"http/1.1"}, VerifyConnection: func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) != 1 || cs.NegotiatedProtocol != "http/1.1" || string(cs.PeerCertificates[0].Raw) != string(cert.Raw) {
			return fmt.Errorf("peer certificate mismatch")
		}
		_, e := s.PeerByCert(cert.Raw)
		return e
	}}, nil
}

func cloneGrant(g Grant) Grant {
	g.Contacts = slices.Clone(g.Contacts)
	g.Permissions = slices.Clone(g.Permissions)
	return g
}
func clonePeer(p Peer) Peer { p.Grant = cloneGrant(p.Grant); return p }

func audit(c *Config, action string, p Peer, actor string) error {
	if len(c.History) >= 1024 {
		return fmt.Errorf("trust journal full; preserve and inspect before additional changes")
	}
	reviewed, _ := json.Marshal(struct {
		Identity     Identity
		Grant        Grant
		Pair, Reason string
	}{p.Identity, p.Grant, p.Pair, p.Reason})
	digest := sha256.Sum256(reviewed)
	c.History = append(c.History, TrustEvent{SPKI: p.Identity.SPKI, ReviewDigest: hex.EncodeToString(digest[:]), At: time.Now().UTC(), Actor: actor, Action: action, Host: p.Identity.Host, Pair: p.Pair, Fingerprint: p.Identity.Fingerprint, Grant: cloneGrant(p.Grant), Reason: p.Reason, Revision: p.Revision})
	return nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.directory != nil {
		return s.directory.Close()
	}
	return nil
}

func (s *Store) persist(c Config) error {
	raw, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	if len(raw)+1 > 1<<20 {
		return fmt.Errorf("mesh config/journal exceeds 1MiB bound")
	}
	current, e := directory(filepath.Join(s.home, "mesh"))
	if e != nil {
		return e
	}
	a, e := current.Stat()
	current.Close()
	if e != nil {
		return e
	}
	b, e := s.directory.Stat()
	if e != nil || !os.SameFile(a, b) {
		return fmt.Errorf("mesh directory identity changed; no trust mutation committed")
	}
	return writePrivate(s.directory, "config.json", append(raw, '\n'), false)
}
func (s *Store) Renew(reason string) (Identity, error) {
	s.trustMu.Lock()
	defer s.trustMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if reason == "" || len(reason) > 4096 {
		return Identity{}, fmt.Errorf("renewal requires reason")
	}
	identity, e := renewIdentity(s.config.Identity, s.cert)
	if e != nil {
		return identity, e
	}
	e = s.update(func(c *Config) error {
		for index, p := range c.Peers {
			if p.State != "revoked" {
				c.Peers[index].State = "revoked"
				c.Peers[index].Revision++
				c.Peers[index].Reason = "local certificate renewed: " + reason
				if e := audit(c, "revoked-for-local-renewal", c.Peers[index], "local:"+local.CurrentIdentity().UID); e != nil {
					return e
				}
			}
		}
		previous := c.Identity.Fingerprint
		c.Identity = identity
		return audit(c, "local-certificate-renewed", Peer{Identity: identity, Reason: reason + "; previous certificate " + previous}, "local:"+local.CurrentIdentity().UID)
	})
	if e != nil {
		return identity, e
	}
	s.cert, e = loadCertificate(s.home, identity)
	return identity, e
}

// validateStoredPeerRoster is shared by live loading and offline original-key
// maintenance; ambiguous grants never authorize an outliving native helper.
func validateStoredPeerRoster(c Config) error {
	seen := map[string]bool{}
	for _, p := range c.Peers {
		// Expired peers remain inspectable and cannot authenticate. Do not disable healthy peers.
		if p.Identity.Host == c.Identity.Host || seen[p.Identity.Host] {
			return fmt.Errorf("duplicate/self peer")
		}
		seen[p.Identity.Host] = true
		if e := p.Grant.Validate(); e != nil {
			return e
		}
	}
	return nil
}
