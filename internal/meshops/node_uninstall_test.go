package meshops

import (
	"encoding/json"
	"github.com/yrashk/arm/internal/buildinfo"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshssh"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestKnownUninstallSourceKeyNeverRepreparesOrApplies(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(home, 0700); e != nil {
		t.Fatal(e)
	}
	q := UninstallDraft{Host: strings.Repeat("1", 32), ProvisionKey: "original-provision", Reason: "remove exact test installation"}
	digest, e := mesh.ReviewDigest(q)
	if e != nil {
		t.Fatal(e)
	}
	guard, e := mesh.ClaimCoordination(home, "original-uninstall", "uninstall", digest, "host:"+q.Host)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(q)
	if e != nil {
		t.Fatal(e)
	}
	if e = guard.Change(func(op *mesh.Coordination) error {
		op.Request = raw
		op.Phase = "incomplete"
		op.Approved = true
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	var result any
	c := &Coordinator{Context: t.Context(), Home: home, Print: func(v any) error { result = v; return nil }, Confirm: func(any, bool, bool) error { t.Fatal("known key requested fresh approval"); return nil }, Inspect: func() (mesh.LifecycleInspection, error) {
		t.Fatal("known key tried preparing against live host")
		return mesh.LifecycleInspection{}, nil
	}}
	if code, e := c.Uninstall("original-uninstall", q, true, false); code != 0 || e != nil {
		t.Fatal(code, e)
	}
	if projected := result.(Operation); !projected.LookupOnly || projected.RecoveryHash != "" {
		t.Fatal("uninstall offered generic recovery", projected)
	}
	if _, e = mesh.HandoffCoordination(home, "original-uninstall", 2, "", "new attempt"); e == nil {
		t.Fatal("uninstall handoff allowed")
	}
	if _, e = mesh.ClaimCoordinationAttempt(home, "original-uninstall", "host:"+q.Host); e == nil {
		t.Fatal("uninstall replay attempt allowed")
	}
	if result == nil {
		t.Fatal("original unknown receipt not returned")
	}
	q.Reason = "different purpose"
	if _, e = c.Uninstall("original-uninstall", q, true, false); e == nil {
		t.Fatal("immutable intent replaced")
	}
}

func TestUninstallReadsActualProvisionWriterEnvelope(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(home, 0700); e != nil {
		t.Fatal(e)
	}
	connection := "fixture-connect"
	key := ProvisionKey(connection)
	request := ProvisionRequest{ConnectionKey: connection, Destination: "fixture.example", Home: filepath.Join(home, "target"), SSH: meshssh.Identity{Account: "fixture", HostKey: "SHA256:fixture"}, Startup: mesh.DefaultStartup(), Transfer: meshssh.Transfer{Root: filepath.Join(home, "install"), Key: key, SHA256: strings.Repeat("a", 64), Size: 1, ExpectedUID: strconv.Itoa(os.Geteuid()), OS: runtime.GOOS, Arch: runtime.GOARCH, Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Reason: "actual writer-envelope regression"}}
	digest, e := mesh.ReviewDigest(request)
	if e != nil {
		t.Fatal(e)
	}
	c := &Coordinator{Context: t.Context(), Home: home}
	if e = c.begin(key, "provision", digest, "ssh:"+request.Destination); e != nil {
		t.Fatal(e)
	}
	defer c.heartbeatStop()
	if e = c.recordProvisionRequest(key, request); e != nil {
		t.Fatal(e)
	}
	if e = c.guard.Change(func(op *mesh.Coordination) error {
		op.Approved = true
		op.Phase = "completed"
		op.Worker = ""
		op.WorkerExpires = time.Time{}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	original, ok, e := mesh.ReadCoordination(home, key)
	if e != nil || !ok {
		t.Fatal(e)
	}
	decoded, e := uninstallProvisionRequest(original, key)
	if e != nil || decoded != request {
		t.Fatal("actual provision writer cannot be consumed", decoded, e)
	}
	for _, kind := range []string{"wrapper-key", "mixed-wrapper", "digest", "unapproved", "incomplete"} {
		t.Run(kind, func(t *testing.T) {
			candidate := original
			var saved StartRequest
			if e := mesh.Decode(candidate.Request, &saved); e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "wrapper-key":
				saved.Key = "another-key"
			case "mixed-wrapper":
				saved.Connect = &ConnectRequest{}
			case "digest":
				candidate.Digest = strings.Repeat("b", 64)
			case "unapproved":
				candidate.Approved = false
			case "incomplete":
				candidate.Phase = "incomplete"
			}
			candidate.Request, _ = json.Marshal(saved)
			if _, e := uninstallProvisionRequest(candidate, key); e == nil {
				t.Fatal("changed provisioning evidence accepted", kind)
			}
		})
	}
}
