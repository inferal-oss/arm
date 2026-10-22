package database

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/yrashk/arm/internal/companions"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/meshops"
	"github.com/yrashk/arm/internal/meshssh"
)

func provisionSeedFixture(t *testing.T, transfer meshssh.Transfer, selected []byte) (*meshssh.CatalogSeed, *companions.Catalog) {
	t.Helper()
	manifest := companions.Manifest{Schema: 1, Version: transfer.Version, Protocol: transfer.Protocol, Integrity: "private test catalog; checksums are integrity, not publisher authentication", Artifacts: []companions.Payload{}}
	files := fstest.MapFS{}
	for _, platform := range [][2]string{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		raw := []byte("independent thin fixture for " + platform[0] + "/" + platform[1])
		if platform[0] == transfer.OS && platform[1] == transfer.Arch {
			raw = selected
		}
		var zipped bytes.Buffer
		compress := gzip.NewWriter(&zipped)
		if _, e := compress.Write(raw); e != nil {
			t.Fatal(e)
		}
		if e := compress.Close(); e != nil {
			t.Fatal(e)
		}
		plainHash, zipHash := sha256.Sum256(raw), sha256.Sum256(zipped.Bytes())
		name := "arm-" + platform[0] + "-" + platform[1]
		manifest.Artifacts = append(manifest.Artifacts, companions.Payload{Name: name, OS: platform[0], Arch: platform[1], Go: "go1.27.3", Size: int64(len(raw)), SHA256: hex.EncodeToString(plainHash[:]), CompressedSize: int64(zipped.Len()), CompressedSHA256: hex.EncodeToString(zipHash[:])})
		files["generated/"+name+".gz"] = &fstest.MapFile{Data: append([]byte{}, zipped.Bytes()...), Mode: 0600}
	}
	raw, digest, e := companions.Canonical(manifest)
	if e != nil {
		t.Fatal(e)
	}
	files["generated/manifest.json"] = &fstest.MapFile{Data: raw, Mode: 0600}
	catalog, e := companions.Load(files, transfer.Version, transfer.Protocol)
	if e != nil {
		t.Fatal(e)
	}
	return &meshssh.CatalogSeed{Schema: 1, Digest: digest, Manifest: manifest}, catalog
}

func TestStrictProvisionSeedActualWriterAndRetainedCatalog(t *testing.T) {
	provision, connect, lifecycle := provisionWriterHistory(t, true)
	var result meshops.ProvisionResult
	if e := json.Unmarshal(provision.Result, &result); e != nil {
		t.Fatal(e)
	}
	seed := result.Receipt.CatalogSeed
	if seed == nil || seed.Identity == nil || result.Receipt.Phase != "seeded_verified" {
		t.Fatal("actual public coordinator writer lacks settled catalog proof", result)
	}
	before, e := os.Stat(seed.Destination)
	if e != nil {
		t.Fatal(e)
	}
	if blockers, e := proveCoordFixture(t, []mesh.Coordination{provision, connect}, lifecycle); e != nil || len(blockers) != 0 {
		t.Fatal("complete seeded history refused", blockers, e)
	}
	after, e := os.Stat(seed.Destination)
	if e != nil || !os.SameFile(before, after) {
		t.Fatal("proof changed retained operator catalog configuration", e)
	}
	if _, e = mesh.VerifyCompanionCatalog(seed.Request.Home, seed.Request.Catalog, seed.Identity); e != nil {
		t.Fatal("retained exact four-platform payloads changed", e)
	}
}

func TestStrictProvisionSeedRejectsPartialUnknownAndChangedEvidence(t *testing.T) {
	original, connect, lifecycle := provisionWriterHistory(t, true)
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing-seed", func(r map[string]any) { delete(r, "catalog_seed") }},
		{"null-seed", func(r map[string]any) { r["catalog_seed"] = nil }},
		{"seed-alias", func(r map[string]any) { r["Catalog_Seed"] = r["catalog_seed"]; delete(r, "catalog_seed") }},
		{"seed-intent", func(r map[string]any) { r["phase"] = "seed_intent" }},
		{"only-started", func(r map[string]any) { r["phase"] = "started_verified" }},
		{"unknown-phase", func(r map[string]any) { r["phase"] = "seed_unknown" }},
		{"identity-missing", func(r map[string]any) { delete(seedMap(r), "identity") }},
		{"identity-null", func(r map[string]any) { seedMap(r)["identity"] = nil }},
		{"identity-mode-null", func(r map[string]any) { seedIdentityMap(r)["mode"] = nil }},
		{"identity-device-missing", func(r map[string]any) { delete(seedIdentityMap(r), "device") }},
		{"identity-inode-zero", func(r map[string]any) { seedIdentityMap(r)["inode"] = 0 }},
		{"identity-owner", func(r map[string]any) { seedIdentityMap(r)["owner"] = 4294967295 }},
		{"identity-public", func(r map[string]any) { seedIdentityMap(r)["mode"] = 0040755 }},
		{"identity-file", func(r map[string]any) { seedIdentityMap(r)["mode"] = 0100700 }},
		{"identity-special", func(r map[string]any) { seedIdentityMap(r)["mode"] = 0044700 }},
		{"identity-unknown-mode", func(r map[string]any) { seedIdentityMap(r)["mode"] = 1<<30 | 0040700 }},
		{"destination", func(r map[string]any) { seedMap(r)["destination"] = seedMap(r)["destination"].(string) + "-other" }},
		{"seed-home", func(r map[string]any) { seedRequestMap(r)["home"] = seedRequestMap(r)["home"].(string) + "-other" }},
		{"seed-host", func(r map[string]any) { seedRequestMap(r)["started_host"] = "11111111111111111111111111111111" }},
		{"seed-transfer", func(r map[string]any) {
			seedRequestMap(r)["transfer"].(map[string]any)["reason"] = "another original transfer"
		}},
		{"manifest-null", func(r map[string]any) { seedCatalogMap(r)["manifest"] = nil }},
		{"manifest-unknown", func(r map[string]any) { seedManifestMap(r)["extra"] = true }},
		{"manifest-schema-null", func(r map[string]any) { seedManifestMap(r)["schema"] = nil }},
		{"manifest-payload-missing", func(r map[string]any) {
			delete(seedManifestMap(r)["artifacts"].([]any)[0].(map[string]any), "compressed_sha256")
		}},
		{"manifest-payload-alias", func(r map[string]any) {
			seedManifestMap(r)["artifacts"].([]any)[0].(map[string]any)["SHA256"] = "alias"
		}},
		{"manifest-version", func(r map[string]any) { seedManifestMap(r)["expected_arm_version"] = "other" }},
		{"manifest-digest", func(r map[string]any) {
			seedCatalogMap(r)["manifest_sha256"] = "1111111111111111111111111111111111111111111111111111111111111111"
		}},
		{"unexpected-seed-field", func(r map[string]any) { seedMap(r)["complete"] = true }},
		{"runtime-missing", func(r map[string]any) { delete(r, "runtime") }},
		{"runtime-host", func(r map[string]any) { r["runtime"].(map[string]any)["host"] = "11111111111111111111111111111111" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := original
			p.Result = editCoordRaw(t, p.Result, func(v map[string]any) { test.change(v["receipt"].(map[string]any)) })
			if blockers, e := proveCoordFixture(t, []mesh.Coordination{p, connect}, lifecycle); e == nil && len(blockers) == 0 {
				t.Fatal("partial or changed catalog seed accepted as settled")
			}
		})
	}
}
func seedMap(receipt map[string]any) map[string]any { return receipt["catalog_seed"].(map[string]any) }
func seedIdentityMap(receipt map[string]any) map[string]any {
	return seedMap(receipt)["identity"].(map[string]any)
}
func seedRequestMap(receipt map[string]any) map[string]any {
	return seedMap(receipt)["request"].(map[string]any)
}
func seedCatalogMap(receipt map[string]any) map[string]any {
	return seedRequestMap(receipt)["catalog"].(map[string]any)
}
func seedManifestMap(receipt map[string]any) map[string]any {
	return seedCatalogMap(receipt)["manifest"].(map[string]any)
}

func TestStrictProvisionSeedOptionalAbsentIsDistinctFromExplicitNull(t *testing.T) {
	original, connect, lifecycle := provisionWriterHistory(t)
	for _, boundary := range []string{"request", "review", "result"} {
		t.Run(boundary, func(t *testing.T) {
			p := original
			switch boundary {
			case "request":
				p.Request = editCoordRaw(t, p.Request, func(v map[string]any) { v["provision"].(map[string]any)["seed_catalog"] = nil })
			case "review":
				p.Review = editCoordRaw(t, p.Review, func(v map[string]any) { v["request"].(map[string]any)["seed_catalog"] = nil })
				canonical, e := compactProofJSON(p.Review)
				if e != nil {
					t.Fatal(e)
				}
				sum := sha256.Sum256([]byte(canonical))
				p.ReviewHash = hex.EncodeToString(sum[:])
			case "result":
				p.Result = editCoordRaw(t, p.Result, func(v map[string]any) { v["receipt"].(map[string]any)["catalog_seed"] = nil })
			}
			if blockers, e := proveCoordFixture(t, []mesh.Coordination{p, connect}, lifecycle); e == nil && len(blockers) == 0 {
				t.Fatal("explicit seed null was silently reinterpreted as an unseeded history")
			}
		})
	}
}

func TestStrictProvisionSeedCanonicalDifferentCatalogIsNotOriginalReview(t *testing.T) {
	original, _, _ := provisionWriterHistory(t, true)
	var request meshops.StartRequest
	var result meshops.ProvisionResult
	if e := json.Unmarshal(original.Request, &request); e != nil {
		t.Fatal(e)
	}
	if e := json.Unmarshal(original.Result, &result); e != nil {
		t.Fatal(e)
	}
	seed := &result.Receipt.CatalogSeed.Request.Catalog
	// Change a platform other than the selected executable, then correctly
	// recompute the canonical digest. This new catalog is internally valid,
	// so only binding it to the original approved catalog can reject it.
	for i := range seed.Manifest.Artifacts {
		a := &seed.Manifest.Artifacts[i]
		if a.OS != request.Provision.Transfer.OS || a.Arch != request.Provision.Transfer.Arch {
			a.SHA256 = "1111111111111111111111111111111111111111111111111111111111111111"
			break
		}
	}
	_, digest, e := companions.Canonical(seed.Manifest)
	if e != nil {
		t.Fatal(e)
	}
	seed.Digest = digest
	result.Receipt.CatalogSeed.Destination = result.Receipt.StartHome + "/mesh-companions/catalog-" + digest
	if e = seed.Validate(request.Provision.Transfer); e != nil {
		t.Fatal("alternate catalog fixture is not semantically valid", e)
	}
	r := &proofCoordRecord{request: request}
	if e = validateProvisionResult(r, result); e == nil {
		t.Fatal("new internally valid catalog replaced the approved catalog")
	}
	request.Provision.Seed = nil
	if e = validateProvisionResult(r, result); e == nil {
		t.Fatal("unrequested catalog seed treated as approved")
	}
}

func TestStrictProvisionSeedSettledChildAndConnectProvenance(t *testing.T) {
	original, connect, lifecycle := provisionWriterHistory(t, true, true)
	if original.Phase != "incomplete" || original.ResolvedBy == "" {
		t.Fatal("fixture has no original uncertain seed + resolved child", original)
	}
	var q meshops.StartRequest
	if e := json.Unmarshal(original.Request, &q); e != nil {
		t.Fatal(e)
	}
	home := filepath.Dir(q.Provision.Transfer.Root)
	child, found, e := mesh.ReadCoordination(home, original.ResolvedBy)
	if e != nil || !found {
		t.Fatal(found, e)
	}
	if blockers, e := proveCoordFixture(t, []mesh.Coordination{original, child, connect}, lifecycle); e != nil || len(blockers) != 0 {
		t.Fatal("exact settled seed recovery family refused", blockers, e)
	}
	if blockers, e := proveCoordFixture(t, []mesh.Coordination{original, connect}, lifecycle); e == nil && len(blockers) == 0 {
		t.Fatal("missing seed recovery child accepted")
	}
	for _, change := range []string{"phase", "parent", "seed", "request"} {
		t.Run(change, func(t *testing.T) {
			c := child
			switch change {
			case "phase":
				c.Phase = "incomplete"
			case "parent":
				c.ParentKey = "wrong-original"
			case "seed":
				c.Result = editCoordRaw(t, c.Result, func(v map[string]any) { v["receipt"].(map[string]any)["phase"] = "seed_intent" })
			case "request":
				c.Request = editCoordRaw(t, c.Request, func(v map[string]any) {
					v["provision"].(map[string]any)["seed_catalog"].(map[string]any)["manifest_sha256"] = "1111111111111111111111111111111111111111111111111111111111111111"
				})
			}
			if blockers, e := proveCoordFixture(t, []mesh.Coordination{original, c, connect}, lifecycle); e == nil && len(blockers) == 0 {
				t.Fatal("unsafe recovery/connect seed provenance accepted")
			}
		})
	}
}
