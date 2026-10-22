package historypack

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	return root
}
func fixtureRecords() []Record {
	return []Record{{Key{"cargo", "", "key  "}, []byte("first")}, {Key{"cargo", "", "Key"}, []byte("second")}, {Key{"ssh-source", "source/target", "same-key"}, []byte{0, 1, 2}}, {Key{"routing", "source/target", "same-key"}, nil}}
}
func prepared(t *testing.T) (string, Prepared) {
	t.Helper()
	root := fixtureRoot(t)
	p, e := Prepare(t.Context(), root, strings.Repeat("a", 32), strings.Repeat("b", 32), fixtureRecords())
	if e != nil {
		t.Fatal(e)
	}
	return root, p
}
func published(t *testing.T) (string, Receipt) {
	t.Helper()
	root, p := prepared(t)
	r, e := Publish(t.Context(), root, p)
	if e != nil {
		t.Fatal(e)
	}
	return root, r
}
func TestRoundTripExactKeysAndReceiptRetry(t *testing.T) {
	root, p := prepared(t)
	if _, e := Open(t.Context(), root, Receipt{p}); e == nil {
		t.Fatal("stage mistaken for published pack")
	}
	r, e := Publish(t.Context(), root, p)
	if e != nil {
		t.Fatal(e)
	}
	retry, e := Publish(t.Context(), root, p)
	if e != nil || !reflect.DeepEqual(retry, r) {
		t.Fatal("exact retry changed receipt", e)
	}
	pack, e := Open(t.Context(), root, r)
	if e != nil {
		t.Fatal(e)
	}
	defer pack.Close()
	for _, v := range fixtureRecords() {
		raw, e := pack.Read(t.Context(), v.Key)
		if e != nil || !bytes.Equal(raw, v.Payload) {
			t.Fatal(v.Key, e)
		}
		if len(raw) > 0 {
			raw[0] ^= 1
		}
	}
	for _, k := range []Key{{"cargo", "", "key"}, {"cargo", "", "KEY"}, {"ssh-source", "another", "same-key"}} {
		if _, e = pack.Read(t.Context(), k); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("exact key comparison lost", k, e)
		}
	}
	m, e := pack.Manifest()
	if e != nil {
		t.Fatal(e)
	}
	m.Entries[0].SHA256 = "caller mutation"
	if e = pack.Verify(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = pack.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = pack.Read(t.Context(), fixtureRecords()[0].Key); e == nil {
		t.Fatal("closed pack readable")
	}
}
func TestNoClobberAndByteEquivalentIdentityDistinction(t *testing.T) {
	root, p := prepared(t)
	final := filepath.Join(root, finalName(p.ID))
	if e := os.Mkdir(final, 0700); e != nil {
		t.Fatal(e)
	}
	marker := filepath.Join(final, "unfamiliar")
	if e := os.WriteFile(marker, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := Publish(t.Context(), root, p); e == nil {
		t.Fatal("overwrote foreign final")
	}
	if raw, e := os.ReadFile(marker); e != nil || string(raw) != "keep" {
		t.Fatal("unfamiliar evidence changed", e)
	}
	if _, e := os.Stat(filepath.Join(root, stageName(p.ID))); e != nil {
		t.Fatal("stage discarded", e)
	}
	root2, r := published(t)
	source := filepath.Join(root2, finalName(r.ID))
	copyName := source + "-retained"
	if e := os.Rename(source, copyName); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"records.bin", "manifest.json"} {
		raw, e := os.ReadFile(filepath.Join(copyName, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(source, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := Publish(t.Context(), root2, r.Prepared); e == nil {
		t.Fatal("byte equivalence substituted for born identity")
	}
}
func TestKernelRenameNoReplace(t *testing.T) {
	root := fixtureRoot(t)
	for _, n := range []string{"stage", "final"} {
		if e := os.Mkdir(filepath.Join(root, n), 0700); e != nil {
			t.Fatal(e)
		}
	}
	f, _, e := openRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = renameNoReplace(int(f.Fd()), "stage", "final"); e == nil {
		t.Fatal("kernel replaced existing directory")
	}
	for _, n := range []string{"stage", "final"} {
		if _, e = os.Stat(filepath.Join(root, n)); e != nil {
			t.Fatal(e)
		}
	}
}
func TestInputBoundsBeforeWriting(t *testing.T) {
	for _, kind := range []string{"empty", "count", "duplicate", "payload", "domain", "principal", "key", "utf8", "control", "total", "headers"} {
		t.Run(kind, func(t *testing.T) {
			root := fixtureRoot(t)
			records := []Record{{Key{"cargo", "", "key"}, nil}}
			switch kind {
			case "empty":
				records = nil
			case "count":
				records = make([]Record, MaxRecords+1)
			case "duplicate":
				records = append(records, records[0])
			case "payload":
				records[0].Payload = make([]byte, MaxPayloadBytes+1)
			case "domain":
				records[0].Key.Domain = strings.Repeat("d", MaxDomainBytes+1)
			case "principal":
				records[0].Key.Principal = strings.Repeat("p", MaxPrincipalBytes+1)
			case "key":
				records[0].Key.Value = strings.Repeat("k", MaxKeyBytes+1)
			case "utf8":
				records[0].Key.Value = string([]byte{0xff})
			case "control":
				records[0].Key.Value = "unsafe\nkey"
			case "total":
				for i := 0; i < 64; i++ {
					records = append(records, Record{Key{"cargo", "", fmt.Sprint(i)}, make([]byte, MaxPayloadBytes)})
				}
			case "headers":
				records = nil
				for i := 0; i < MaxRecords; i++ {
					records = append(records, Record{Key{"cargo", "", fmt.Sprint(i) + strings.Repeat("x", 1000)}, nil})
				}
			}
			if _, e := Prepare(t.Context(), root, strings.Repeat("a", 32), strings.Repeat("b", 32), records); e == nil {
				t.Fatal("accepted", kind)
			}
			names, e := os.ReadDir(root)
			if e != nil || len(names) != 0 {
				t.Fatal("invalid input wrote namespace", names, e)
			}
		})
	}
}
func TestCanonicalAndFrameCorruption(t *testing.T) {
	for _, kind := range []string{"trailing", "truncated", "prefix-overflow", "payload", "manifest-null", "manifest-alias", "manifest-extra", "manifest-duplicate", "offset-overflow"} {
		t.Run(kind, func(t *testing.T) {
			root, p := prepared(t)
			dir := filepath.Join(root, stageName(p.ID))
			rec := filepath.Join(dir, "records.bin")
			manifest := filepath.Join(dir, "manifest.json")
			raw, e := os.ReadFile(manifest)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "trailing":
				f, e := os.OpenFile(rec, os.O_APPEND|os.O_WRONLY, 0)
				if e != nil {
					t.Fatal(e)
				}
				f.Write([]byte{1})
				f.Close()
			case "truncated":
				if e = os.Truncate(rec, int64(p.RecordsBytes)-1); e != nil {
					t.Fatal(e)
				}
			case "prefix-overflow":
				f, e := os.OpenFile(rec, os.O_WRONLY, 0)
				if e != nil {
					t.Fatal(e)
				}
				var v [8]byte
				binary.BigEndian.PutUint64(v[:], ^uint64(0))
				f.WriteAt(v[:], int64(len(magic)+4))
				f.Close()
			case "payload":
				f, e := os.OpenFile(rec, os.O_WRONLY, 0)
				if e != nil {
					t.Fatal(e)
				}
				f.WriteAt([]byte{9}, int64(p.RecordsBytes)-1)
				f.Close()
			case "manifest-null":
				raw = bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":null`), 1)
			case "manifest-alias":
				raw = bytes.Replace(raw, []byte(`"authority"`), []byte(`"Authority"`), 1)
			case "manifest-extra":
				raw = append(raw[:len(raw)-1], []byte(`,"future":true}`)...)
			case "manifest-duplicate":
				raw = bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1,"schema_version":1`), 1)
			case "offset-overflow":
				var m Manifest
				if e = json.Unmarshal(raw, &m); e != nil {
					t.Fatal(e)
				}
				m.Entries[0].Offset = ^uint64(0)
				raw, _ = json.Marshal(m)
			}
			if strings.HasPrefix(kind, "manifest-") || kind == "offset-overflow" {
				if e = os.WriteFile(manifest, raw, 0600); e != nil {
					t.Fatal(e)
				}
				p.ManifestBytes = uint64(len(raw))
				p.ManifestSHA256 = digest(raw)
			}
			if _, e = Publish(t.Context(), root, p); e == nil {
				t.Fatal("corruption accepted", kind)
			}
			if _, e = os.Stat(dir); e != nil {
				t.Fatal("corrupt evidence removed", e)
			}
		})
	}
}
func TestUnsafePathsModesLinksAndMembers(t *testing.T) {
	for _, kind := range []string{"root-mode", "dir-mode", "file-mode", "special-mode", "symlink-file", "hardlink-file", "extra-member", "lock-replaced", "root-replaced", "ancestor-symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, p := prepared(t)
			dir := filepath.Join(root, stageName(p.ID))
			file := filepath.Join(dir, "records.bin")
			requestRoot := root
			switch kind {
			case "root-mode":
				if e := os.Chmod(root, 0777); e != nil {
					t.Fatal(e)
				}
			case "dir-mode":
				if e := os.Chmod(dir, 0777); e != nil {
					t.Fatal(e)
				}
			case "file-mode":
				if e := os.Chmod(file, 0644); e != nil {
					t.Fatal(e)
				}
			case "special-mode":
				if e := unix.Chmod(file, 04600); e != nil {
					t.Fatal(e)
				}
				var st unix.Stat_t
				if e := unix.Stat(file, &st); e != nil {
					t.Fatal(e)
				}
				if st.Mode&unix.S_ISUID == 0 {
					t.Skip("filesystem/sandbox did not retain set-ID fixture bit; synthetic mode rejection is tested separately")
				}
			case "symlink-file":
				if e := os.Rename(file, file+"-old"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(file+"-old", file); e != nil {
					t.Fatal(e)
				}
			case "hardlink-file":
				if e := os.Link(file, filepath.Join(root, "extra-link")); e != nil {
					t.Fatal(e)
				}
			case "extra-member":
				if e := os.WriteFile(filepath.Join(dir, "extra"), nil, 0600); e != nil {
					t.Fatal(e)
				}
			case "lock-replaced":
				if e := os.Rename(filepath.Join(root, lockName), filepath.Join(root, "old-lock")); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(filepath.Join(root, lockName), nil, 0600); e != nil {
					t.Fatal(e)
				}
			case "root-replaced":
				if e := os.Rename(root, root+"-old"); e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { os.RemoveAll(root + "-old") })
				if e := os.Mkdir(root, 0700); e != nil {
					t.Fatal(e)
				}
			case "ancestor-symlink":
				alias := filepath.Join(fixtureRoot(t), "alias")
				if e := os.Symlink(filepath.Dir(root), alias); e != nil {
					t.Fatal(e)
				}
				requestRoot = filepath.Join(alias, filepath.Base(root))
			}
			if _, e := Publish(t.Context(), requestRoot, p); e == nil {
				t.Fatal("unsafe pack accepted", kind)
			}
		})
	}
}

// Err side effects model cancellation at real filesystem boundaries without
// product fault hooks. No unrelated path/process is touched.
type boundaryContext struct {
	context.Context
	check func() error
}

func (c boundaryContext) Err() error { return c.check() }
func TestCancellationRetainsBornStageAndLostPublicationReply(t *testing.T) {
	root := fixtureRoot(t)
	id := strings.Repeat("a", 32)
	ctx := boundaryContext{context.Background(), func() error {
		if _, e := os.Stat(filepath.Join(root, stageName(id), "records.bin")); e == nil {
			return context.Canceled
		}
		return nil
	}}
	p, e := Prepare(ctx, root, id, strings.Repeat("b", 32), fixtureRecords())
	if !errors.Is(e, context.Canceled) || p.Directory.Inode == 0 {
		t.Fatal("cancellation lost born stage", p, e)
	}
	if _, e = os.Stat(filepath.Join(root, stageName(id))); e != nil {
		t.Fatal("cancelled stage deleted", e)
	}
	root, p = prepared(t)
	ctx = boundaryContext{context.Background(), func() error {
		if _, e := os.Stat(filepath.Join(root, finalName(p.ID))); e == nil {
			return context.Canceled
		}
		return nil
	}}
	r, e := Publish(ctx, root, p)
	var unknown *PublicationError
	if !errors.As(e, &unknown) || !errors.Is(e, context.Canceled) || r.Prepared != p {
		t.Fatal("postrename cancellation is not explicit unknown", e)
	}
	retry, e := Publish(t.Context(), root, p)
	if e != nil || retry != r {
		t.Fatal("exact lost-reply retry changed evidence", e)
	}
}
func TestOpenReadRejectsCanonicalChangeAndUnboundedFrame(t *testing.T) {
	root, r := published(t)
	pack, e := Open(t.Context(), root, r)
	if e != nil {
		t.Fatal(e)
	}
	defer pack.Close()
	if e = os.Chmod(filepath.Join(root, finalName(r.ID)), 0777); e != nil {
		t.Fatal(e)
	}
	if _, e = pack.Read(t.Context(), fixtureRecords()[0].Key); e == nil {
		t.Fatal("warm read ignored unsafe directory")
	}
	m := Manifest{Schema: 1, ID: r.ID, Authority: r.Authority, RecordsBytes: MaxPackBytes, RecordsSHA256: strings.Repeat("a", 64), Entries: []Entry{{Key{"cargo", "", "key"}, ^uint64(0), ^uint64(0), strings.Repeat("b", 64)}}}
	if e = manifestValid(m); e == nil {
		t.Fatal("overflow accepted")
	}
}

func TestProtectedModesAndExactNativeBoundaries(t *testing.T) {
	file := Identity{1, 1, uint32(os.Geteuid()), unix.S_IFREG | 0600}
	dir := Identity{1, 1, uint32(os.Geteuid()), unix.S_IFDIR | 0700}
	for _, bit := range []uint32{unix.S_ISUID, unix.S_ISGID, unix.S_ISVTX, 0020, 0002} {
		bad := file
		bad.Mode |= bit
		if validFile(bad) {
			t.Fatal("unsafe file mode accepted", bit)
		}
		bad = dir
		bad.Mode |= bit
		if validDir(bad) {
			t.Fatal("unsafe dir mode accepted", bit)
		}
	}
	// Actual inclusive4096-record bound, plus exact1MiB payload boundary.
	records := make([]Record, MaxRecords)
	for i := range records {
		records[i] = Record{Key{"cargo", "", fmt.Sprintf("key-%04d", i)}, nil}
	}
	records[0].Payload = bytes.Repeat([]byte{42}, MaxPayloadBytes)
	root := fixtureRoot(t)
	p, e := Prepare(t.Context(), root, strings.Repeat("a", 32), strings.Repeat("b", 32), records)
	if e != nil {
		t.Fatal(e)
	}
	r, e := Publish(t.Context(), root, p)
	if e != nil {
		t.Fatal(e)
	}
	pack, e := Open(t.Context(), root, r)
	if e != nil {
		t.Fatal(e)
	}
	defer pack.Close()
	raw, e := pack.Read(t.Context(), records[0].Key)
	if e != nil || len(raw) != MaxPayloadBytes || raw[0] != 42 {
		t.Fatal("inclusive limit failed", len(raw), e)
	}
}
func TestFinalHashCanonicalParentReplacementIsUncertain(t *testing.T) {
	root, p := prepared(t)
	old := root + "-retained"
	t.Cleanup(func() { os.RemoveAll(old) })
	changed := false
	ctx := boundaryContext{context.Background(), func() error {
		if _, e := os.Stat(filepath.Join(root, finalName(p.ID))); e == nil && !changed {
			changed = true
			if e = os.Rename(root, old); e != nil {
				t.Fatal(e)
			}
			if e = os.Mkdir(root, 0700); e != nil {
				t.Fatal(e)
			}
		}
		return nil
	}}
	r, e := Publish(ctx, root, p)
	var unknown *PublicationError
	if !changed || !errors.As(e, &unknown) || r.Prepared != p {
		t.Fatal("canonical replacement after final hashing falsely published", e)
	}
	if names, e := os.ReadDir(root); e != nil || len(names) != 0 {
		t.Fatal("replacement root mutated", e)
	}
	if _, e = Open(t.Context(), root, r); e == nil {
		t.Fatal("detached pack treated as canonical")
	}
	if e = os.Remove(root); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(old, root); e != nil {
		t.Fatal(e)
	}
	if _, e = Publish(t.Context(), root, p); e != nil {
		t.Fatal("exact restored root reconciliation failed", e)
	}
}
func TestPublicationLockWaitIsCancellableAndIdentityBound(t *testing.T) {
	root, p := prepared(t)
	f, _, e := openRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	lock, e := acquire(t.Context(), f, false, &p.Lock)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, e = Publish(ctx, root, p); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	lock.close()
	if _, e = Publish(t.Context(), root, p); e != nil {
		t.Fatal(e)
	}
}

func TestWarmReadRejectsChangedManifestAndTrailingBytes(t *testing.T) {
	for _, kind := range []string{"manifest", "records"} {
		t.Run(kind, func(t *testing.T) {
			root, r := published(t)
			pack, e := Open(t.Context(), root, r)
			if e != nil {
				t.Fatal(e)
			}
			defer pack.Close()
			name := "manifest.json"
			if kind == "records" {
				name = "records.bin"
			}
			path := filepath.Join(root, finalName(r.ID), name)
			f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.Write([]byte(" ")); e != nil {
				t.Fatal(e)
			}
			if e = f.Close(); e != nil {
				t.Fatal(e)
			}
			if _, e = pack.Read(t.Context(), fixtureRecords()[0].Key); e == nil {
				t.Fatal("warm read ignored changed EOF/manifest")
			}
		})
	}
}

func TestPrepareSyncFailuresRetainBornEvidence(t *testing.T) {
	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			root := fixtureRoot(t)
			calls := 0
			p, e := prepare(t.Context(), root, strings.Repeat("a", 32), strings.Repeat("b", 32), fixtureRecords(), func(f *os.File) error {
				calls++
				if calls == failAt {
					return unix.EIO
				}
				return f.Sync()
			})
			if !errors.Is(e, unix.EIO) || p.Directory.Inode == 0 {
				t.Fatal("fsync failure lost stage evidence", p, e)
			}
			if _, e = os.Stat(filepath.Join(root, stageName(p.ID))); e != nil {
				t.Fatal("failed stage discarded", e)
			}
			if _, e = os.Stat(filepath.Join(root, finalName(p.ID))); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("failed preparation published", e)
			}
		})
	}
}
func TestPublishSyncAndRenameAcknowledgementFailuresAreUnknown(t *testing.T) {
	for _, kind := range []string{"sync", "rename-ack"} {
		t.Run(kind, func(t *testing.T) {
			root, p := prepared(t)
			syncFile := (*os.File).Sync
			rename := renameNoReplace
			if kind == "sync" {
				syncFile = func(*os.File) error { return unix.EIO }
			} else {
				rename = func(fd int, from, to string) error {
					if e := renameNoReplace(fd, from, to); e != nil {
						return e
					}
					return unix.EIO
				}
			}
			r, e := publish(t.Context(), root, p, syncFile, rename)
			var unknown *PublicationError
			if !errors.As(e, &unknown) || !errors.Is(e, unix.EIO) {
				t.Fatal("publication uncertainty lost", e)
			}
			if _, e = os.Stat(filepath.Join(root, finalName(p.ID))); e != nil {
				t.Fatal("published evidence removed", e)
			}
			got, e := Publish(t.Context(), root, p)
			if e != nil || got != r {
				t.Fatal("exact inspection retry failed", e)
			}
		})
	}
}

func TestManifestCountPreflightBeforeTypedAllocation(t *testing.T) {
	root, p := prepared(t)
	file := filepath.Join(root, stageName(p.ID), "manifest.json")
	header := `{"schema_version":1,"pack_id":"` + p.ID + `","authority":"` + p.Authority + `","records_bytes":1,"records_sha256":"` + strings.Repeat("a", 64) + `","entries":[`
	entry := `{"key":{"domain":"cargo","principal":"","key":"x"},"frame_offset":1,"payload_bytes":0,"sha256":"` + strings.Repeat("b", 64) + `"}`
	// 4097 otherwise fully typed entries must reach the count gate before any
	// full []Entry materialization, even with a matching caller-supplied hash.
	raw := []byte(header + strings.Repeat(entry+",", MaxRecords) + entry + `]}`)
	if len(raw) > MaxManifestBytes {
		t.Fatal("invalid fixture bound")
	}
	if e := preflightManifest(raw); e == nil || !strings.Contains(e.Error(), "entry count") {
		t.Fatal("did not stop at streaming count gate", e)
	}
	if e := os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	p.ManifestBytes = uint64(len(raw))
	p.ManifestSHA256 = digest(raw)
	if _, e := Publish(t.Context(), root, p); e == nil || !strings.Contains(e.Error(), "entry count") {
		t.Fatal("public publication bypassed preflight", e)
	}
	// The many-tiny-object variant is rejected by closed shape immediately.
	raw = []byte(header + strings.Repeat("{},", 200000) + `{}]}`)
	if e := preflightManifest(raw); e == nil {
		t.Fatal("tiny object array accepted")
	}
}

func TestWarmReadFinalGuardParentReplacementRefusesBytes(t *testing.T) {
	root, r := published(t)
	pack, e := Open(t.Context(), root, r)
	if e != nil {
		t.Fatal(e)
	}
	defer pack.Close()
	old := root + "-retained"
	t.Cleanup(func() { os.RemoveAll(old) })
	checks := 0
	changed := false
	ctx := boundaryContext{context.Background(), func() error {
		checks++
		// Read: initial Err1; initial manifest read Err2+3; post-read Err4;
		// final guard begins its manifest hash at Err5, AFTER its early sameRoot.
		if checks == 5 {
			changed = true
			if e := os.Rename(root, old); e != nil {
				t.Fatal(e)
			}
			if e := os.Mkdir(root, 0700); e != nil {
				t.Fatal(e)
			}
		}
		return nil
	}}
	raw, e := pack.Read(ctx, fixtureRecords()[0].Key)
	if !changed || e == nil || raw != nil {
		t.Fatal("late canonical replacement returned detached bytes", checks, len(raw), e)
	}
	if _, e = os.Stat(filepath.Join(old, finalName(r.ID))); e != nil {
		t.Fatal("original evidence lost", e)
	}
}
