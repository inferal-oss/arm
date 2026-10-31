package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/meshforward"
	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
)

// controlledRawClose models the first kernel Close result while closing a real
// pipe FD. A later Close succeeds, so a second-call proof would miss the fault.
type controlledRawClose struct {
	net.Conn
	mu    sync.Mutex
	calls int
	fault error
}

func (c *controlledRawClose) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	_ = c.Conn.Close()
	if c.calls == 1 {
		return c.fault
	}
	return nil
}

type controlledAdmittedCarrier struct {
	net.Conn
	raw            *controlledRawClose
	applicationErr error
	once           sync.Once
	nativeErr      error
}

func (c *controlledAdmittedCarrier) Close() error {
	c.once.Do(func() { c.nativeErr = c.raw.Close() })
	return errors.Join(c.nativeErr, c.applicationErr)
}

func (c *controlledAdmittedCarrier) NativeCloseProof() error {
	_ = c.Close()
	return c.nativeErr
}

func forwardAdmittedTestCert(t *testing.T, name string) (tls.Certificate, *x509.Certificate, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name + ".arm.invalid"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private, Leaf: parsed}, parsed, hex.EncodeToString(sum[:])
}

func TestForwardUnixAdmittedOriginalCloseReachesManagerDebt(t *testing.T) {
	for _, tc := range []struct {
		name                string
		native, application bool
	}{{"native_close_uncertain", true, false}, {"application_close_known_native", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := shortForwardMeshFixture(t)
			s.Manager.Start()
			deadline := time.Now().Add(5 * time.Second)
			for {
				status, err := s.Manager.Status()
				if err != nil {
					t.Fatal(err)
				}
				if status.CPU != nil && status.CPU.Known && status.CPU.Available >= .01 && status.Memory != nil && !status.Memory.Stale && status.Memory.Available >= 1<<20 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("native source capacity unavailable")
				}
				time.Sleep(20 * time.Millisecond)
			}
			sourceCert, sourceParsed, sourcePin := forwardAdmittedTestCert(t, "source")
			targetCert, targetParsed, targetPin := forwardAdmittedTestCert(t, "target")
			sourceRoots, targetRoots := x509.NewCertPool(), x509.NewCertPool()
			sourceRoots.AddCert(sourceParsed)
			targetRoots.AddCert(targetParsed)
			protocol := "arm.workspace.forward.v1"
			clientTLS := &tls.Config{Certificates: []tls.Certificate{sourceCert}, RootCAs: targetRoots, ServerName: "target.arm.invalid", MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{protocol}}
			serverTLS := &tls.Config{Certificates: []tls.Certificate{targetCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: sourceRoots, MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, NextProtos: []string{protocol}}
			binding := model.ForwardSourceBinding{Workspace: strings.Repeat("a", 32), Target: strings.Repeat("b", 32), Service: "echo", GrantRevision: 1, Bind: "127.0.0.1:0", TokenSHA256: strings.Repeat("c", 64)}
			q := meshforward.Request{Version: 1, Workspace: binding.Workspace, Generation: strings.Repeat("d", 32), Source: strings.Repeat("e", 32), Target: binding.Target, SourcePin: sourcePin, TargetPin: targetPin, Service: binding.Service, GrantRevision: binding.GrantRevision, ConnectionID: local.ID(), TTL: time.Minute, RouteBindingHash: strings.Repeat("f", 64)}
			if err := q.Validate(); err != nil {
				t.Fatal(err)
			}
			joined, childJoined := make(chan struct{}), make(chan struct{})
			finishSource, finishChild := forwardUnixTestCallerJoins(t, joined, childJoined)
			h, err := s.Manager.AcquireForwardListener(context.Background(), local.ID(), "actual admitted Unix remote close", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined)
			if err != nil {
				t.Fatal(err)
			}
			child, err := h.IssueUnixChild(context.Background(), childJoined)
			if err != nil {
				t.Fatal(err)
			}
			pair, err := child.IssueStoragePair(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			r := &forwardSource{owner: &meshService{owner: s}, handle: h, binding: binding, authority: forwardSourceAuthority{Generation: q.Generation, Origin: workspace.Identity{Host: q.Source, Fingerprint: q.SourcePin}, Destination: workspace.Identity{Host: q.Target, Fingerprint: q.TargetPin}}}
			w, err := r.startPrivateUnixWorker(context.Background(), pair)
			if err != nil {
				t.Fatal(err)
			}
			track := forwardUnixTestWorkerCleanup(t, w)
			nativeFault := errors.New("first original remote raw close uncertain")
			applicationFault := errors.New("TLS application wrapper close failed")
			admitted := make(chan *controlledAdmittedCarrier, 1)
			serverDone := make(chan error, 1)
			w.open = func(ctx context.Context) (*meshforward.AdmittedRoute, error) {
				clientRaw, serverRaw := net.Pipe()
				track(clientRaw)
				track(serverRaw)
				raw := &controlledRawClose{Conn: clientRaw}
				if tc.native {
					raw.fault = nativeFault
				}
				carrier := &controlledAdmittedCarrier{Conn: raw, raw: raw}
				if tc.application {
					carrier.applicationErr = applicationFault
				}
				client := tls.Client(carrier, clientTLS)
				server := tls.Server(serverRaw, serverTLS)
				_ = client.SetDeadline(time.Now().Add(5 * time.Second))
				_ = server.SetDeadline(time.Now().Add(5 * time.Second))
				go func() {
					defer server.Close()
					if err := server.Handshake(); err != nil {
						serverDone <- err
						return
					}
					got, err := meshforward.ReadRequest(server)
					if err == nil && got != q {
						err = errors.New("destination request changed")
					}
					if err == nil {
						err = meshforward.WriteAccepted(server, q)
					}
					if err == nil {
						_, err = io.Copy(io.Discard, server)
					}
					serverDone <- err
				}()
				if err := client.HandshakeContext(ctx); err != nil {
					_ = carrier.Close()
					return nil, err
				}
				if err := meshforward.WriteRequest(client, q); err != nil {
					_ = carrier.Close()
					return nil, err
				}
				route, err := meshforward.ReadAdmittedRoute(client, q)
				if err != nil {
					_ = carrier.Close()
					return nil, err
				}
				_ = client.SetDeadline(time.Time{})
				admitted <- carrier
				return route, nil
			}
			localConn, err := net.DialTimeout("unix", findForwardUnixSocket(t, s, h.LeaseID()), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			track(localConn)
			var carrier *controlledAdmittedCarrier
			select {
			case carrier = <-admitted:
			case <-time.After(5 * time.Second):
				t.Fatal("pinned TLS and exact destination ACK did not complete")
			}
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closeErr := w.Close(closeCtx)
			carrier.raw.mu.Lock()
			rawCalls := carrier.raw.calls
			carrier.raw.mu.Unlock()
			if rawCalls != 1 {
				t.Fatalf("original raw socket closed %d times", rawCalls)
			}
			select {
			case <-serverDone:
			case <-time.After(5 * time.Second):
				t.Fatal("destination TLS worker did not join")
			}
			status, err := s.Manager.Status()
			if err != nil {
				t.Fatal(err)
			}
			var sourceState, storageState string
			for _, lease := range status.Leases {
				if lease.ID == h.LeaseID() {
					sourceState = lease.State
				}
				if lease.Request.Kind == "storage" && lease.Request.Reason == "Forward Unix source directory for "+h.LeaseID() {
					storageState = lease.State
				}
			}
			db, err := sql.Open("sqlite", filepath.Join(s.home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var phase string
			var terminal sql.NullString
			if err := db.QueryRow(`SELECT phase,json_extract(data,'$.native_terminal') FROM forward_unix_pairs WHERE source_id=?`, h.LeaseID()).Scan(&phase, &terminal); err != nil {
				t.Fatal(err)
			}
			w.mu.Lock()
			var currentPair *forwardUnixWorkerPair
			for p := range w.active {
				currentPair = p
			}
			w.mu.Unlock()
			forwardUnixProcess.Lock()
			_, workerHeld := forwardUnixProcess.workers[w]
			_, pairHeld := forwardUnixProcess.pairs[currentPair]
			forwardUnixProcess.Unlock()
			if tc.native {
				if !errors.Is(closeErr, nativeFault) || sourceState != "needs_attention" || storageState != "active" || phase != "bound" || terminal.Valid || !workerHeld || !pairHeld {
					t.Fatalf("native debt escaped: close=%v source=%q storage=%q phase=%q terminal=%v worker=%t pair=%t", closeErr, sourceState, storageState, phase, terminal, workerHeld, pairHeld)
				}
				finishChild()
				finishSource()
			} else {
				if closeErr != nil || sourceState != "active" || storageState != "terminal" || phase != "terminal" || !terminal.Valid || workerHeld || pairHeld {
					t.Fatalf("known native close retained debt: close=%v source=%q storage=%q phase=%q terminal=%v worker=%t pair=%t", closeErr, sourceState, storageState, phase, terminal, workerHeld, pairHeld)
				}
				finishChild()
				if err := child.FinishAfterJoin(); err != nil {
					t.Fatal(err)
				}
				finishSource()
				if _, err := h.FinishAfterJoin(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
