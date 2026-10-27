package meshops

import (
	"context"
	"github.com/inferal-oss/arm/internal/buildinfo"
	"github.com/inferal-oss/arm/internal/companions"
	"github.com/inferal-oss/arm/internal/mesh"
	"github.com/inferal-oss/arm/internal/meshssh"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProvisionMissingCatalogRefusesBeforeStateOrRemoteMutation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	hash := strings.Repeat("a", 64)
	m := companions.Manifest{Schema: 1, Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Integrity: "test integrity only"}
	for _, p := range [][2]string{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		m.Artifacts = append(m.Artifacts, companions.Payload{Name: "arm-" + p[0] + "-" + p[1], OS: p[0], Arch: p[1], Go: "go1.27.3", Size: 1, SHA256: hash, CompressedSize: 1, CompressedSHA256: hash})
	}
	_, digest, e := companions.Canonical(m)
	if e != nil {
		t.Fatal(e)
	}
	a := mesh.Artifact{Schema: 1, SHA256: hash, Size: 1, OS: "linux", Arch: "arm64", Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Reason: "verify source before installation"}
	q := ProvisionRequest{ConnectionKey: "missing-catalog", Destination: "fixture", SSH: meshssh.Identity{Account: "fixture", HostKey: "SHA256:fixture"}, Home: "/fixture/state", Startup: mesh.DefaultStartup(), Transfer: meshssh.Transfer{Root: "/fixture/install", Key: ProvisionKey("missing-catalog"), SHA256: hash, Size: 1, ExpectedUID: strconv.Itoa(os.Geteuid()), OS: a.OS, Arch: a.Arch, Version: a.Version, Protocol: a.Protocol, Reason: a.Reason}, Seed: &meshssh.CatalogSeed{Schema: 1, Digest: digest, Manifest: m}}
	c := Coordinator{Home: home, Context: context.Background(), Machine: true, Print: func(any) error { t.Fatal("source unavailable must not offer usable install plan"); return nil }, Confirm: func(any, bool, bool) error { t.Fatal("source unavailable reached review"); return nil }}
	if _, e = c.Provision(q, a, true, false); e == nil || !strings.Contains(e.Error(), "seed_source_unavailable") {
		t.Fatal(e)
	}
	if _, e = os.Lstat(home); !os.IsNotExist(e) {
		t.Fatal("source refusal created state", e)
	}
}
