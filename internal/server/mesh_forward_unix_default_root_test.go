package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/meshforward"
	"getarm.org/internal/model"
	"getarm.org/internal/workspace"
)

func forwardUnixTestChangeDurablePlacement(t *testing.T, s *Server, id string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(s.home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow(`SELECT data FROM leases WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var lease model.Lease
	if err := json.Unmarshal([]byte(raw), &lease); err != nil || lease.StorageBinding != nil || lease.Request.StoragePool != "" {
		t.Fatal("default-root lease unavailable", err)
	}
	lease.StorageBinding = &model.StoragePoolBinding{}
	changed, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE leases SET data=? WHERE id=?`, string(changed), id); err != nil {
		t.Fatal("same-revision placement mutation unavailable", err)
	}
	current, err := s.Manager.Inspect(id)
	if err != nil || current.Revision != lease.Revision || current.StorageBinding == nil {
		t.Fatal("same-revision placement did not remain live", err)
	}
}

func TestForwardUnixAdmittedPlacementChangeDeliversNoLocalBytes(t *testing.T) {
	for _, role := range []string{"source", "storage"} {
		t.Run(role, func(t *testing.T) {
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
			forwardUnixTestCallerJoins(t, joined, childJoined)
			h, err := s.Manager.AcquireForwardListener(context.Background(), local.ID(), "private Unix placement refusal", binding, time.Minute, model.Resources{CPU: .01, Memory: 1 << 20}, joined)
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
			if err := w.bound.CheckBeforeApplicationBytes(context.Background()); err != nil {
				t.Fatal("default-root positive application gate refused", err)
			}
			admitted := make(chan struct{}, 1)
			release := make(chan struct{})
			serverBytes := make(chan int64, 1)
			w.open = func(ctx context.Context) (*meshforward.AdmittedRoute, error) {
				clientRaw, serverRaw := net.Pipe()
				track(clientRaw)
				track(serverRaw)
				client := tls.Client(&controlledAdmittedCarrier{Conn: clientRaw, raw: &controlledRawClose{Conn: clientRaw}}, clientTLS)
				server := tls.Server(serverRaw, serverTLS)
				_ = client.SetDeadline(time.Now().Add(5 * time.Second))
				_ = server.SetDeadline(time.Now().Add(5 * time.Second))
				go func() {
					defer server.Close()
					if err := server.Handshake(); err != nil {
						serverBytes <- -1
						return
					}
					got, err := meshforward.ReadRequest(server)
					if err != nil || got != q || meshforward.WriteAccepted(server, q) != nil {
						serverBytes <- -1
						return
					}
					n, _ := io.Copy(io.Discard, server)
					serverBytes <- n
				}()
				if err := client.HandshakeContext(ctx); err != nil {
					_ = client.Close()
					return nil, err
				}
				if err := meshforward.WriteRequest(client, q); err != nil {
					_ = client.Close()
					return nil, err
				}
				route, err := meshforward.ReadAdmittedRoute(client, q)
				if err != nil {
					_ = client.Close()
					return nil, err
				}
				_ = client.SetDeadline(time.Time{})
				admitted <- struct{}{}
				select {
				case <-release:
					return route, nil
				case <-ctx.Done():
					_ = client.Close()
					return nil, ctx.Err()
				}
			}
			localConn, err := net.DialTimeout("unix", findForwardUnixSocket(t, s, h.LeaseID()), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			track(localConn)
			if _, err := localConn.Write([]byte("unread local application bytes")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-admitted:
			case <-time.After(5 * time.Second):
				t.Fatal("pinned TLS and exact destination ACK did not complete")
			}
			if err := w.bound.CheckBeforeApplicationBytes(context.Background()); err != nil {
				t.Fatal("unchanged placement refused at the admitted-route cut", err)
			}
			status, err := s.Manager.Status()
			if err != nil {
				t.Fatal(err)
			}
			charged := make(map[string]model.Lease, 2)
			storageID := ""
			for _, lease := range status.Leases {
				if lease.ID == h.LeaseID() {
					charged[lease.ID] = lease
				}
				if lease.Request.Kind == "storage" && lease.Request.Reason == "Forward Unix source directory for "+h.LeaseID() {
					if storageID != "" {
						t.Fatal("multiple charged Unix storage originals")
					}
					storageID = lease.ID
					charged[lease.ID] = lease
				}
			}
			if storageID == "" || len(charged) != 2 {
				t.Fatal("charged forwarding originals unavailable")
			}
			id := h.LeaseID()
			if role == "storage" {
				id = storageID
			}
			forwardUnixTestChangeDurablePlacement(t, s, id)
			placementErr := w.bound.CheckBeforeApplicationBytes(context.Background())
			var placementModelErr *model.Error
			if !errors.As(placementErr, &placementModelErr) {
				t.Fatal("changed placement did not fail the final local gate", placementErr)
			}
			expectedCode := "lease_inactive"
			if role == "storage" {
				expectedCode = "forward_unix_pair_unavailable"
			}
			if placementModelErr.Code != expectedCode {
				t.Fatalf("changed placement failed for %q, want %q", placementModelErr.Code, expectedCode)
			}
			close(release)
			select {
			case n := <-serverBytes:
				if n != 0 {
					t.Fatalf("changed placement delivered %d local bytes", n)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("destination reader did not join after placement refusal")
			}
			applicationErr := w.ApplicationError()
			var applicationModelErr *model.Error
			if !errors.As(applicationErr, &applicationModelErr) || applicationModelErr.Code != placementModelErr.Code || applicationModelErr.Message != placementModelErr.Message || strings.Contains(applicationErr.Error(), "forbidden:") {
				t.Fatal("worker refusal did not match the placement gate", applicationErr)
			}
			// The intentionally invalid binding makes aggregate accounting reject
			// Status. Read only the two original physical rows instead.
			db, err := sql.Open("sqlite", filepath.Join(s.home, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			for _, chargedID := range []string{h.LeaseID(), storageID} {
				var raw string
				if err := db.QueryRow(`SELECT data FROM leases WHERE id=?`, chargedID).Scan(&raw); err != nil {
					t.Fatal("charged forwarding original unavailable", err)
				}
				var lease model.Lease
				if err := json.Unmarshal([]byte(raw), &lease); err != nil {
					t.Fatal("charged forwarding original unreadable", err)
				}
				before := charged[chargedID]
				if lease.ID != chargedID || lease.Request.Resources != before.Request.Resources || lease.Revision != before.Revision || lease.Ended != nil || (lease.State != "active" && lease.State != "needs_attention") {
					t.Fatalf("changed placement released original forwarding charge %s", chargedID)
				}
			}
		})
	}
}
