package database

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/meshssh"
	"github.com/yrashk/arm/internal/model"
)

// Uses the actual public coordinator/receipt writer with deterministic setup
// callbacks. This validates producer JSON shapes and journal relationships; it
// does not claim SSH/TLS transport acceptance (covered by separate live gates).
func coordWriterFixture(t *testing.T) (*meshops.Service, meshops.StartRequest, func(), string) {
	t.Helper()
	fixture, lifecycles := coordHistoryFixture(t, "connect")
	life := lifecycles[fixture.Key]
	var review proofCoordConnectReview
	if err := strictEvidence(string(fixture.Review), &review); err != nil {
		t.Fatal(err)
	}
	var q meshops.StartRequest
	if err := strictEvidence(string(fixture.Request), &q); err != nil {
		t.Fatal(err)
	}
	own := mesh.Config{Identity: *life.LocalIdentity, Listen: life.LocalIdentity.Address, Peers: []mesh.Peer{}}
	remote := mesh.Config{Identity: *life.RemoteIdentity, Listen: life.RemoteIdentity.Address, Peers: []mesh.Peer{}}
	oi := mesh.LifecycleInspection{Protocol: 1, Version: buildinfo.Version, OS: review.Local.OS, Arch: review.Local.Arch, Home: review.Local.Home, Owner: local.CurrentIdentity(), Running: true, Host: own.Identity.Host, Identity: &own.Identity, Config: &own, Startup: mesh.DefaultStartup(), StartupSource: "saved", PrivateIPs: []string{"127.0.0.1"}}
	ri := mesh.LifecycleInspection{Protocol: 1, Version: buildinfo.Version, OS: review.Remote.OS, Arch: review.Remote.Arch, Home: review.Remote.Home, Owner: local.CurrentIdentity(), Running: true, Host: remote.Identity.Host, Identity: &remote.Identity, Config: &remote, Startup: mesh.DefaultStartup(), StartupSource: "saved", PrivateIPs: []string{"127.0.0.1"}, Route: &mesh.SSHRoute{ClientIP: "127.0.0.1", ServerIP: "127.0.0.1"}}
	assign := func(out, value any) error {
		if out == nil {
			return nil
		}
		return json.Unmarshal(coordJSON(t, value), out)
	}
	service := &meshops.Service{Home: oi.Home, Context: context.Background()}
	service.New = func(ctx context.Context) *meshops.Coordinator {
		return &meshops.Coordinator{Home: oi.Home, Context: ctx, Inspect: func() (mesh.LifecycleInspection, error) { return oi, nil }, Ensure: func(mesh.StartupPolicy) (mesh.LifecycleInspection, error) { oi.Running = true; return oi, nil }, Call: func(ctx context.Context, operation string, request, result any) error {
			switch operation {
			case "mesh_setup_prepare":
				return assign(result, mesh.PreparedSetup{Key: q.Key, Token: "private-fixture-local", Identity: own.Identity, Listen: own.Listen, Expires: time.Now().Add(time.Minute)})
			case "mesh_setup_apply", "mesh_setup_abort", "mesh_prove":
				return assign(result, map[string]bool{"ok": true})
			case "mesh_pair":
				var b meshops.Bootstrap
				if err := assign(&b, request); err != nil {
					return err
				}
				own.Peers = []mesh.Peer{{Identity: remote.Identity, Pair: b.Pair, Grant: b.Grant, State: "pending", Revision: 1}}
				return assign(result, own.Peers[0])
			case "mesh_activate":
				own.Peers[0].State = "active"
				own.Peers[0].Proven = true
				own.Peers[0].Revision = 3
				return nil
			case "mesh_peers":
				return assign(result, own.Peers)
			case "mesh_dispatch":
				return assign(result, remote.Identity)
			default:
				return fmt.Errorf("unexpected fixture operation %s", operation)
			}
		}, SSH: func(ctx context.Context, destination, binary string, request any, expected meshssh.Identity, result any) (meshssh.Identity, error) {
			identity := meshssh.Identity{Account: life.SSHAccount, HostKey: life.SSHHostKey}
			switch v := request.(type) {
			case meshops.Helper:
				switch v.Action {
				case "lifecycle_inspect":
					return identity, assign(result, ri)
				case "lifecycle_ensure":
					ri.Running = true
					return identity, assign(result, ri)
				case "lifecycle_prepare":
					return identity, assign(result, mesh.PreparedSetup{Key: q.Key, Token: "private-fixture-remote", Identity: remote.Identity, Listen: remote.Listen, Expires: time.Now().Add(time.Minute)})
				case "lifecycle_apply", "lifecycle_abort":
					return identity, assign(result, map[string]bool{"ok": true})
				}
			case meshops.Bootstrap:
				switch v.Action {
				case "pair":
					remote.Peers = []mesh.Peer{{Identity: own.Identity, Pair: v.Pair, Grant: v.Grant, State: "pending", Revision: 1}}
					return identity, assign(result, remote.Peers[0])
				case "activate":
					remote.Peers[0].State = "active"
					remote.Peers[0].Proven = true
					remote.Peers[0].Revision = 3
					return identity, nil
				case "peers":
					return identity, assign(result, remote.Peers)
				}
			}
			return identity, fmt.Errorf("unexpected fixture helper %T", request)
		}}
	}
	t.Cleanup(service.Close)
	return service, q, func() { oi.Running = false; ri.Running = false }, oi.Home
}
func coordAwait(t *testing.T, s *meshops.Service, key, phase string) meshops.Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		op, err := s.Get(key)
		if err == nil && op.Phase == phase {
			return op
		}
		time.Sleep(5 * time.Millisecond)
	}
	op, err := s.Get(key)
	t.Fatalf("wanted %s got %+v %v", phase, op, err)
	return op
}
func coordProveWriter(t *testing.T, home string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "mesh-lifecycle"))
	if err != nil {
		t.Fatal(err)
	}
	raws := map[string][]byte{}
	lifes := map[string]mesh.LifecycleOperation{}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(home, "mesh-lifecycle", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(entry.Name(), "coordinator-") {
			raws[entry.Name()] = raw
		} else {
			var life mesh.LifecycleOperation
			if err = strictEvidence(string(raw), &life); err != nil {
				t.Fatal(err)
			}
			lifes[life.Key] = life
		}
	}
	host := ""
	for _, life := range lifes {
		host = life.LocalHost
		break
	}
	blockers, err := proveResetCoordinations(raws, local.CurrentIdentity().UID, host, lifes)
	if err != nil || len(blockers) > 0 {
		t.Fatalf("actual producer history %+v %v", blockers, err)
	}
}
func TestStrictCoordinatorArchivalActualConnectRestoreAndVerificationWriters(t *testing.T) {
	for _, recovery := range []string{"restore", "verify-lost-receipt"} {
		t.Run(recovery, func(t *testing.T) {
			s, q, stop, home := coordWriterFixture(t)
			if _, err := s.Start(q); err != nil {
				t.Fatal(err)
			}
			review := coordAwait(t, s, q.Key, "awaiting_review")
			if _, err := s.Decide(meshops.Decision{Key: q.Key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: true}); err != nil {
				t.Fatal(err)
			}
			coordAwait(t, s, q.Key, "completed")
			s.Close()
			coordProveWriter(t, home)
			if recovery == "restore" {
				stop()
			} else {
				// Model durable coordinator-ACK loss after the independent lifecycle
				// receipt is connected. The next REAL producer must review proof again.
				op, ok, err := mesh.ReadCoordination(home, q.Key)
				if err != nil || !ok {
					t.Fatal(err)
				}
				op.Phase = "incomplete"
				op.Result = nil
				op.Error = &model.Error{Code: "unknown", Message: "fixture lost final receipt"}
				op.Revision++
				path := filepath.Join(home, "mesh-lifecycle", coordName(t, q.Key))
				if err = local.AtomicJSON(path, op); err != nil {
					t.Fatal(err)
				}
			}
			coordinator := s.New(context.Background())
			coordinator.Print = func(any) error { return nil }
			coordinator.Confirm = func(any, bool, bool) error { return nil }
			if _, err := coordinator.Connect(q.Key, *q.Connect, true, false); err != nil {
				t.Fatal(err)
			}
			coordProveWriter(t, home)
		})
	}
}
