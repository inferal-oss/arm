package meshops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"getarm.org/internal/buildinfo"
	"getarm.org/internal/mesh"
	"getarm.org/internal/meshssh"
)

func TestProvisionNoConsentAndDryRunNeverImportOrCreateState(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(strconv.FormatBool(dry), func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "absent")
			artifact := mesh.Artifact{Schema: 1, SHA256: strings.Repeat("a", 64), Size: 1, OS: "linux", Arch: "arm64", Version: buildinfo.Version, Protocol: mesh.LifecycleProtocol, Reason: "review only"}
			q := ProvisionRequest{ConnectionKey: "machine-review", Destination: "fixture", SSH: meshssh.Identity{Account: "fixture", HostKey: "SHA256:fixture"}, Home: "/private-fixture/state", Startup: mesh.DefaultStartup(), Transfer: meshssh.Transfer{Root: "/private-fixture/install", Key: ProvisionKey("machine-review"), SHA256: artifact.SHA256, Size: artifact.Size, OS: artifact.OS, Arch: artifact.Arch, ExpectedUID: strconv.Itoa(os.Geteuid()), Version: artifact.Version, Protocol: artifact.Protocol, Reason: artifact.Reason}}
			prints := 0
			c := Coordinator{Context: context.Background(), Home: home, Machine: true, Print: func(any) error { prints++; return nil }, Confirm: func(any, bool, bool) error { t.Fatal("no-consent path prompted"); return errors.New("unexpected") }, ProvisionTransport: &ProvisionTransport{Import: func(mesh.Artifact) (mesh.Artifact, error) { t.Fatal("unapproved import"); return artifact, nil }}}
			code, err := c.Provision(q, artifact, false, dry)
			if dry && (code != 0 || err != nil) || !dry && (code == 0 || err == nil) || prints != 1 {
				t.Fatal(code, err, prints)
			}
			if _, err = os.Lstat(home); !os.IsNotExist(err) {
				t.Fatal("read-only review created source home", err)
			}
		})
	}
}
