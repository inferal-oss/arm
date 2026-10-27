package cli

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"testing/fstest"

	"getarm.org/internal/companions"
	"getarm.org/internal/mesh"
)

func TestKubernetesCatalogProofVerifiesAllPayloadStreams(t *testing.T) {
	hash := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	files := fstest.MapFS{}
	manifest := companions.Manifest{Schema: 1, Version: Version, Protocol: mesh.LifecycleProtocol, Integrity: "test carrier streams"}
	for _, platform := range [][2]string{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		plain := []byte("carrier " + platform[0] + "/" + platform[1])
		var encoded bytes.Buffer
		compressor := gzip.NewWriter(&encoded)
		if _, err := compressor.Write(plain); err != nil {
			t.Fatal(err)
		}
		if err := compressor.Close(); err != nil {
			t.Fatal(err)
		}
		name := "arm-" + platform[0] + "-" + platform[1]
		compressed := append([]byte(nil), encoded.Bytes()...)
		manifest.Artifacts = append(manifest.Artifacts, companions.Payload{Name: name, OS: platform[0], Arch: platform[1], Go: "go1.27.3", Size: int64(len(plain)), SHA256: hash(plain), CompressedSize: int64(len(compressed)), CompressedSHA256: hash(compressed)})
		files["generated/"+name+".gz"] = &fstest.MapFile{Data: compressed, Mode: 0600}
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files["generated/manifest.json"] = &fstest.MapFile{Data: append(raw, '\n'), Mode: 0600}
	catalog, err := companions.Load(files, Version, mesh.LifecycleProtocol)
	if err != nil {
		t.Fatal(err)
	}
	_, expected, err := companions.Canonical(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if digest, err := verifiedEmbeddedCatalogDigest(catalog); err != nil || digest != expected {
		t.Fatalf("verified carrier digest = %q, %v", digest, err)
	}
	corrupted := *files["generated/arm-linux-arm64.gz"]
	corrupted.Data = append([]byte(nil), corrupted.Data...)
	corrupted.Data[len(corrupted.Data)-1] ^= 1
	files["generated/arm-linux-arm64.gz"] = &corrupted
	if _, err := verifiedEmbeddedCatalogDigest(catalog); err == nil {
		t.Fatal("corrupted carrier payload was accepted")
	}
}
