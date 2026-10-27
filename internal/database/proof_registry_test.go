package database_test

import (
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/mesh"
)

func expireProofIdentity(t *testing.T, home string, identity mesh.Identity) mesh.Identity {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "mesh", "node-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("fixture private key missing")
	}
	decoded, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := decoded.(crypto.Signer)
	if !ok {
		t.Fatal("fixture key not signer")
	}
	block, _ = pem.Decode([]byte(identity.Certificate))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	cert.NotBefore = time.Now().UTC().Truncate(time.Second).Add(-48 * time.Hour)
	cert.NotAfter = time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	identity.Certificate = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	sum := sha256.Sum256(der)
	identity.Fingerprint = hex.EncodeToString(sum[:])
	identity.Expires = cert.NotAfter
	return identity
}
func TestResetProofUsesCompleteRegistryValidationAndAllowsExpiredRetirement(t *testing.T) {
	for _, kind := range []string{"valid", "peer-fingerprint", "active-unproven", "invalid-listen", "expired"} {
		t.Run(kind, func(t *testing.T) {
			home := proofFixture(t, false)
			proof, err := prove(t, home)
			if err != nil {
				t.Fatal(err)
			}
			local, err := mesh.Init(home, proof.Host, "fixture-local", "127.0.0.1:23101", "127.0.0.1:23101")
			if err != nil {
				t.Fatal(err)
			}
			remoteHome, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			os.Chmod(remoteHome, 0700)
			remote, err := mesh.Init(remoteHome, strings.Repeat("b", 32), "fixture-peer", "127.0.0.1:23102", "127.0.0.1:23102")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = local.Pair(remote.Config().Identity, mesh.Grant{Permissions: []string{"observe-host"}}, strings.Repeat("a", 64), "private reset validation"); err != nil {
				t.Fatal(err)
			}
			remote.Close()
			config := local.Config()
			local.Close()
			switch kind {
			case "peer-fingerprint":
				config.Peers[0].Identity.Fingerprint = strings.Repeat("0", 64)
			case "active-unproven":
				config.Peers[0].State = "active"
				config.Peers[0].Proven = false
			case "invalid-listen":
				config.Listen = "0.0.0.0:0"
			case "expired":
				config.Identity = expireProofIdentity(t, home, config.Identity)
				config.Peers[0].Identity = expireProofIdentity(t, remoteHome, config.Peers[0].Identity)
			}
			raw, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(home, "mesh", "config.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, home)
			r, err := review(t, home)
			if kind == "valid" || kind == "expired" {
				if err != nil {
					t.Fatal("valid historical retirement refused", err)
				}
				if !r.Proof.Settled || r.Mesh == nil || !r.Mesh.Required {
					t.Fatal("retirement requirement missing")
				}
				r.Close()
			} else if err == nil {
				r.Close()
				t.Fatal("malformed peer/listener registry accepted")
			}
			if !reflect.DeepEqual(before, snapshotTree(t, home)) {
				t.Fatal("registry proof changed state")
			}
		})
	}
}
