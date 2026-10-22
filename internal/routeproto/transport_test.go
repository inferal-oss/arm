package routeproto

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const (
	outerPath     = "/route-prototype/v1"
	outerProtocol = "arm-route-prototype.v1"
	innerProtocol = "arm-route-prototype/1"
	maxFrame      = 64 << 10
	maxEnvelope   = 60 << 10
)

type identity struct {
	host string
	cert tls.Certificate
	leaf *x509.Certificate
}

func newIdentity(t *testing.T, host string, before, after time.Time) identity {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: host}, DNSNames: []string{san(host)}, NotBefore: before, NotAfter: after, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return identity{host, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, leaf}
}

func san(host string) string { return "node-" + host + ".arm.invalid" }

func policy(local, peer identity, server bool, protocol string) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(peer.leaf)
	pin, key := sha256.Sum256(peer.leaf.Raw), sha256.Sum256(peer.leaf.RawSubjectPublicKeyInfo)
	c := &tls.Config{Certificates: []tls.Certificate{local.cert}, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{protocol}, SessionTicketsDisabled: true}
	if server {
		c.ClientAuth = tls.RequireAndVerifyClientCert
		c.ClientCAs = pool
	} else {
		c.RootCAs = pool
		c.ServerName = san(peer.host)
	}
	c.VerifyConnection = func(s tls.ConnectionState) error {
		if s.Version != tls.VersionTLS13 || s.DidResume || s.NegotiatedProtocol != protocol || len(s.VerifiedChains) == 0 || len(s.PeerCertificates) != 1 {
			return errors.New("protocol or verified chain mismatch")
		}
		p := s.PeerCertificates[0]
		if sha256.Sum256(p.Raw) != pin || sha256.Sum256(p.RawSubjectPublicKeyInfo) != key || len(p.DNSNames) != 1 || p.DNSNames[0] != san(peer.host) {
			return errors.New("exact member pin/SAN mismatch")
		}
		return nil
	}
	return c
}

type streamBounds struct {
	frame          int64
	bytes          int64
	idle, lifetime time.Duration
}

func defaultBounds() streamBounds {
	return streamBounds{maxFrame, 1 << 20, time.Second, 3 * time.Second}
}

type stream struct {
	net.Conn
	ws              *wire
	cancel          context.CancelFunc
	once            sync.Once
	readMu, writeMu sync.Mutex
	read, written   int64
	limit           int64
	frame           int
	start           time.Time
	last            atomic.Int64
	done            chan struct{}
}

func bounded(ctx context.Context, ws *wire, b streamBounds) *stream {
	if b.frame < 2 || b.bytes < 1 || b.idle <= 0 || b.lifetime <= 0 {
		panic("invalid prototype bounds")
	}
	ctx, cancel := context.WithTimeout(ctx, b.lifetime)
	// NetConn resets SetReadLimit(-1). Set the limit AFTER constructing it.
	base := websocket.NetConn(ctx, ws.Conn, websocket.MessageBinary)
	// The maintained reader consumes one lookahead byte when checking its
	// message limit. Reserve it: valid payload<=frame-1, delivered<=frame.
	ws.SetReadLimit(b.frame - 1)
	s := &stream{Conn: base, ws: ws, cancel: cancel, limit: b.bytes, frame: int(b.frame - 1), start: time.Now(), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		tick := time.NewTicker(max(time.Millisecond, min(20*time.Millisecond, b.idle/4)))
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = s.Close()
				return
			case <-tick.C:
				if time.Since(s.start)-time.Duration(s.last.Load()) >= b.idle {
					_ = s.Close()
					return
				}
			}
		}
	}()
	return s
}
func (s *stream) Close() error {
	s.once.Do(func() { s.cancel(); _ = s.ws.CloseNow(); _ = s.Conn.Close() })
	return nil
}
func (s *stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	if s.read >= s.limit {
		_ = s.Close()
		return 0, errors.New("incoming byte quota")
	}
	p = p[:min(int64(len(p)), s.limit-s.read)]
	n, e := s.Conn.Read(p)
	s.read += int64(n)
	if n > 0 {
		s.last.Store(time.Since(s.start).Nanoseconds())
	}
	return n, e
}
func (s *stream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	total := 0
	for len(p) > 0 {
		if s.written >= s.limit {
			_ = s.Close()
			return total, errors.New("outgoing byte quota")
		}
		size := min(int64(len(p)), int64(s.frame), s.limit-s.written)
		n, e := s.Conn.Write(p[:size])
		s.written += int64(n)
		total += n
		p = p[n:]
		if n > 0 {
			s.last.Store(time.Since(s.start).Nanoseconds())
		}
		if e != nil {
			return total, e
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func writeObject(c net.Conn, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(b) > maxEnvelope {
		return errors.New("envelope too large")
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(b)))
	if _, e = io.Copy(c, strings.NewReader(string(header)+string(b))); e != nil {
		return e
	}
	return nil
}
func readObject(c net.Conn, v any) error {
	var h [4]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return e
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > maxEnvelope {
		return errors.New("bounded envelope required")
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(c, b); e != nil {
		return e
	}
	if e := canonicalKeys(b, v); e != nil {
		return e
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return errors.New("single object required")
	}
	return nil
}

func dialOuter(ctx context.Context, address string, config *tls.Config) (*wire, error) {
	var socket net.Conn
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialTLSContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if network != "tcp" || target != address {
			return nil, errors.New("dial target changed")
		}
		raw, e := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if e != nil {
			return nil, e
		}
		socket = raw
		c := tls.Client(raw, config)
		if e = c.HandshakeContext(ctx); e != nil {
			raw.Close()
			return nil, e
		}
		return c, nil
	}}
	defer tr.CloseIdleConnections()
	c, _, e := websocket.Dial(ctx, "wss://"+address+outerPath, &websocket.DialOptions{HTTPClient: &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Subprotocols: []string{outerProtocol}, CompressionMode: websocket.CompressionDisabled})
	if e == nil && c.Subprotocol() != outerProtocol {
		c.CloseNow()
		return nil, fmt.Errorf("subprotocol mismatch")
	}
	if e != nil {
		return nil, e
	}
	return &wire{Conn: c, socket: socket}, nil
}

// Retain only the exact transport socket belonging to this admitted stream.
// NetConn's text-frame rejection initiates a graceful close independent of its
// read context. Force-closing this socket prevents that handshake from extending
// our configured idle/lifetime bound. It never selects or closes another socket.
type wire struct {
	*websocket.Conn
	socket net.Conn
}

func (w *wire) CloseNow() error {
	if w.socket != nil {
		_ = w.socket.Close()
	}
	return w.Conn.CloseNow()
}

type hijackCapture struct {
	http.ResponseWriter
	socket net.Conn
}

func (h *hijackCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, e := http.NewResponseController(h.ResponseWriter).Hijack()
	if e == nil {
		if c, ok := conn.(*tls.Conn); ok {
			h.socket = c.NetConn()
		} else {
			h.socket = conn
		}
	}
	return conn, rw, e
}
func acceptWire(w http.ResponseWriter, r *http.Request, opts *websocket.AcceptOptions) (*wire, error) {
	captured := &hijackCapture{ResponseWriter: w}
	conn, e := websocket.Accept(captured, r, opts)
	if e != nil {
		return nil, e
	}
	if captured.socket == nil {
		conn.CloseNow()
		return nil, errors.New("prototype must retain exact hijacked socket")
	}
	return &wire{Conn: conn, socket: captured.socket}, nil
}

// Prototype objects have flat canonical fields (path alone is an array).
// Reject duplicate/case-aliased keys before encoding/json's field matching.
func canonicalKeys(raw []byte, v any) error {
	typ := reflect.TypeOf(v).Elem()
	allowed := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		allowed[strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]] = true
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		return errors.New("object required")
	}
	seen := map[string]bool{}
	for d.More() {
		tok, e = d.Token()
		if e != nil {
			return e
		}
		k, ok := tok.(string)
		if !ok || !allowed[k] || seen[k] {
			return errors.New("noncanonical or duplicate field")
		}
		seen[k] = true
		var value json.RawMessage
		if e = d.Decode(&value); e != nil {
			return e
		}
	}
	if tok, e = d.Token(); e != nil || tok != json.Delim('}') {
		return errors.New("object end required")
	}
	if _, e = d.Token(); e != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
