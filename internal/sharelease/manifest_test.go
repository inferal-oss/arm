package sharelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/sharefs"
	"github.com/yrashk/arm/internal/storage"
	"golang.org/x/sys/unix"
)

func nativeManifest(t *testing.T) (sharefs.Manifest, *storage.Provider, model.Artifact) {
	t.Helper()
	provider, err := storage.Open(filepath.Join(t.TempDir(), "managed"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { provider.Close() })
	name := strings.Repeat("a", 32)
	identity, err := provider.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	a := model.Artifact{Name: name, Path: provider.Path(name), Phase: "owned", Identity: &identity}
	if err = os.WriteFile(filepath.Join(a.Path, "payload"), bytes.Repeat([]byte("approved"), 400000), 0600); err != nil {
		t.Fatal(err)
	}
	engine, err := sharefs.New(sharefs.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	m, err := engine.Build(context.Background(), provider, a, sharefs.Binding{Authority: strings.Repeat("b", 32), Owner: uint32(os.Geteuid()), Storage: name, StorageRevision: 1, Share: strings.Repeat("c", 32), Generation: strings.Repeat("d", 32)})
	if err != nil {
		t.Fatal(err)
	}
	return m, provider, a
}
func rehash(t *testing.T, m *sharefs.Manifest) {
	t.Helper()
	m.SHA256 = ""
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	m.SHA256 = hex.EncodeToString(h[:])
}
func TestChunkedManifestRoundtripKeepsActualNativeAuthority(t *testing.T) {
	m, p, a := nativeManifest(t)
	parts, s, err := EncodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManifest(parts, s)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(m)
	back, _ := json.Marshal(decoded)
	if !bytes.Equal(raw, back) {
		t.Fatal("codec changed original approved manifest")
	}
	engine, _ := sharefs.New(sharefs.DefaultLimits())
	origin, err := engine.Open(context.Background(), p, a, decoded, sharefs.Scope{Source: strings.Repeat("e", 32), EnrollmentRevision: 1, Consumer: strings.Repeat("f", 32), Session: strings.Repeat("1", 32)}, func(context.Context, sharefs.Access) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	handle, err := origin.OpenRead(context.Background(), "payload")
	if err != nil {
		t.Fatal(err)
	}
	got, err := origin.Read(context.Background(), handle, 0, 8)
	if err != nil || string(got) != "approved" {
		t.Fatal(string(got), err)
	}
}
func TestLargeManifestPartsHaveExactBoundedCompleteRoster(t *testing.T) {
	// Synthetic child identities test only serialization/shape, NEVER authorize
	// native reads. The root comes from an actual managed provider; real complete
	// native authority is separately tested above.
	m, _, _ := nativeManifest(t)
	m.Entries = m.Entries[:1]
	for i := 0; i < 3000; i++ {
		v := m.Entries[0]
		v.Path = fmt.Sprintf("%04d-", i) + strings.Repeat("x", 200)
		v.Directory = false
		v.Version.Mode = unix.S_IFREG | 0600
		v.Version.Links = 1
		v.Version.Size = 0
		v.Version.Identity.Inode = uint64(i + 10000)
		v.Chunks = []string{}
		m.Entries = append(m.Entries, v)
	}
	rehash(t, &m)
	parts, s, err := EncodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if s.RawBytes < 1<<20 || len(parts) < 5 || s.StoredBytes <= s.RawBytes {
		t.Fatalf("fixture not meaningfully chunked %+v", s)
	}
	stored := 0
	for i, p := range parts {
		if p.Index != i || len(p.Data) > MaxManifestEncodedPartBytes || len(p.Data) >= 1<<20 {
			t.Fatal("part not bounded below maintenance row limit")
		}
		stored += len(p.Data)
	}
	if stored != s.StoredBytes {
		t.Fatal("base64 storage expansion not accounted")
	}
	if _, err = DecodeManifest(parts, s); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func([]ManifestPart) []ManifestPart{
		"missing":          func(p []ManifestPart) []ManifestPart { return p[:len(p)-1] },
		"extra":            func(p []ManifestPart) []ManifestPart { return append(p, p[len(p)-1]) },
		"duplicate":        func(p []ManifestPart) []ManifestPart { p[1] = p[0]; return p },
		"reordered":        func(p []ManifestPart) []ManifestPart { p[0], p[1] = p[1], p[0]; return p },
		"wrong-share":      func(p []ManifestPart) []ManifestPart { p[1].Share = strings.Repeat("9", 32); return p },
		"wrong-generation": func(p []ManifestPart) []ManifestPart { p[1].Generation = strings.Repeat("9", 32); return p },
		"newline-base64":   func(p []ManifestPart) []ManifestPart { p[0].Data = p[0].Data[:100] + "\n" + p[0].Data[100:]; return p },
		"short-middle": func(p []ManifestPart) []ManifestPart {
			p[0].Data = base64.StdEncoding.EncodeToString([]byte("short"))
			return p
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := mutate(append([]ManifestPart{}, parts...))
			if _, err := DecodeManifest(bad, s); err == nil {
				t.Fatal("accepted incomplete/mismatched manifest")
			}
		})
	}
	bad := s
	bad.StoredBytes--
	if _, err := DecodeManifest(parts, bad); err == nil {
		t.Fatal("underreported stored bytes accepted")
	}
}
func TestManifestRawProducerShapeIsClosed(t *testing.T) {
	m, _, _ := nativeManifest(t)
	parts, s, err := EncodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatal("small fixture needs one part")
	}
	raw, err := base64.StdEncoding.DecodeString(parts[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string][]byte{
		"case-alias":     bytes.Replace(raw, []byte(`"schema":1`), []byte(`"Schema":1`), 1),
		"unknown-nested": bytes.Replace(raw, []byte(`"owner":`), []byte(`"unknown":true,"owner":`), 1),
		"null-required":  bytes.Replace(raw, []byte(`"chunk_bytes":1048576`), []byte(`"chunk_bytes":null`), 1),
		"duplicate":      bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":1,"schema":1`), 1),
		"trailing":       append(append([]byte{}, raw...), []byte(` {}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			p := append([]ManifestPart{}, parts...)
			p[0].Data = base64.StdEncoding.EncodeToString(bad)
			summary := s
			summary.RawBytes = len(bad)
			summary.StoredBytes = len(p[0].Data)
			if _, err := DecodeManifest(p, summary); err == nil {
				t.Fatal("accepted noncanonical raw manifest")
			}
		})
	}
}

// Serialization-only maximum shape. Synthetic child identities never authorize
// native reads; this measures ledger encoding memory added AFTER admitted Build.
func TestMaximumLedgerEncodingAllocationFitsPreparationHeadroom(t *testing.T) {
	m, _, _ := nativeManifest(t)
	m.Entries = m.Entries[:1]
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
		v.Version.Size = 0
		v.Chunks = []string{}
		if i == 0 {
			v.Version.Size = int64(sharefs.MaxChunks) * sharefs.ChunkBytes
			v.Chunks = make([]string, sharefs.MaxChunks)
			for j := range v.Chunks {
				v.Chunks[j] = strings.Repeat("a", 64)
			}
		}
		m.Entries = append(m.Entries, v)
	}
	rehash(t, &m)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	parts, summary, err := EncodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(parts)
	runtime.KeepAlive(m)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("manifest=%d encoded=%d entries=%d chunks=%d ledger-encode-cumulative=%d", summary.RawBytes, summary.StoredBytes, summary.Entries, summary.Chunks, allocated)
	if summary.RawBytes < 6<<20 || summary.Entries != sharefs.MaxEntries || summary.Chunks != sharefs.MaxChunks {
		t.Fatal("not maximum shape")
	}
	// The separate ledger policy adds twenty manifest units for encoding
	// plus three exact base64/SQL copies. This measures cumulative allocations,
	// not a lower post-GC live heap or an operating-system RSS guarantee.
	if allocated > uint64(PreparationMemory().Encoding) || allocated+uint64(PreparationMemory().SQLCopies) > uint64(PreparationMemory().Total-PreparationMemory().Base) {
		t.Fatal("ledger encoder exceeded reserved manifest transients")
	}
	// Commit/reopen the actual maximum-shape part roster, not only the encoder.
	// These are closed serialization fixtures, not authority to read synthetic files.
	db := fixtureDB(t)
	p, _, _ := evidenceFixture()
	p.Authority = m.Binding.Authority
	p.Owner = strconv.FormatUint(uint64(m.Binding.Owner), 10)
	p.ID = m.Binding.Share
	p.Generation = m.Binding.Generation
	p.Request.Storage = m.Binding.Storage
	p.Request.StorageRevision = int(m.Binding.StorageRevision)
	p.StorageIdentity = m.Entries[0].Version.Identity
	p.Worker.UID = p.Owner
	p.PreparationWorker = p.Worker
	writePublication(t, db, p)
	update := func(next Publication, transition string) {
		t.Helper()
		oldSHA, _ := hash(p)
		newSHA, _ := hash(next)
		j := Journal{Schema: 1, Authority: p.Authority, Owner: p.Owner, At: next.Updated, Actor: "uid:" + p.Owner, Reason: next.Reason, Entity: "publication", ID: p.ID, Revision: next.Revision, Transition: transition, BeforeSHA256: oldSHA, AfterSHA256: newSHA}
		jr, e := EncodeJournal(j)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := Canonical(next)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`UPDATE share_publications SET data=? WHERE id=?`, string(raw), p.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO share_journal(data) VALUES(?)`, string(jr)); e != nil {
			t.Fatal(e)
		}
		p = next
	}
	next := p
	next.WorkerState = "running"
	next.Revision++
	next.JournalSlots--
	next.Updated = next.Updated.Add(time.Millisecond)
	update(next, "worker_started")
	for _, part := range parts {
		if _, err = db.Exec(`INSERT INTO share_manifest_parts VALUES(?,?,?,?)`, part.Share, part.Generation, part.Index, part.Data); err != nil {
			t.Fatal(err)
		}
	}
	next = p
	next.State = "prepared"
	next.WorkerState = "joined"
	next.Revision++
	next.JournalSlots--
	next.Updated = next.Updated.Add(time.Millisecond)
	at := next.Updated
	next.Prepared = &at
	next.Summary = &summary
	update(next, "prepared")
	snapshot, err := Load(t.Context(), db, p.Authority, p.Owner)
	if err != nil || snapshot.Publications[p.ID].Summary == nil || snapshot.ManifestRaw != int64(summary.RawBytes) || snapshot.ManifestStored != int64(summary.StoredBytes) {
		t.Fatal("largest admitted serialized evidence could not reopen", err)
	}
}
