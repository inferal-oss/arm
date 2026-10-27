package syncfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"getarm.org/internal/syncreconcile"
	"golang.org/x/sys/unix"
)

type memoryJournal struct {
	prepared, outcomes []Receipt
	failOutcome        bool
}

func (m *memoryJournal) Prepare(_ context.Context, r Receipt) error {
	m.prepared = append(m.prepared, r)
	return nil
}
func (m *memoryJournal) Outcome(_ context.Context, r Receipt) error {
	m.outcomes = append(m.outcomes, r)
	if m.failOutcome {
		return errors.New("lost journal ack")
	}
	return nil
}

func testRealPath(t *testing.T, p string) string {
	t.Helper()
	real, e := filepath.EvalSymlinks(p)
	if e != nil {
		t.Fatal(e)
	}
	return real
}
func testOpen(t *testing.T, p string) *Root {
	t.Helper()
	r, e := OpenRoot(testRealPath(t, p))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { r.Close() })
	return r
}
func testDirs(t *testing.T) (string, *Root, *Root, *Root, NamePolicy) {
	t.Helper()
	base := testRealPath(t, t.TempDir())
	for _, name := range []string{"source", "receiver", "work"} {
		if e := os.Mkdir(filepath.Join(base, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	src, dst, work := testOpen(t, filepath.Join(base, "source")), testOpen(t, filepath.Join(base, "receiver")), testOpen(t, filepath.Join(base, "work"))
	policy, e := DetectNamePolicy(dst, work)
	if e != nil {
		t.Fatal(e)
	}
	return base, src, dst, work, policy
}
func testPlan(t *testing.T, src, dst *Root, names NamePolicy, excluded []string, policy syncreconcile.Policy) syncreconcile.Plan {
	return testPlanMetadata(t, src, dst, names, excluded, policy, Metadata{})
}
func testPlanMetadata(t *testing.T, src, dst *Root, names NamePolicy, excluded []string, policy syncreconcile.Policy, metadata Metadata) syncreconcile.Plan {
	t.Helper()
	ctx := t.Context()
	limits := ScanLimits{Entries: 1000, Bytes: 64 << 20}
	source, e := src.Scan(ctx, nil, excluded, names, metadata, limits)
	if e != nil {
		t.Fatal(e)
	}
	receiver, e := dst.Scan(ctx, nil, excluded, names, metadata, limits)
	if e != nil {
		t.Fatal(e)
	}
	for name := range source.Entries {
		if _, ok := receiver.NameKeys[name]; !ok {
			receiver.NameKeys[name], e = names.Key(name)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	for name := range receiver.Entries {
		if _, ok := source.NameKeys[name]; !ok {
			source.NameKeys[name], e = names.Key(name)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	req := syncreconcile.Request{Binding: syncreconcile.Binding{Local: syncreconcile.Endpoint{ID: "local-node", RootID: src.Path()}, Remote: syncreconcile.Endpoint{ID: "remote-node", RootID: dst.Path()}, SelectionID: "all", MetadataID: "content", Exclusions: excluded}, Direction: syncreconcile.Push, LocalPolicy: syncreconcile.Additive, RemotePolicy: policy, Local: source, Remote: receiver, FirstRun: true}
	plan, e := syncreconcile.Reconcile(req)
	if e != nil {
		t.Fatal(e)
	}
	if len(plan.Conflicts()) != 0 {
		t.Fatal(plan.Conflicts())
	}
	return plan
}
func testWork(t *testing.T, dst, work *Root, plan syncreconcile.Plan) *Workspace {
	t.Helper()
	w, e := CreateWorkspace(dst, work, Reservation{Key: "testoperation0001", PlanDigest: plan.Digest(), Generation: plan.Generation(), Side: syncreconcile.Remote, DiskBytes: 64 << 20})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	return w
}
func assertContent(t *testing.T, p string, want []byte) {
	t.Helper()
	got, e := os.ReadFile(p)
	if e != nil || sha256.Sum256(got) != sha256.Sum256(want) {
		t.Fatalf("content at %s: %v", p, e)
	}
}

func TestScanExclusionAncestorAndNativeKeys(t *testing.T) {
	base, src, _, work, names := testDirs(t)
	if e := os.Mkdir(filepath.Join(src.Path(), "dir"), 0700); e != nil {
		t.Fatal(e)
	}
	for name := range map[string]bool{"keep": true, "other": true} {
		if e := os.WriteFile(filepath.Join(src.Path(), "dir", name), []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	snap, e := src.Scan(t.Context(), []string{"future/name"}, []string{"dir/keep"}, names, Metadata{}, ScanLimits{Entries: 100, Bytes: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := snap.Entries["dir/keep"]; ok {
		t.Fatal("excluded entry scanned")
	}
	if _, ok := snap.Entries["dir/other"]; !ok {
		t.Fatal("selected sibling missing")
	}
	second, e := src.Scan(t.Context(), nil, []string{"dir/keep"}, names, Metadata{}, ScanLimits{Entries: 100, Bytes: 1 << 20})
	if e != nil || len(second.Entries) != len(snap.Entries) {
		t.Fatalf("repeat scan lost directory entries: %v, first=%d second=%d", e, len(snap.Entries), len(second.Entries))
	}
	for _, name := range []string{"dir", "dir/keep", "future", "future/name"} {
		if snap.NameKeys[name] == "" {
			t.Fatalf("missing native key for %q", name)
		}
	}
	if _, e := names.Key("café"); e == nil {
		t.Fatal("unsupported Unicode native key accepted")
	}
	_ = base
	_ = work
}

func TestRootRequiresExactCanonicalPath(t *testing.T) {
	base := testRealPath(t, t.TempDir())
	if _, e := OpenRoot(base + "/."); e == nil {
		t.Fatal("root path alias accepted")
	}
}

func TestNameProbePreservesReplacedEntry(t *testing.T) {
	_, _, _, scratch, _ := testDirs(t)
	name := ".ARMcaseReplacement"
	fd, e := unix.Openat(scratch.FD(), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer unix.Close(fd)
	if e := os.Rename(filepath.Join(scratch.Path(), name), filepath.Join(scratch.Path(), name+"-moved")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(scratch.Path(), name), []byte("replacement"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := finishNameProbe(scratch, fd, name, ".armcasereplacement"); !errors.Is(e, ErrChanged) {
		t.Fatalf("replaced probe accepted: %v", e)
	}
	assertContent(t, filepath.Join(scratch.Path(), name), []byte("replacement"))
}

func TestWorkspaceDoesNotAdoptReplacedDirectory(t *testing.T) {
	_, _, _, parent, _ := testDirs(t)
	name := "created"
	if e := unix.Mkdirat(parent.FD(), name, 0700); e != nil {
		t.Fatal(e)
	}
	created, e := statNamed(parent.FD(), name)
	if e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(filepath.Join(parent.Path(), name), filepath.Join(parent.Path(), name+"-moved")); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(parent.Path(), name), 0700); e != nil {
		t.Fatal(e)
	}
	if child, e := openCreatedChild(parent, name, created); !errors.Is(e, ErrChanged) {
		if child != nil {
			_ = child.Close()
		}
		t.Fatalf("replaced workspace directory adopted: %v", e)
	}
}

func TestWorkspaceDoesNotAdoptReplacedParentRoute(t *testing.T) {
	base, _, _, parent, _ := testDirs(t)
	name := "created"
	if e := unix.Mkdirat(parent.FD(), name, 0700); e != nil {
		t.Fatal(e)
	}
	created, e := statNamed(parent.FD(), name)
	if e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(parent.Path(), filepath.Join(base, "work-moved")); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(parent.Path(), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(parent.Path(), name), 0700); e != nil {
		t.Fatal(e)
	}
	if child, e := openCreatedChild(parent, name, created); !errors.Is(e, ErrChanged) {
		if child != nil {
			_ = child.Close()
		}
		t.Fatalf("replaced workspace parent adopted: %v", e)
	}
}

func TestPublishOverwriteRetainsPreimage(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	old, newData := []byte("old reviewed bytes"), []byte("new strongly verified bytes")
	if e := os.WriteFile(filepath.Join(dst.Path(), "data"), old, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src.Path(), "data"), newData, 0600); e != nil {
		t.Fatal(e)
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Additive)
	w := testWork(t, dst, work, plan)
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "data"), newData, 0600); e != nil {
		t.Fatal(e)
	}
	j := &memoryJournal{}
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, j)
	if e != nil {
		t.Fatal(e)
	}
	receipts, e := p.ApplyAll(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if len(receipts) != 1 || receipts[0].State != "applied" || receipts[0].Preimage == "" || len(j.prepared) != 1 || len(j.outcomes) != 1 {
		t.Fatal(receipts, j)
	}
	assertContent(t, filepath.Join(dst.Path(), "data"), newData)
	assertContent(t, receipts[0].Preimage, old)
	if _, e := p.ApplyAll(t.Context()); e == nil {
		t.Fatal("same worker replayed")
	}
}

func TestFullRemovalQuarantinesExactEntry(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	old := []byte("reviewed removal")
	if e := os.WriteFile(filepath.Join(dst.Path(), "orphan"), old, 0600); e != nil {
		t.Fatal(e)
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Full)
	w := testWork(t, dst, work, plan)
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, &memoryJournal{})
	if e != nil {
		t.Fatal(e)
	}
	receipts, e := p.ApplyAll(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if len(receipts) != 1 || receipts[0].Preimage == "" {
		t.Fatal(receipts)
	}
	if _, e := os.Lstat(filepath.Join(dst.Path(), "orphan")); !os.IsNotExist(e) {
		t.Fatalf("orphan still visible: %v", e)
	}
	assertContent(t, receipts[0].Preimage, old)
}

func TestConcurrentEditBeforeCutIsConflict(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	for root, data := range map[*Root]string{src: "new", dst: "old"} {
		if e := os.WriteFile(filepath.Join(root.Path(), "data"), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Additive)
	w := testWork(t, dst, work, plan)
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "data"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dst.Path(), "data"), []byte("external edit"), 0600); e != nil {
		t.Fatal(e)
	}
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, &memoryJournal{})
	if e != nil {
		t.Fatal(e)
	}
	receipts, e := p.ApplyAll(t.Context())
	if !errors.Is(e, ErrChanged) {
		t.Fatalf("got %v", e)
	}
	if len(receipts) != 1 || receipts[0].State != "conflict" {
		t.Fatal(receipts)
	}
	assertContent(t, filepath.Join(dst.Path(), "data"), []byte("external edit"))
}

func TestLaterStagedMismatchBlocksEarlierPublication(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	for _, name := range []string{"a", "b"} {
		if e := os.WriteFile(filepath.Join(src.Path(), name), []byte(name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Additive)
	w := testWork(t, dst, work, plan)
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "a"), []byte("a"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "b"), []byte("wrong"), 0600); e != nil {
		t.Fatal(e)
	}
	j := &memoryJournal{}
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, j)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := p.ApplyAll(t.Context()); !errors.Is(e, ErrChanged) {
		t.Fatalf("expected staged conflict, got %v", e)
	}
	if len(j.prepared) != 0 {
		t.Fatal("an effect was prepared before all staged content verified")
	}
	for _, name := range []string{"a", "b"} {
		if _, e := os.Lstat(filepath.Join(dst.Path(), name)); !os.IsNotExist(e) {
			t.Fatalf("%q published despite later mismatch: %v", name, e)
		}
	}
}

func TestRootReplacementStopsBeforeMutation(t *testing.T) {
	base, src, dst, work, names := testDirs(t)
	if e := os.WriteFile(filepath.Join(src.Path(), "data"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Additive)
	w := testWork(t, dst, work, plan)
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "data"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(dst.Path(), dst.Path()+"-moved"); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(base, "receiver"), 0700); e != nil {
		t.Fatal(e)
	}
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, &memoryJournal{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.ApplyAll(t.Context()); !errors.Is(e, ErrChanged) {
		t.Fatalf("got %v", e)
	}
	for _, path := range []string{dst.Path(), dst.Path() + "-moved"} {
		if _, e := os.Lstat(filepath.Join(path, "data")); !os.IsNotExist(e) {
			t.Fatalf("mutation in %s: %v", path, e)
		}
	}
}

func TestJournalLostOutcomeRetainsUnknown(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	if e := os.WriteFile(filepath.Join(src.Path(), "data"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Additive)
	w := testWork(t, dst, work, plan)
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "data"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	j := &memoryJournal{failOutcome: true}
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, j)
	if e != nil {
		t.Fatal(e)
	}
	receipts, e := p.ApplyAll(t.Context())
	if !errors.Is(e, ErrUnknown) || len(receipts) != 1 {
		t.Fatalf("%v %+v", e, receipts)
	}
	assertContent(t, filepath.Join(dst.Path(), "data"), []byte("new"))
}

func TestSHA256Fingerprint(t *testing.T) {
	_, src, _, _, names := testDirs(t)
	data := []byte("sha256")
	if e := os.WriteFile(filepath.Join(src.Path(), "f"), data, 0600); e != nil {
		t.Fatal(e)
	}
	snap, e := src.Scan(t.Context(), nil, nil, names, Metadata{}, ScanLimits{Entries: 10, Bytes: 100})
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(data)
	if snap.Entries["f"].ContentHash != hex.EncodeToString(h[:]) {
		t.Fatal(snap)
	}
	if bytes.Equal(data, []byte("different")) {
		t.Fatal("bad fixture")
	}
}

func TestScanRejectsUnreviewedPrivilegeMode(t *testing.T) {
	for _, st := range []unix.Stat_t{
		{Mode: unix.S_IFREG | 0600 | unix.S_ISUID},
		{Mode: unix.S_IFREG | 0600 | unix.S_ISGID},
		{Mode: unix.S_IFREG | 0600 | unix.S_ISVTX},
	} {
		if _, e := metadataHash(st, syncreconcile.File, Metadata{}); e == nil {
			t.Fatalf("unreviewed privilege mode %o accepted", st.Mode)
		}
	}
}

func TestNestedDirectoryCreationFinalizesAfterChildren(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	if e := os.Mkdir(filepath.Join(src.Path(), "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src.Path(), "nested", "f"), []byte("nested data"), 0600); e != nil {
		t.Fatal(e)
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Additive)
	w := testWork(t, dst, work, plan)
	if e := os.Mkdir(filepath.Join(w.Stage.Path(), "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "nested", "f"), []byte("nested data"), 0600); e != nil {
		t.Fatal(e)
	}
	j := &memoryJournal{}
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, j)
	if e != nil {
		t.Fatal(e)
	}
	receipts, e := p.ApplyAll(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if len(receipts) != 2 || len(j.outcomes) != 2 {
		t.Fatal(receipts, j)
	}
	for _, r := range receipts {
		if r.State != "applied" {
			t.Fatal(receipts)
		}
	}
	assertContent(t, filepath.Join(dst.Path(), "nested", "f"), []byte("nested data"))
}

func TestDirectoryMetadataFinalizedAfterNestedFile(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	stamp := time.Unix(1600000000, 0)
	dir := filepath.Join(src.Path(), "nested")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "f"), []byte("nested data"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chtimes(filepath.Join(dir, "f"), stamp, stamp); e != nil {
		t.Fatal(e)
	}
	if e := os.Chtimes(dir, stamp, stamp); e != nil {
		t.Fatal(e)
	}
	metadata := Metadata{Permissions: true, ModTimes: true}
	plan := testPlanMetadata(t, src, dst, names, nil, syncreconcile.Additive, metadata)
	w := testWork(t, dst, work, plan)
	if e := os.Mkdir(filepath.Join(w.Stage.Path(), "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "nested", "f"), []byte("nested data"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, rel := range []string{"nested/f", "nested"} {
		if e := os.Chtimes(filepath.Join(w.Stage.Path(), rel), stamp, stamp); e != nil {
			t.Fatal(e)
		}
	}
	p, e := NewPublisher(dst, w, plan, names, metadata, nil, &memoryJournal{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := p.ApplyAll(t.Context()); e != nil {
		t.Fatal(e)
	}
	want, e := src.Scan(t.Context(), nil, nil, names, metadata, ScanLimits{Entries: 10, Bytes: 100})
	if e != nil {
		t.Fatal(e)
	}
	got, e := dst.Scan(t.Context(), nil, nil, names, metadata, ScanLimits{Entries: 10, Bytes: 100})
	wantDir, gotDir := want.Entries["nested"], got.Entries["nested"]
	if e != nil || !equalEntry(&wantDir, &gotDir) {
		t.Fatalf("directory metadata differed: %v, want=%+v got=%+v", e, want.Entries["nested"], got.Entries["nested"])
	}
}

func TestDirectoryBeforeFingerprintChangedAtPreflight(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	if e := os.Mkdir(filepath.Join(src.Path(), "d"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(filepath.Join(dst.Path(), "d"), 0755); e != nil {
		t.Fatal(e)
	}
	metadata := Metadata{Permissions: true}
	plan := testPlanMetadata(t, src, dst, names, nil, syncreconcile.Additive, metadata)
	w := testWork(t, dst, work, plan)
	if e := os.Mkdir(filepath.Join(w.Stage.Path(), "d"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(filepath.Join(dst.Path(), "d"), 0700); e != nil {
		t.Fatal(e)
	}
	j := &memoryJournal{}
	p, e := NewPublisher(dst, w, plan, names, metadata, nil, j)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := p.ApplyAll(t.Context()); !errors.Is(e, ErrChanged) {
		t.Fatalf("expected reviewed Before conflict, got %v", e)
	}
	if len(j.prepared) != 0 {
		t.Fatal("effect prepared after failed preflight")
	}
}

func TestSymlinkCopiedAsLinkWithoutFollowingTarget(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "secret")
	if e := os.Symlink(target, filepath.Join(src.Path(), "link")); e != nil {
		t.Fatal(e)
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Additive)
	w := testWork(t, dst, work, plan)
	if e := os.Symlink(target, filepath.Join(w.Stage.Path(), "link")); e != nil {
		t.Fatal(e)
	}
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, &memoryJournal{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.ApplyAll(t.Context()); e != nil {
		t.Fatal(e)
	}
	got, e := os.Readlink(filepath.Join(dst.Path(), "link"))
	if e != nil || got != target {
		t.Fatal(got, e)
	}
	if _, e := os.Lstat(target); !os.IsNotExist(e) {
		t.Fatalf("symlink target touched: %v", e)
	}
}

func TestReplacedParentSymlinkCannotRedirectPublication(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	outside := t.TempDir()
	for _, r := range []*Root{src, dst} {
		if e := os.Mkdir(filepath.Join(r.Path(), "dir"), 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(src.Path(), "dir", "data"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Additive)
	w := testWork(t, dst, work, plan)
	if e := os.Mkdir(filepath.Join(w.Stage.Path(), "dir"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "dir", "data"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(filepath.Join(dst.Path(), "dir"), filepath.Join(dst.Path(), "dir-old")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(dst.Path(), "dir")); e != nil {
		t.Fatal(e)
	}
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, &memoryJournal{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.ApplyAll(t.Context()); e == nil {
		t.Fatal("replaced parent accepted")
	}
	if _, e := os.Lstat(filepath.Join(outside, "data")); !os.IsNotExist(e) {
		t.Fatalf("outside touched: %v", e)
	}
}

func TestCancelledPublisherHasNoEffect(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	if e := os.WriteFile(filepath.Join(src.Path(), "data"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	plan := testPlan(t, src, dst, names, nil, syncreconcile.Additive)
	w := testWork(t, dst, work, plan)
	if e := os.WriteFile(filepath.Join(w.Stage.Path(), "data"), []byte("new"), 0600); e != nil {
		t.Fatal(e)
	}
	j := &memoryJournal{}
	p, e := NewPublisher(dst, w, plan, names, Metadata{}, nil, j)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = p.ApplyAll(ctx); !errors.Is(e, context.Canceled) {
		t.Fatalf("got %v", e)
	}
	if len(j.prepared) != 0 {
		t.Fatal("cancelled effect journaled")
	}
	if _, e := os.Lstat(filepath.Join(dst.Path(), "data")); !os.IsNotExist(e) {
		t.Fatalf("cancelled effect visible: %v", e)
	}
}
