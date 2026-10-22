package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/meshssh"
)

// Actual public journal producers with fixed external-I/O callbacks. Real SSH,
// artifact execution and runtime-image replacement have separate live gates.
func provisionWriterHistory(t *testing.T) (mesh.Coordination, mesh.Coordination, map[string]mesh.LifecycleOperation) {
	t.Helper()
	service, connect, _, home := coordWriterFixture(t)
	remote, e := mesh.InspectLifecycle(connect.Connect.RemoteHome, buildinfo.Version)
	if e != nil || remote.Identity == nil {
		t.Fatal(e)
	}
	payload := []byte("private trusted-catalog byte fixture")
	sum := sha256.Sum256(payload)
	sha := hex.EncodeToString(sum[:])
	dir := filepath.Join(home, "mesh-artifacts")
	if e = os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "binary-"+sha), payload, 0700); e != nil {
		t.Fatal(e)
	}
	artifact := mesh.Artifact{Schema: 1, SHA256: sha, Size: int64(len(payload)), OS: runtime.GOOS, Arch: runtime.GOARCH, Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Reason: connect.Connect.Reason}
	transfer := meshssh.Transfer{Root: filepath.Join(home, "target-install"), Key: meshops.ProvisionKey(connect.Key), SHA256: sha, Size: artifact.Size, ExpectedUID: remote.Owner.UID, OS: artifact.OS, Arch: artifact.Arch, Version: artifact.Version, Protocol: artifact.Protocol, Reason: artifact.Reason}
	q := meshops.ProvisionRequest{ConnectionKey: connect.Key, Destination: connect.Connect.Destination, Transfer: transfer, SSH: meshssh.Identity{Account: "fixture", HostKey: "SHA256:fixture"}, Home: connect.Connect.RemoteHome, Startup: mesh.DefaultStartup()}
	binary := provisionBinary(q)
	connect.Connect.Binary = binary
	connect.Connect.ProvisionKey = transfer.Key
	coordinator := service.New(context.Background())
	coordinator.Print = func(any) error { return nil }
	coordinator.Confirm = func(any, bool, bool) error { return nil }
	receipt := mesh.ProvisionReceipt{Schema: 1, Request: transfer, Actor: remote.Owner, Phase: "published_verified", Binary: binary, Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Updated: time.Now().UTC()}
	assign := func(out, value any) error { return json.Unmarshal(coordJSON(t, value), out) }
	coordinator.ProvisionTransport = &meshops.ProvisionTransport{
		Probe: func(context.Context, string, string, string, meshssh.Identity) (meshssh.Probe, meshssh.Identity, error) {
			return meshssh.Probe{OS: q.Transfer.OS, Arch: q.Transfer.Arch, UID: q.Transfer.ExpectedUID, Home: home, StateHome: q.Home, CPUs: 4, Startup: coordJSON(t, q.Startup)}, q.SSH, nil
		},
		Upload: func(_ context.Context, _ string, _ meshssh.Transfer, input io.Reader, _ meshssh.Identity, out any) (meshssh.Identity, error) {
			raw, e := io.ReadAll(input)
			if e != nil || string(raw) != string(payload) {
				return q.SSH, fmt.Errorf("artifact input differs")
			}
			return q.SSH, assign(out, receipt)
		},
	}
	coordinator.SSH = func(_ context.Context, _ string, _ string, _ any, _ meshssh.Identity, out any) (meshssh.Identity, error) {
		receipt.Phase = "started_verified"
		receipt.StartedHost = remote.Host
		receipt.StartHome = q.Home
		receipt.Startup = &q.Startup
		method := "kernel-self-executable"
		if runtime.GOOS == "darwin" {
			method = "startup-open-file-snapshot"
		}
		receipt.Runtime = &mesh.RuntimeProof{Host: remote.Host, UID: q.Transfer.ExpectedUID, SHA256: sha, Size: artifact.Size, Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Method: method, Captured: time.Now().UTC().Add(-time.Second)}
		receipt.Updated = time.Now().UTC()
		return q.SSH, assign(out, receipt)
	}
	if _, e = coordinator.Provision(q, artifact, true, false); e != nil {
		t.Fatal(e)
	}
	if _, e = service.Start(connect); e != nil {
		t.Fatal(e)
	}
	review := coordAwait(t, service, connect.Key, "awaiting_review")
	if _, e = service.Decide(meshops.Decision{Key: connect.Key, ReviewRevision: review.ReviewRevision, ReviewHash: review.ReviewHash, Approve: true}); e != nil {
		t.Fatal(e)
	}
	coordAwait(t, service, connect.Key, "completed")
	service.Close()
	prov, ok, e := mesh.ReadCoordination(home, transfer.Key)
	if e != nil || !ok {
		t.Fatal(e)
	}
	conn, ok, e := mesh.ReadCoordination(home, connect.Key)
	if e != nil || !ok {
		t.Fatal(e)
	}
	life, ok, e := mesh.ReadLifecycleOperation(home, connect.Key)
	if e != nil || !ok {
		t.Fatal(e)
	}
	coordProveWriter(t, home)
	return prov, conn, map[string]mesh.LifecycleOperation{connect.Key: life}
}
func TestStrictProvisionArchivalActualPublicWriterAndConnectLink(t *testing.T) {
	prov, conn, life := provisionWriterHistory(t)
	blockers, e := proveCoordFixture(t, []mesh.Coordination{prov, conn}, life)
	if e != nil || len(blockers) > 0 {
		t.Fatal(blockers, e)
	}
}
func TestStrictProvisionArchivalRejectsUnsettledForgedAndMissingLinks(t *testing.T) {
	original, connect, life := provisionWriterHistory(t)
	tests := []struct {
		name   string
		change func(*mesh.Coordination, *mesh.Coordination)
		omit   bool
	}{
		{"missing-provision", func(*mesh.Coordination, *mesh.Coordination) {}, true},
		{"worker", func(p, c *mesh.Coordination) {
			p.Worker = "expired-but-unresolved"
			p.WorkerExpires = time.Now().Add(-time.Hour)
		}, false},
		{"incomplete", func(p, c *mesh.Coordination) { p.Phase = "incomplete" }, false},
		{"unknown-phase", func(p, c *mesh.Coordination) { p.Phase = "unknown" }, false},
		{"unapproved", func(p, c *mesh.Coordination) { p.Approved = false }, false},
		{"runtime-null", func(p, c *mesh.Coordination) {
			p.Result = editCoordRaw(t, p.Result, func(v map[string]any) { v["receipt"].(map[string]any)["runtime"] = nil })
		}, false},
		{"runtime-alias", func(p, c *mesh.Coordination) {
			p.Result = editCoordRaw(t, p.Result, func(v map[string]any) { v["receipt"].(map[string]any)["runtime"].(map[string]any)["SHA256"] = "hidden" })
		}, false},
		{"different-digest", func(p, c *mesh.Coordination) {
			p.Result = editCoordRaw(t, p.Result, func(v map[string]any) {
				v["receipt"].(map[string]any)["runtime"].(map[string]any)["sha256"] = fmt.Sprintf("%064d", 1)
			})
		}, false},
		{"different-account", func(p, c *mesh.Coordination) {
			p.Result = editCoordRaw(t, p.Result, func(v map[string]any) { v["receipt"].(map[string]any)["runtime"].(map[string]any)["uid"] = "other" })
		}, false},
		{"null-started-host", func(p, c *mesh.Coordination) {
			p.Result = editCoordRaw(t, p.Result, func(v map[string]any) { v["receipt"].(map[string]any)["started_host"] = nil })
		}, false},
		{"publication-only", func(p, c *mesh.Coordination) {
			p.Result = editCoordRaw(t, p.Result, func(v map[string]any) { v["receipt"].(map[string]any)["phase"] = "published_verified" })
		}, false},
		{"link-to-connect", func(p, c *mesh.Coordination) {
			c.Request = editCoordRaw(t, c.Request, func(v map[string]any) { v["connect"].(map[string]any)["provision_key"] = c.Key })
		}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p, c := original, connect
			test.change(&p, &c)
			ops := []mesh.Coordination{p, c}
			if test.omit {
				ops = []mesh.Coordination{c}
			}
			blockers, e := proveCoordFixture(t, ops, life)
			if e == nil && len(blockers) == 0 {
				t.Fatal("unsafe provision archive accepted")
			}
		})
	}
}

// Re-sign all local JSON digests after changing a valid provisioning record:
// the reference must still bind its exact SSH account/pin, home and artifact.
func TestStrictProvisionLinkRejectsSemanticallyValidDifferentProvision(t *testing.T) {
	original, connect, life := provisionWriterHistory(t)
	for _, which := range []string{"home", "ssh-account", "ssh-pin"} {
		t.Run(which, func(t *testing.T) {
			p := original
			var request meshops.StartRequest
			if e := json.Unmarshal(p.Request, &request); e != nil {
				t.Fatal(e)
			}
			switch which {
			case "home":
				request.Provision.Home += "-different"
			case "ssh-account":
				request.Provision.SSH.Account = "another-account"
			case "ssh-pin":
				request.Provision.SSH.HostKey = "SHA256:another-key"
			}
			p.Request = coordJSON(t, request)
			p.Digest, _ = mesh.ReviewDigest(*request.Provision)
			var review meshops.ProvisionPlan
			if e := json.Unmarshal(p.Review, &review); e != nil {
				t.Fatal(e)
			}
			review.Request = *request.Provision
			p.Review = coordJSON(t, review)
			p.ReviewHash, _ = mesh.ReviewDigest(review)
			var result meshops.ProvisionResult
			if e := json.Unmarshal(p.Result, &result); e != nil {
				t.Fatal(e)
			}
			result.Receipt.StartHome = request.Provision.Home
			p.Result = coordJSON(t, result)
			// This modified record is internally coherent on its own.
			if b, e := proveCoordFixture(t, []mesh.Coordination{p}, nil); e != nil || len(b) != 0 {
				t.Fatal("fixture did not reach cross-link validation", b, e)
			}
			if b, e := proveCoordFixture(t, []mesh.Coordination{p, connect}, life); e == nil && len(b) == 0 {
				t.Fatal("changed linked authority accepted")
			}
		})
	}
}
