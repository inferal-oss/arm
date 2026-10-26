package syncfs

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/rsyncengine"
	"github.com/yrashk/arm/internal/syncreconcile"
	"golang.org/x/sys/unix"
)

func nativePlan(t *testing.T, src, dst *Root, names NamePolicy, exclusions []string, metadata Metadata) syncreconcile.Plan {
	t.Helper()
	local, e := src.Scan(t.Context(), nil, exclusions, names, metadata, ScanLimits{Entries: 100, Bytes: 16 << 20})
	if e != nil {
		t.Fatal(e)
	}
	remote, e := dst.Scan(t.Context(), nil, exclusions, names, metadata, ScanLimits{Entries: 100, Bytes: 16 << 20})
	if e != nil {
		t.Fatal(e)
	}
	for name := range local.NameKeys {
		if _, ok := remote.NameKeys[name]; !ok {
			remote.NameKeys[name], e = names.Key(name)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	for name := range remote.NameKeys {
		if _, ok := local.NameKeys[name]; !ok {
			local.NameKeys[name], e = names.Key(name)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	srcID, e := src.NativeID()
	if e != nil {
		t.Fatal(e)
	}
	dstID, e := dst.NativeID()
	if e != nil {
		t.Fatal(e)
	}
	req := syncreconcile.Request{Binding: syncreconcile.Binding{Local: syncreconcile.Endpoint{ID: "local/account", RootID: srcID}, Remote: syncreconcile.Endpoint{ID: "remote/account", RootID: dstID}, SelectionID: "reviewed", MetadataID: "selected", Exclusions: exclusions}, Direction: syncreconcile.Push, LocalPolicy: syncreconcile.Additive, RemotePolicy: syncreconcile.Additive, Local: local, Remote: remote, FirstRun: true}
	plan, e := syncreconcile.Reconcile(req)
	if e != nil || len(plan.Conflicts()) != 0 {
		t.Fatalf("native plan: %v conflicts=%+v", e, plan.Conflicts())
	}
	return plan
}

func runStagedRsync(t *testing.T, source, target string, metadata Metadata) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	server := make(chan error, 1)
	go func() {
		_, e := rsyncengine.Run(ctx, b, rsyncengine.ServerReceiver, target, rsyncengine.Metadata{Symlinks: true, Permissions: metadata.Permissions, ModTimes: metadata.ModTimes})
		server <- e
	}()
	_, clientErr := rsyncengine.Run(ctx, a, rsyncengine.ClientSender, source, rsyncengine.Metadata{Symlinks: true, Permissions: metadata.Permissions, ModTimes: metadata.ModTimes})
	if clientErr != nil {
		t.Fatal(clientErr)
	}
	if serverErr := <-server; serverErr != nil {
		t.Fatal(serverErr)
	}
}

func TestSelectedManifestAndBasisOverRsync(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	old := bytes.Repeat([]byte("old rolling block\n"), 2048)
	newData := bytes.Clone(old)
	copy(newData[12000:], []byte("new reviewed data"))
	if e := os.WriteFile(filepath.Join(src.Path(), "selected"), newData, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(src.Path(), "excluded"), []byte("never transfer this"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dst.Path(), "selected"), old, 0600); e != nil {
		t.Fatal(e)
	}
	plan := nativePlan(t, src, dst, names, []string{"excluded"}, Metadata{})
	makeWork := func(root *Root, side syncreconcile.Side) *Workspace {
		w, e := CreateWorkspace(root, work, Reservation{Key: "source-manifest-123456", PlanDigest: plan.Digest(), Generation: plan.Generation(), Side: side, DiskBytes: 8 << 20})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = w.Close() })
		return w
	}
	sender, receiver := makeWork(src, syncreconcile.Local), makeWork(dst, syncreconcile.Remote)
	if e := VerifyPlanRoot(plan, syncreconcile.Local, src); e != nil {
		t.Fatal(e)
	}
	if _, charge, e := BuildSourceManifest(t.Context(), src, sender.Stage, plan, syncreconcile.Local, syncreconcile.Remote, names, Metadata{}, 8<<20); e != nil || charge.Bytes != int64(len(newData)) {
		t.Fatalf("source manifest: charge=%+v err=%v", charge, e)
	}
	if _, e := os.Lstat(filepath.Join(sender.StagePath(), "excluded")); !os.IsNotExist(e) {
		t.Fatalf("excluded source entered manifest: %v", e)
	}
	if charge, e := SeedReceiverBasis(t.Context(), dst, receiver.Stage, plan, syncreconcile.Remote, names, Metadata{}, 8<<20); e != nil || charge.Bytes != int64(len(old)) {
		t.Fatalf("receiver basis: charge=%+v err=%v", charge, e)
	}
	oldNative, e := nativeAt(dst, "selected")
	if e != nil {
		t.Fatal(e)
	}
	basisNative, e := nativeAt(receiver.Stage, "selected")
	if e != nil || sameObject(oldNative, basisNative) {
		t.Fatalf("basis hard-linked live receiver: %v", e)
	}
	runStagedRsync(t, sender.StagePath(), receiver.StagePath(), Metadata{})
	if e := FinalizeStageDirectories(t.Context(), dst, receiver.Stage, plan, syncreconcile.Remote, Metadata{}, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := NewBoundPublisher(src, receiver, plan, names, Metadata{}, []string{"excluded"}, &memoryJournal{}); !errors.Is(e, ErrChanged) {
		t.Fatalf("wrong held receiver root accepted: %v", e)
	}
	publisher, e := NewBoundPublisher(dst, receiver, plan, names, Metadata{}, []string{"excluded"}, &memoryJournal{})
	if e != nil {
		t.Fatal(e)
	}
	if receipts, e := publisher.ApplyAll(t.Context()); e != nil || len(receipts) != 1 || receipts[0].State != "applied" {
		t.Fatalf("publication: receipts=%+v err=%v", receipts, e)
	}
	assertContent(t, filepath.Join(dst.Path(), "selected"), newData)
	if _, e := os.Lstat(filepath.Join(dst.Path(), "excluded")); !os.IsNotExist(e) {
		t.Fatalf("excluded receiver path created: %v", e)
	}
}

func TestNativeRootIDChangesWhenNamedRootReplaced(t *testing.T) {
	base, src, _, _, _ := testDirs(t)
	first, e := src.NativeID()
	if e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(src.Path(), filepath.Join(base, "source-moved")); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(src.Path(), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e := src.NativeID(); e == nil {
		t.Fatal("old held root accepted replacement")
	}
	if e := src.Close(); e != nil {
		t.Fatal(e)
	}
	replacement := testOpen(t, src.Path())
	second, e := replacement.NativeID()
	if e != nil || second == first {
		t.Fatalf("replacement reused native root ID: %v", e)
	}
}

func TestNativeRootIDIncludesCreationEvidenceBeyondInode(t *testing.T) {
	st := unix.Stat_t{Dev: 7, Ino: 42}
	fs := unix.Statfs_t{}
	first, e := nativeIDFromEvidence("/reviewed/root", st, fs, rootCreationEvidence{Method: "test-birthtime", BirthSec: 1000, BirthNsec: 1})
	if e != nil {
		t.Fatal(e)
	}
	second, e := nativeIDFromEvidence("/reviewed/root", st, fs, rootCreationEvidence{Method: "test-birthtime", BirthSec: 1000, BirthNsec: 2})
	if e != nil || second == first {
		t.Fatalf("reused inode with different creation evidence kept the same ID: %v", e)
	}
}

func TestKeepBothSourcePathIsMappedIntoManifest(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	localData, remoteData := []byte("local version"), []byte("remote version")
	if e := os.WriteFile(filepath.Join(src.Path(), "f"), localData, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dst.Path(), "f"), remoteData, 0600); e != nil {
		t.Fatal(e)
	}
	local, e := src.Scan(t.Context(), []string{"f.local", "f.remote"}, nil, names, Metadata{}, ScanLimits{Entries: 10, Bytes: 1000})
	if e != nil {
		t.Fatal(e)
	}
	remote, e := dst.Scan(t.Context(), []string{"f.local", "f.remote"}, nil, names, Metadata{}, ScanLimits{Entries: 10, Bytes: 1000})
	if e != nil {
		t.Fatal(e)
	}
	srcID, e := src.NativeID()
	if e != nil {
		t.Fatal(e)
	}
	dstID, e := dst.NativeID()
	if e != nil {
		t.Fatal(e)
	}
	req := syncreconcile.Request{Binding: syncreconcile.Binding{Local: syncreconcile.Endpoint{ID: "local/account", RootID: srcID}, Remote: syncreconcile.Endpoint{ID: "remote/account", RootID: dstID}, SelectionID: "reviewed", MetadataID: "content"}, Direction: syncreconcile.Both, LocalPolicy: syncreconcile.Additive, RemotePolicy: syncreconcile.Additive, Local: local, Remote: remote, FirstRun: true}
	initial, e := syncreconcile.Reconcile(req)
	if e != nil || len(initial.Conflicts()) != 1 {
		t.Fatalf("expected one conflict: %v %+v", e, initial.Conflicts())
	}
	localEntry, remoteEntry := local.Entries["f"], remote.Entries["f"]
	req.Resolutions = []syncreconcile.Resolution{{Path: "f", Choice: syncreconcile.KeepBoth, ExpectedLocal: &localEntry, ExpectedRemote: &remoteEntry, LocalName: "f.local", RemoteName: "f.remote"}}
	plan, e := syncreconcile.Reconcile(req)
	if e != nil || len(plan.Conflicts()) != 0 {
		t.Fatalf("keep-both plan: %v %+v", e, plan.Conflicts())
	}
	w, e := CreateWorkspace(src, work, Reservation{Key: "keep-both-manifest-123", PlanDigest: plan.Digest(), Generation: plan.Generation(), Side: syncreconcile.Local, DiskBytes: 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = w.Close() })
	_, charge, e := BuildSourceManifest(t.Context(), src, w.Stage, plan, syncreconcile.Local, syncreconcile.Remote, names, Metadata{}, 1<<20)
	if e != nil || charge.Bytes != int64(len(localData)) {
		t.Fatalf("mapped manifest: %+v %v", charge, e)
	}
	assertContent(t, filepath.Join(w.StagePath(), "f.local"), localData)
	if _, e := os.Lstat(filepath.Join(w.StagePath(), "f")); !os.IsNotExist(e) {
		t.Fatalf("unmapped source path staged: %v", e)
	}
}

func TestDirectorySidecarRestoresStageAfterRsync(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	dir := filepath.Join(src.Path(), "nested")
	if e := os.Mkdir(dir, 0750); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "f"), []byte("directory child"), 0640); e != nil {
		t.Fatal(e)
	}
	stamp := time.Unix(1600000000, 0)
	for _, name := range []string{filepath.Join(dir, "f"), dir} {
		if e := os.Chtimes(name, stamp, stamp); e != nil {
			t.Fatal(e)
		}
	}
	metadata := Metadata{Permissions: true, ModTimes: true}
	plan := nativePlan(t, src, dst, names, nil, metadata)
	makeWork := func(root *Root, side syncreconcile.Side) *Workspace {
		w, e := CreateWorkspace(root, work, Reservation{Key: "directory-sidecar-123", PlanDigest: plan.Digest(), Generation: plan.Generation(), Side: side, DiskBytes: 1 << 20})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = w.Close() })
		return w
	}
	sender, receiver := makeWork(src, syncreconcile.Local), makeWork(dst, syncreconcile.Remote)
	dirs, _, e := BuildSourceManifest(t.Context(), src, sender.Stage, plan, syncreconcile.Local, syncreconcile.Remote, names, metadata, 1<<20)
	if e != nil || len(dirs) != 1 || dirs[0].Path != "nested" {
		t.Fatalf("directory sidecar: %+v %v", dirs, e)
	}
	runStagedRsync(t, sender.StagePath(), receiver.StagePath(), metadata)
	tampered := append([]DirectoryMetadata(nil), dirs...)
	tampered[0].MTimeSec++
	if e := FinalizeStageDirectories(t.Context(), dst, receiver.Stage, plan, syncreconcile.Remote, metadata, tampered); !errors.Is(e, ErrChanged) {
		t.Fatalf("tampered directory sidecar accepted: %v", e)
	}
	if e := FinalizeStageDirectories(t.Context(), dst, receiver.Stage, plan, syncreconcile.Remote, metadata, dirs); e != nil {
		t.Fatal(e)
	}
	publisher, e := NewBoundPublisher(dst, receiver, plan, names, metadata, nil, &memoryJournal{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := publisher.ApplyAll(t.Context()); e != nil {
		t.Fatal(e)
	}
	want, e := src.Scan(t.Context(), nil, nil, names, metadata, ScanLimits{Entries: 10, Bytes: 1000})
	if e != nil {
		t.Fatal(e)
	}
	got, e := dst.Scan(t.Context(), nil, nil, names, metadata, ScanLimits{Entries: 10, Bytes: 1000})
	if e != nil || len(got.Entries) != len(want.Entries) {
		t.Fatalf("post-publish scan: %v want=%+v got=%+v", e, want, got)
	}
	for name, expected := range want.Entries {
		if got.Entries[name] != expected {
			t.Fatalf("%q fingerprint differed: want=%+v got=%+v", name, expected, got.Entries[name])
		}
	}
}

func TestSelectedSymlinkStaysLinkThroughRsync(t *testing.T) {
	_, src, dst, work, names := testDirs(t)
	target := filepath.Join(t.TempDir(), "outside-secret")
	if e := os.Symlink(target, filepath.Join(src.Path(), "link")); e != nil {
		t.Fatal(e)
	}
	plan := nativePlan(t, src, dst, names, nil, Metadata{})
	makeWork := func(root *Root, side syncreconcile.Side) *Workspace {
		w, e := CreateWorkspace(root, work, Reservation{Key: "symlink-manifest-123", PlanDigest: plan.Digest(), Generation: plan.Generation(), Side: side, DiskBytes: 1 << 20})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = w.Close() })
		return w
	}
	sender, receiver := makeWork(src, syncreconcile.Local), makeWork(dst, syncreconcile.Remote)
	if _, _, e := BuildSourceManifest(t.Context(), src, sender.Stage, plan, syncreconcile.Local, syncreconcile.Remote, names, Metadata{}, 1<<20); e != nil {
		t.Fatal(e)
	}
	runStagedRsync(t, sender.StagePath(), receiver.StagePath(), Metadata{})
	if e := FinalizeStageDirectories(t.Context(), dst, receiver.Stage, plan, syncreconcile.Remote, Metadata{}, nil); e != nil {
		t.Fatal(e)
	}
	publisher, e := NewBoundPublisher(dst, receiver, plan, names, Metadata{}, nil, &memoryJournal{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := publisher.ApplyAll(t.Context()); e != nil {
		t.Fatal(e)
	}
	link, e := os.Readlink(filepath.Join(dst.Path(), "link"))
	if e != nil || link != target {
		t.Fatalf("symlink target changed: %q %v", link, e)
	}
	if _, e := os.Lstat(target); !os.IsNotExist(e) {
		t.Fatalf("outside symlink target touched: %v", e)
	}
}

func TestAncestorStageCannotFinalizeSelectedRoot(t *testing.T) {
	base, src, dst, _, names := testDirs(t)
	sourceDir := filepath.Join(src.Path(), "receiver")
	if e := os.Mkdir(sourceDir, 0750); e != nil {
		t.Fatal(e)
	}
	stamp := time.Unix(1600000000, 0)
	if e := os.Chtimes(sourceDir, stamp, stamp); e != nil {
		t.Fatal(e)
	}
	metadata := Metadata{Permissions: true, ModTimes: true}
	plan := nativePlan(t, src, dst, names, nil, metadata)
	ancestor := testOpen(t, base)
	before, e := statFD(dst.FD())
	if e != nil {
		t.Fatal(e)
	}
	records := []DirectoryMetadata{{Path: "receiver", Mode: 0750, MTimeSec: stamp.Unix()}}
	if e := FinalizeStageDirectories(t.Context(), dst, ancestor, plan, syncreconcile.Remote, metadata, records); e == nil {
		t.Fatal("ancestor stage accepted for directory finalization")
	}
	after, e := statFD(dst.FD())
	if e != nil || !stableStat(before, after) {
		t.Fatalf("selected root metadata changed through ancestor stage: %v", e)
	}
	if e := privateStage(src, ancestor); e == nil {
		t.Fatal("source manifest accepted ancestor of selected root")
	}
}

func TestStageFinishRejectsReplacedDirectoryPayload(t *testing.T) {
	_, _, _, stage, _ := testDirs(t)
	b, e := newStageBuilder(stage, Metadata{}, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if e := b.ensureDir("d"); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(stage.Path(), "d")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(stage.Path(), "d"), []byte("unreviewed payload"), 0600); e != nil {
		t.Fatal(e)
	}
	if b.charge.Bytes != 0 {
		t.Fatalf("directory charged unexpected bytes: %+v", b.charge)
	}
	if e := b.finish(t.Context()); !errors.Is(e, ErrChanged) {
		t.Fatalf("replaced directory was accepted as selected manifest: %v", e)
	}
}

func TestStageFinishRejectsOpenedDirectory(t *testing.T) {
	_, _, _, stage, _ := testDirs(t)
	b, e := newStageBuilder(stage, Metadata{}, 1<<20)
	if e != nil {
		t.Fatal(e)
	}
	if e := b.ensureDir("d"); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(filepath.Join(stage.Path(), "d"), 0777); e != nil {
		t.Fatal(e)
	}
	if e := b.finish(t.Context()); !errors.Is(e, ErrChanged) {
		t.Fatalf("world-writable stage directory accepted: %v", e)
	}
}
