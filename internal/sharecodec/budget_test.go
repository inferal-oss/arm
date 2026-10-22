package sharecodec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/storage"
	"golang.org/x/sys/unix"
)

// This fixture measures worst-shape map/codec/encoder allocation. Synthetic
// child identities do NOT authorize filesystem reads or prove native contents;
// all actual confinement tests use real managed files independently.
func TestMaximumManifestAllocationFitsAdmissionHeadroom(t *testing.T) {
	p, err := storage.Open(filepath.Join(t.TempDir(), "managed"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	born, err := p.Create(id)
	if err != nil {
		t.Fatal(err)
	}
	a := model.Artifact{Name: id, Path: p.Path(id), Identity: &born, Phase: "owned"}
	e, err := sharefs.New(sharefs.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	b := sharefs.Binding{Authority: strings.Repeat("b", 32), Owner: uint32(os.Geteuid()), Storage: id, StorageRevision: 1, Share: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)}
	m, err := e.Build(context.Background(), p, a, b)
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.Repeat("p", 150)
	dir := m.Entries[0]
	dir.Path = prefix
	dir.Version.Identity.Inode = 1000
	m.Entries = append(m.Entries, dir)
	for i := 0; i < sharefs.MaxEntries-2; i++ {
		v := dir
		v.Path = prefix + "/" + fmt.Sprintf("%04d-", i) + strings.Repeat("f", 185)
		v.Directory = false
		v.Version.Mode = unix.S_IFREG | 0600
		v.Version.Links = 1
		v.Version.Identity.Inode = uint64(i + 1001)
		v.Chunks = []string{}
		v.Version.Size = 0
		if i == 0 {
			v.Version.Size = int64(sharefs.MaxChunks) * sharefs.ChunkBytes
			v.Chunks = make([]string, sharefs.MaxChunks)
			for j := range v.Chunks {
				v.Chunks[j] = strings.Repeat("a", 64)
			}
		}
		m.Entries = append(m.Entries, v)
	}
	m.SHA256 = ""
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	m.SHA256 = hex.EncodeToString(h[:])
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 6<<20 || len(raw) > sharefs.MaxManifestBytes {
		t.Fatalf("maximum-shape fixture size%d", len(raw))
	}
	if err = m.Validate(sharefs.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var decoded sharefs.Manifest
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	o, err := e.Open(context.Background(), p, a, decoded, sharefs.Scope{Source: strings.Repeat("e", 32), EnrollmentRevision: 1, Consumer: strings.Repeat("f", 32), Session: strings.Repeat("1", 32)}, func(context.Context, sharefs.Access) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	o.Close()
	runtime.KeepAlive(encoded)
	runtime.KeepAlive(decoded)
	allocated := after.TotalAlloc - before.TotalAlloc
	plan := AdmissionMemory()
	t.Logf("manifest=%d entries=%d chunks=%d decode+validation+owned-session+encode cumulative-allocations=%d plus2source-manifests=%d preparation-headroom=%d session-headroom=%d", len(raw), len(m.Entries), sharefs.MaxChunks, allocated, allocated+uint64(len(raw))*2, plan.Preparation, plan.Session)
	if allocated+uint64(len(raw))*2 > uint64(plan.Preparation) {
		t.Fatal("measured maximum-shape cycle exceeds preparation headroom")
	}
	if plan.Session < plan.Preparation+Credits*MaxOutgoing*3+int64(sharefs.DefaultLimits().Reads)*2*sharefs.ChunkBytes+sharefs.VerifiedCacheOriginBytes {
		t.Fatal("session omits bounded transport, verified cache and active buffer headroom")
	}
}

func TestAdmissionCacheAccountingIsExplicit(t *testing.T) {
	plan := AdmissionMemory()
	if plan.Policy != "share-memory-v2" || plan.VerifiedCache != sharefs.VerifiedCacheOriginBytes || plan.Session < plan.Preparation+plan.VerifiedCache {
		t.Fatal("cache not represented in cooperative admission", plan)
	}
	if sharefs.VerifiedCacheAuthorityBytes > int64(sharefs.DefaultLimits().Sessions)*plan.VerifiedCache {
		t.Fatal("session claims cannot cover authority cache bound")
	}
}
