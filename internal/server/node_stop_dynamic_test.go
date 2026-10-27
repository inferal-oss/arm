package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inferal-oss/arm/internal/client"
	"github.com/inferal-oss/arm/internal/local"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/model"
	"github.com/inferal-oss/arm/internal/nodestop"
)

func stopPrivateBackend(t *testing.T) (string, local.Endpoint, model.Status) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 4}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error("private backend did not join cleanly", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("private backend owner remains unresolved; no saved-PID fallback")
		}
	})
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		endpoint, e := local.ReadEndpoint(home)
		if e == nil {
			var status model.Status
			if e = client.Call(t.Context(), home, "status", struct{}{}, &status); e == nil {
				return home, endpoint, status
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("private backend did not publish a usable local endpoint")
	return "", local.Endpoint{}, model.Status{}
}

func stopEnablePrivateMesh(t *testing.T, home, host, key string) mesh.Identity {
	t.Helper()
	var prepared mesh.PreparedSetup
	if err := client.Call(t.Context(), home, "mesh_setup_prepare", mesh.PrepareRequest{Key: key, Name: key, IP: "127.0.0.1", ExpectedHost: host}, &prepared); err != nil {
		t.Fatal(err)
	}
	var applied any
	if err := client.Call(t.Context(), home, "mesh_setup_apply", mesh.ApplySetup{Key: key, Token: prepared.Token, Fingerprint: prepared.Identity.Fingerprint, Listen: prepared.Listen}, &applied); err != nil {
		t.Fatal(err)
	}
	return prepared.Identity
}

func stopPairPrivate(t *testing.T, leftHome, rightHome string, left, right mesh.Identity, pair string) mesh.Peer {
	t.Helper()
	return stopPairPrivateGrant(t, leftHome, rightHome, left, right, pair, mesh.Grant{Permissions: []string{"node-stop", "observe-own"}})
}
func stopPairPrivateGrant(t *testing.T, leftHome, rightHome string, left, right mesh.Identity, pair string, grant mesh.Grant) mesh.Peer {
	t.Helper()
	for _, row := range []struct {
		home  string
		other mesh.Identity
	}{{leftHome, right}, {rightHome, left}} {
		if err := client.Call(t.Context(), row.home, "mesh_pair", struct {
			Identity       mesh.Identity
			Grant          mesh.Grant
			Pair, Reason   string
			ReplaceRevoked bool
		}{row.other, grant, pair, "fresh explicit private test pairing", true}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ home, other string }{{leftHome, right.Host}, {rightHome, left.Host}} {
		if err := client.Call(t.Context(), row.home, "mesh_prove", struct{ Host, Pair string }{row.other, pair}, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ home, other string }{{leftHome, right.Host}, {rightHome, left.Host}} {
		if err := client.Call(t.Context(), row.home, "mesh_activate", struct{ Host, Pair string }{row.other, pair}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var peers []mesh.Peer
	if err := client.Call(t.Context(), rightHome, "mesh_peers", struct{}{}, &peers); err != nil {
		t.Fatal(err)
	}
	for _, peer := range peers {
		if peer.Identity.Host == left.Host && peer.State == "active" {
			return peer
		}
	}
	t.Fatal("private pairing did not establish the exact destination enrollment")
	return mesh.Peer{}
}

func stopPlanPrivate(t *testing.T, sourceHome string, target mesh.Identity, peer mesh.Peer, key string) (nodestop.Plan, error) {
	t.Helper()
	raw, _ := json.Marshal(stopPlanRequest{Key: key, Reason: "private read-only generation review", Policy: model.ShutdownRequest{}})
	request := mesh.Request{Operation: "node_stop_plan", Expected: &mesh.ExpectedEnrollment{Pair: peer.Pair, Revision: peer.Revision}, Params: raw}
	var result struct {
		Plan   nodestop.Plan `json:"plan"`
		SHA256 string        `json:"review_sha256"`
	}
	err := client.Call(t.Context(), sourceHome, "mesh_dispatch", struct {
		Host    string
		Request mesh.Request
	}{target.Host, request}, &result)
	if err == nil {
		hash, e := result.Plan.Digest()
		if e != nil || hash != result.SHA256 {
			return result.Plan, errors.New("actual plan/digest mismatch")
		}
	}
	return result.Plan, err
}

func TestDynamicMeshEnableInstallsSameGenerationBoundaryAndRefusesChangedIdentity(t *testing.T) {
	leftHome, _, leftStatus := stopPrivateBackend(t)
	rightHome, rightEndpoint, rightStatus := stopPrivateBackend(t)
	if _, err := os.Stat(filepath.Join(rightHome, "mesh")); !os.IsNotExist(err) {
		t.Fatal("private no-mesh startup already initialized identity", err)
	}
	left := stopEnablePrivateMesh(t, leftHome, leftStatus.Host, "left-enable")
	right := stopEnablePrivateMesh(t, rightHome, rightStatus.Host, "right-enable")
	peer := stopPairPrivate(t, leftHome, rightHome, left, right, strings.Repeat("a", 64))
	plan, err := stopPlanPrivate(t, leftHome, right, peer, "after-dynamic-enable")
	if err != nil {
		t.Fatal("dynamic node-stop generation binding missing", err)
	}
	if plan.Target.Generation == "" || plan.Target.Generation != rightEndpoint.Generation || plan.Target.Certificate != right.Fingerprint {
		t.Fatal("dynamic enable borrowed/changed backend lifetime", plan.Target)
	}
	// Close/reopen the SAME owned listener identity through actual local RPC.
	if err = client.Call(t.Context(), rightHome, "mesh_setup_revoke", struct {
		Key, Host, Pair, Fingerprint, Reason string
		Revision                             int
	}{"close-edge", left.Host, peer.Pair, left.Fingerprint, "explicit private same-identity reopen", peer.Revision}, nil); err != nil {
		t.Fatal(err)
	}
	if err = client.Call(t.Context(), leftHome, "mesh_revoke", struct{ Host, Reason string }{right.Host, "private old edge revoked"}, nil); err != nil {
		t.Fatal(err)
	}
	reopened := stopEnablePrivateMesh(t, rightHome, rightStatus.Host, "right-reopen")
	if reopened.Fingerprint != right.Fingerprint {
		t.Fatal("same-identity reopen changed pin")
	}
	peer = stopPairPrivate(t, leftHome, rightHome, left, reopened, strings.Repeat("b", 64))
	plan, err = stopPlanPrivate(t, leftHome, reopened, peer, "after-same-reopen")
	if err != nil || plan.Target.Generation != rightEndpoint.Generation {
		t.Fatal("same boundary installation was not idempotent", err)
	}
	// A real certificate rotation must leave ordinary local/mesh functionality
	// usable, but cannot reuse the cached old node-stop boundary for this lifetime.
	var changed mesh.Identity
	if err = client.Call(t.Context(), rightHome, "mesh_renew", struct{ Reason string }{"private changed-identity refusal"}, &changed); err != nil {
		t.Fatal(err)
	}
	if changed.Fingerprint == right.Fingerprint {
		t.Fatal("identity-change control did not rotate certificate")
	}
	if err = client.Call(t.Context(), leftHome, "mesh_revoke", struct{ Host, Reason string }{right.Host, "private old pin revoked"}, nil); err != nil {
		t.Fatal(err)
	}
	peer = stopPairPrivate(t, leftHome, rightHome, left, changed, strings.Repeat("c", 64))
	_, err = stopPlanPrivate(t, leftHome, changed, peer, "changed-identity")
	var api *model.Error
	if !errors.As(err, &api) || api.Code != "node_stop_unavailable" {
		t.Fatal("new pin reused old boundary or failed before authenticated Stop handling", err)
	}
	var status model.Status
	if err = client.Call(t.Context(), rightHome, "status", struct{}{}, &status); err != nil || status.Host != rightStatus.Host {
		t.Fatal("unavailable node-stop disabled ordinary local service", err)
	}
}
