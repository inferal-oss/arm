package mesh

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"getarm.org/internal/companions"
	"getarm.org/internal/meshssh"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func seedFixture(t *testing.T) (meshssh.CatalogSeedRequest, *companions.Catalog) {
	t.Helper()
	home := t.TempDir()
	os.Chmod(home, 0700)
	m := companions.Manifest{Schema: 1, Version: "0.1.0-dev", Protocol: 1, Integrity: "test fixture integrity only"}
	files := fstest.MapFS{}
	sum := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
	for _, p := range [][2]string{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		plain := []byte("thin fixture " + p[0] + p[1])
		var out bytes.Buffer
		gz := gzip.NewWriter(&out)
		gz.Write(plain)
		gz.Close()
		raw := out.Bytes()
		name := "arm-" + p[0] + "-" + p[1]
		m.Artifacts = append(m.Artifacts, companions.Payload{Name: name, OS: p[0], Arch: p[1], Go: "go1.27.3", Size: int64(len(plain)), SHA256: sum(plain), CompressedSize: int64(len(raw)), CompressedSHA256: sum(raw)})
		files["generated/"+name+".gz"] = &fstest.MapFile{Data: raw, Mode: 0600}
	}
	raw, digest, e := companions.Canonical(m)
	if e != nil {
		t.Fatal(e)
	}
	files["generated/manifest.json"] = &fstest.MapFile{Data: raw, Mode: 0600}
	c, e := companions.Load(files, m.Version, m.Protocol)
	if e != nil {
		t.Fatal(e)
	}
	p := m.Artifacts[3]
	q := meshssh.CatalogSeedRequest{Home: home, Host: strings.Repeat("a", 32), Catalog: meshssh.CatalogSeed{Schema: 1, Digest: digest, Manifest: m}, Transfer: meshssh.Transfer{Root: filepath.Join(home, "install"), Key: strings.Repeat("b", 64), SHA256: p.SHA256, Size: p.Size, ExpectedUID: strconv.Itoa(os.Geteuid()), OS: p.OS, Arch: p.Arch, Version: m.Version, Protocol: m.Protocol, Reason: "test exact framed catalog"}}
	return q, c
}
func TestSeedPublishesDirectoryAtomicallyWithDurableNativeIdentity(t *testing.T) {
	q, c := seedFixture(t)
	frames := c.Frames()
	defer frames.Close()
	final := filepath.Join(q.Home, "mesh-companions", "catalog-"+q.Catalog.Digest)
	called := false
	id, e := PublishCompanionCatalog(context.Background(), q, frames, func(id CatalogIdentity) error {
		called = true
		if _, e := os.Lstat(final); !os.IsNotExist(e) {
			t.Fatal("reader-visible final before durable born receipt", e)
		}
		stage := filepath.Join(q.Home, "mesh-companions", "stage-"+q.Transfer.Key)
		names, e := os.ReadDir(stage)
		if e != nil || len(names) != 5 {
			t.Fatal("incomplete stage at identity commit", e)
		}
		return nil
	})
	if e != nil || !called {
		t.Fatal(e, called)
	}
	if _, e = VerifyCompanionCatalog(q.Home, q.Catalog, &id); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Lstat(filepath.Join(q.Home, "mesh-companions", "stage-"+q.Transfer.Key)); !os.IsNotExist(e) {
		t.Fatal("stage remains after atomic publish", e)
	}
	source, e := OpenCompanionCatalog(q.Home, q.Transfer.Version, q.Transfer.Protocol)
	if e != nil || source == nil {
		t.Fatal(e)
	}
	defer source.Close()
	f, _, e := source.Catalog.Open("linux", "arm64")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if _, e = io.Copy(io.Discard, f); e != nil {
		t.Fatal(e)
	}
}
func TestSeedRuntimeFenceAndUncertainFramesNeverPublish(t *testing.T) {
	for _, kind := range []string{"runtime", "runtime-after-copy", "truncated", "trailing", "born"} {
		t.Run(kind, func(t *testing.T) {
			q, c := seedFixture(t)
			f := c.Frames()
			raw, e := io.ReadAll(f)
			f.Close()
			if e != nil {
				t.Fatal(e)
			}
			guard := func() error { return nil }
			born := func(CatalogIdentity) error { return nil }
			switch kind {
			case "runtime":
				guard = func() error { return errors.New("authority reset") }
			case "runtime-after-copy":
				calls := 0
				guard = func() error {
					calls++
					if calls > 1 {
						return errors.New("authority stopped during stream")
					}
					return nil
				}
			case "truncated":
				raw = raw[:len(raw)-1]
			case "trailing":
				raw = append(raw, 0)
			case "born":
				born = func(CatalogIdentity) error { return errors.New("receipt sync failed") }
			}
			_, e = publishCompanionCatalog(context.Background(), q, bytes.NewReader(raw), born, guard)
			if e == nil {
				t.Fatal("unsafe publication accepted")
			}
			if _, e = os.Lstat(filepath.Join(q.Home, "mesh-companions", "catalog-"+q.Catalog.Digest)); !os.IsNotExist(e) {
				t.Fatal("uncertain catalog visible", e)
			}
			if kind == "runtime" {
				if _, e = os.Lstat(filepath.Join(q.Home, "mesh-companions")); !os.IsNotExist(e) {
					t.Fatal("runtime refusal wrote namespace")
				}
			}
		})
	}
}
func TestSeedEvidenceRejectsForgedDestinationAndNativeOwnership(t *testing.T) {
	q, _ := seedFixture(t)
	valid := CatalogSeedReceipt{Request: q, Destination: filepath.Join(q.Home, "mesh-companions", "catalog-"+q.Catalog.Digest), Identity: &CatalogIdentity{Inode: 1, Owner: uint32(os.Geteuid()), Mode: 0040700}}
	if e := ValidateCatalogSeedEvidence(q, &valid); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"path", "owner", "mode", "inode"} {
		s := valid
		id := *valid.Identity
		s.Identity = &id
		switch kind {
		case "path":
			s.Destination += "-other"
		case "owner":
			s.Identity.Owner++
		case "mode":
			s.Identity.Mode = 0040777
		case "inode":
			s.Identity.Inode = 0
		}
		if ValidateCatalogSeedEvidence(q, &s) == nil {
			t.Fatal("forged evidence accepted", kind)
		}
	}
}
