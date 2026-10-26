package syncfs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"slices"
	"strings"

	"github.com/inferal-oss/arm/internal/syncreconcile"
	"golang.org/x/sys/unix"
)

// ErrNativeIdentityUnavailable means a persisted baseline cannot safely
// distinguish root inode reuse on this filesystem.
var ErrNativeIdentityUnavailable = errors.New("native root creation identity unavailable; re-review or rebaseline")

type rootCreationEvidence struct {
	Method     string
	BirthSec   int64
	BirthNsec  uint32
	Generation *uint64 `json:",omitempty"`
	MountID    *uint64 `json:",omitempty"`
}

func nativeIDFromEvidence(path string, st unix.Stat_t, fs unix.Statfs_t, creation rootCreationEvidence) (string, error) {
	value := struct {
		Version, OS, Path string
		Device, Inode     uint64
		FilesystemID      [2]int32
		FilesystemType    uint64
		Creation          rootCreationEvidence
	}{"arm-sync-root-v2", runtime.GOOS, path, uint64(st.Dev), uint64(st.Ino), fs.Fsid.Val, uint64(fs.Type), creation}
	raw, e := json.Marshal(value)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(raw)
	return "arm-sync-root-v2:" + hex.EncodeToString(sum[:]), nil
}

// NativeID binds a canonical named root to the held directory, filesystem,
// and supported native creation evidence. The caller must include effective
// account/enrollment in Endpoint.ID too. Earlier IDs intentionally do not
// match, so a baseline with weaker root identity cannot be reused silently.
func (r *Root) NativeID() (string, error) {
	if e := r.Check(); e != nil {
		return "", e
	}
	st, e := statFD(r.FD())
	if e != nil {
		return "", e
	}
	var fs unix.Statfs_t
	if e := unix.Fstatfs(r.FD(), &fs); e != nil {
		return "", e
	}
	creation, e := readNativeRootEvidence(r.FD(), st)
	if e != nil {
		return "", e
	}
	if e := r.Check(); e != nil {
		return "", e
	}
	return nativeIDFromEvidence(r.Path(), st, fs, creation)
}

// VerifyPlanRoot refuses a stale or different native endpoint before any
// source materialization or receiver basis copy.
func VerifyPlanRoot(plan syncreconcile.Plan, side syncreconcile.Side, root *Root) error {
	if root == nil {
		return fmt.Errorf("nil selected root")
	}
	proposal, ok := plan.ProposedBaseline()
	if !ok || len(plan.Conflicts()) != 0 {
		return fmt.Errorf("conflict-free reviewed plan required")
	}
	want := ""
	switch side {
	case syncreconcile.Local:
		want = proposal.Binding.Local.RootID
	case syncreconcile.Remote:
		want = proposal.Binding.Remote.RootID
	default:
		return fmt.Errorf("invalid endpoint side")
	}
	actual, e := root.NativeID()
	if e != nil {
		return e
	}
	if actual != want {
		return ErrChanged
	}
	return nil
}

// NewBoundPublisher is the sync worker entry for publication. It verifies the
// receiver's held native root against the reviewed plan before constructing
// the lower-level publisher.
func NewBoundPublisher(root *Root, work *Workspace, plan syncreconcile.Plan, names NamePolicy, metadata Metadata, exclusions []string, journal Journal) (*Publisher, error) {
	if work == nil {
		return nil, fmt.Errorf("nil receiver workspace")
	}
	if e := VerifyPlanRoot(plan, work.Reservation.Side, root); e != nil {
		return nil, e
	}
	return NewPublisher(root, work, plan, names, metadata, exclusions, journal)
}

// StageCharge is a logical lower bound. The caller must reserve and observe
// actual filesystem allocation, temporary rsync files, metadata and journals.
type StageCharge struct {
	Entries int
	Bytes   int64
}

// DirectoryMetadata is a plan-bound sidecar for restoring staged directory
// metadata after all rsync batches. Child transfer changes directory mtimes.
type DirectoryMetadata struct {
	Path     string
	Mode     uint32
	MTimeSec int64
}

func privateStage(selected, stage *Root) error {
	if selected == nil || stage == nil {
		return fmt.Errorf("selected and private stage roots required")
	}
	if e := selected.Check(); e != nil {
		return e
	}
	if e := stage.Check(); e != nil {
		return e
	}
	if selected.Path() == stage.Path() || strings.HasPrefix(stage.Path(), selected.Path()+"/") || strings.HasPrefix(selected.Path(), stage.Path()+"/") {
		return fmt.Errorf("private stage overlaps selected root")
	}
	a, e := statFD(selected.FD())
	if e != nil {
		return e
	}
	b, e := statFD(stage.FD())
	if e != nil {
		return e
	}
	if a.Dev != b.Dev || b.Uid != uint32(os.Geteuid()) || b.Mode&0077 != 0 {
		return fmt.Errorf("stage must be private, account-owned and on selected filesystem")
	}
	for _, ancestor := range stage.chain {
		st, e := statFD(int(ancestor.Fd()))
		if e != nil {
			return e
		}
		if sameObject(a, st) {
			return fmt.Errorf("stage aliases selected root")
		}
	}
	for _, ancestor := range selected.chain {
		st, e := statFD(int(ancestor.Fd()))
		if e != nil {
			return e
		}
		if sameObject(b, st) {
			return fmt.Errorf("stage aliases an ancestor of selected root")
		}
	}
	return nil
}

func emptyRoot(r *Root) error {
	fd, e := unix.Openat(r.FD(), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), "private-stage")
	defer f.Close()
	items, e := f.ReadDir(1)
	if e != nil && e != io.EOF {
		return e
	}
	if len(items) != 0 {
		return fmt.Errorf("private stage must start empty")
	}
	return r.Check()
}

type stageBuilder struct {
	root         *Root
	known        map[string]unix.Stat_t
	expected     map[string]bool
	fingerprints map[string]syncreconcile.Entry
	metadata     Metadata
	charge       StageCharge
	limit        int64
}

func newStageBuilder(root *Root, metadata Metadata, limit int64) (*stageBuilder, error) {
	if limit <= 0 || limit > 1<<40 {
		return nil, fmt.Errorf("invalid private stage byte limit")
	}
	if e := emptyRoot(root); e != nil {
		return nil, e
	}
	return &stageBuilder{root: root, known: map[string]unix.Stat_t{}, expected: map[string]bool{}, fingerprints: map[string]syncreconcile.Entry{}, metadata: metadata, limit: limit}, nil
}

func (b *stageBuilder) chargeEntry(name string, size int64) error {
	if b.expected[name] || b.charge.Entries >= syncreconcile.MaxEntries || size < 0 || size > b.limit-b.charge.Bytes {
		return fmt.Errorf("private stage path or byte bound exceeded")
	}
	b.expected[name] = true
	b.charge.Entries++
	b.charge.Bytes += size
	return nil
}

func (b *stageBuilder) ensureDir(name string) error {
	if name == "." {
		return nil
	}
	if e := validateRelative(name); e != nil {
		return e
	}
	if e := b.ensureDir(path.Dir(name)); e != nil {
		return e
	}
	h, base, e := b.root.parent(name)
	if e != nil {
		return e
	}
	defer h.Close()
	st, e := statNamed(h.fd, base)
	if errors.Is(e, unix.ENOENT) {
		if e = unix.Mkdirat(h.fd, base, 0700); e != nil {
			return e
		}
		st, e = statNamed(h.fd, base)
		if e != nil {
			return e
		}
		if e = unix.Fsync(h.fd); e != nil {
			return e
		}
		b.known[name] = st
		return b.chargeEntry(name, 0)
	}
	if e != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return ErrChanged
	}
	prior, ok := b.known[name]
	if !ok || !sameObject(prior, st) {
		return ErrChanged
	}
	return nil
}

func (b *stageBuilder) copyEntry(ctx context.Context, source *Root, sourcePath, destination string, expected syncreconcile.Entry, metadata Metadata) error {
	if e := b.ensureDir(path.Dir(destination)); e != nil {
		return e
	}
	if expected.Kind == syncreconcile.Directory {
		if e := b.ensureDir(destination); e != nil {
			return e
		}
		return nil
	}
	if e := b.chargeEntry(destination, expected.Size); e != nil {
		return e
	}
	sourceParent, sourceName, e := source.parent(sourcePath)
	if e != nil {
		return e
	}
	defer sourceParent.Close()
	destParent, destName, e := b.root.parent(destination)
	if e != nil {
		return e
	}
	defer destParent.Close()
	if expected.Kind == syncreconcile.Symlink {
		buf := make([]byte, 4097)
		n, e := unix.Readlinkat(sourceParent.fd, sourceName, buf)
		if e != nil || n >= len(buf) {
			return ErrChanged
		}
		if e = unix.Symlinkat(string(buf[:n]), destParent.fd, destName); e != nil {
			return e
		}
	} else if expected.Kind == syncreconcile.File {
		if e = copyRegular(ctx, sourceParent.fd, sourceName, destParent.fd, destName, expected.Size, metadata); e != nil {
			return e
		}
	} else {
		return fmt.Errorf("unsupported staged kind")
	}
	if e := unix.Fsync(destParent.fd); e != nil {
		return e
	}
	if e := sourceParent.Check(); e != nil {
		return e
	}
	if e := destParent.Check(); e != nil {
		return e
	}
	actual, e := entryAt(ctx, b.root, destination, metadata, b.limit)
	if e != nil || !equalEntry(actual, &expected) {
		return ErrChanged
	}
	b.fingerprints[destination] = expected
	return nil
}

func copyRegular(ctx context.Context, sourceFD int, sourceName string, destFD int, destName string, expectedSize int64, metadata Metadata) error {
	fd, e := unix.Openat(sourceFD, sourceName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	source := os.NewFile(uintptr(fd), sourceName)
	defer source.Close()
	before, e := statFD(fd)
	if e != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Size != expectedSize || before.Nlink < 1 {
		return ErrChanged
	}
	if metadata.Permissions && before.Mode&0400 == 0 {
		return fmt.Errorf("stage-owned copy cannot preserve unreadable owner mode")
	}
	to, e := unix.Openat(destFD, destName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	dest := os.NewFile(uintptr(to), destName)
	defer dest.Close()
	buf := make([]byte, 128<<10)
	var copied int64
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		n, readErr := source.Read(buf)
		if n > 0 {
			copied += int64(n)
			if copied > expectedSize {
				return ErrChanged
			}
			if _, e := dest.Write(buf[:n]); e != nil {
				return e
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	after, e := statFD(fd)
	if e != nil || copied != expectedSize || !stableStat(before, after) {
		return ErrChanged
	}
	if metadata.Permissions {
		if e = unix.Fchmod(to, uint32(before.Mode&0777)); e != nil {
			return e
		}
	}
	if metadata.ModTimes {
		sec, _ := statMtime(before)
		if e = unix.Futimes(to, []unix.Timeval{{Sec: sec}, {Sec: sec}}); e != nil {
			return e
		}
	}
	return unix.Fsync(to)
}

func (b *stageBuilder) finish(ctx context.Context) error {
	if e := b.root.Check(); e != nil {
		return e
	}
	snapshot, e := b.root.Scan(ctx, nil, nil, NamePolicy{}, b.metadata, ScanLimits{Entries: syncreconcile.MaxEntries, Bytes: b.limit})
	if e != nil {
		return e
	}
	if len(snapshot.Entries) != len(b.expected) {
		return ErrChanged
	}
	for name := range b.expected {
		if _, ok := snapshot.Entries[name]; !ok {
			return ErrChanged
		}
	}
	for name, expected := range b.fingerprints {
		if snapshot.Entries[name] != expected {
			return ErrChanged
		}
	}
	for name, held := range b.known {
		if snapshot.Entries[name].Kind != syncreconcile.Directory {
			return ErrChanged
		}
		current, e := nativeAt(b.root, name)
		if e != nil || !sameObject(held, current) || current.Uid != uint32(os.Geteuid()) || current.Mode&0777 != 0700 {
			return ErrChanged
		}
	}
	var scannedBytes int64
	for _, entry := range snapshot.Entries {
		if entry.Size > b.limit-scannedBytes {
			return ErrChanged
		}
		scannedBytes += entry.Size
	}
	if scannedBytes != b.charge.Bytes {
		return ErrChanged
	}
	return nil
}

// BuildSourceManifest copies only reviewed After sources for one source and
// receiver side into a fresh private tree. The tree can be passed as the
// entire sender root to rsyncengine.Run without exposing other source paths.
// The returned directory records must cross the authenticated control stream
// and be applied to the receiver stage after all transfer batches.
func BuildSourceManifest(ctx context.Context, source, manifest *Root, plan syncreconcile.Plan, sourceSide, receiverSide syncreconcile.Side, names NamePolicy, metadata Metadata, byteLimit int64) ([]DirectoryMetadata, StageCharge, error) {
	if ctx == nil {
		return nil, StageCharge{}, fmt.Errorf("nil staging context")
	}
	if sourceSide != syncreconcile.Local && sourceSide != syncreconcile.Remote || receiverSide != syncreconcile.Local && receiverSide != syncreconcile.Remote {
		return nil, StageCharge{}, fmt.Errorf("invalid source or receiver side")
	}
	if e := VerifyPlanRoot(plan, sourceSide, source); e != nil {
		return nil, StageCharge{}, e
	}
	if e := privateStage(source, manifest); e != nil {
		return nil, StageCharge{}, e
	}
	b, e := newStageBuilder(manifest, metadata, byteLimit)
	if e != nil {
		return nil, StageCharge{}, e
	}
	var chosen []syncreconcile.Action
	for _, a := range plan.Actions() {
		if a.Side != receiverSide || a.After == nil || a.Source != sourceSide {
			continue
		}
		if _, e := names.Key(a.Path); e != nil {
			return nil, b.charge, e
		}
		if _, e := names.Key(a.SourcePath); e != nil {
			return nil, b.charge, e
		}
		observed, e := entryAt(ctx, source, a.SourcePath, metadata, byteLimit)
		if e != nil || !equalEntry(observed, a.After) {
			return nil, b.charge, ErrChanged
		}
		if a.After.Kind == syncreconcile.Directory && metadata.Permissions {
			st, e := nativeAt(source, a.SourcePath)
			if e != nil || st.Mode&0500 != 0500 {
				return nil, b.charge, fmt.Errorf("stage-owned directory requires owner read and search")
			}
		}
		chosen = append(chosen, a)
	}
	var dirs []DirectoryMetadata
	for _, a := range chosen {
		if e := ctx.Err(); e != nil {
			return dirs, b.charge, e
		}
		if a.After.Kind == syncreconcile.Directory {
			st, e := nativeAt(source, a.SourcePath)
			if e != nil {
				return dirs, b.charge, e
			}
			sec, _ := statMtime(st)
			dirs = append(dirs, DirectoryMetadata{Path: a.Path, Mode: uint32(st.Mode & 0777), MTimeSec: sec})
		}
		if e := b.copyEntry(ctx, source, a.SourcePath, a.Path, *a.After, metadata); e != nil {
			return dirs, b.charge, e
		}
		observed, e := entryAt(ctx, source, a.SourcePath, metadata, byteLimit)
		if e != nil || !equalEntry(observed, a.After) {
			return dirs, b.charge, ErrChanged
		}
	}
	if e := b.finish(ctx); e != nil {
		return dirs, b.charge, e
	}
	return dirs, b.charge, nil
}

// SeedReceiverBasis copies each reviewed file Before into an empty private
// receiver stage. It never hard-links a live selected file; rsync may mutate
// these isolated basis copies while reconstructing the reviewed After.
func SeedReceiverBasis(ctx context.Context, receiver, stage *Root, plan syncreconcile.Plan, receiverSide syncreconcile.Side, names NamePolicy, metadata Metadata, byteLimit int64) (StageCharge, error) {
	if ctx == nil {
		return StageCharge{}, fmt.Errorf("nil staging context")
	}
	if e := VerifyPlanRoot(plan, receiverSide, receiver); e != nil {
		return StageCharge{}, e
	}
	if e := privateStage(receiver, stage); e != nil {
		return StageCharge{}, e
	}
	b, e := newStageBuilder(stage, metadata, byteLimit)
	if e != nil {
		return StageCharge{}, e
	}
	var chosen []syncreconcile.Action
	for _, a := range plan.Actions() {
		if a.Side != receiverSide || a.Before == nil || a.After == nil || a.Before.Kind != syncreconcile.File || a.After.Kind != syncreconcile.File {
			continue
		}
		if _, e := names.Key(a.Path); e != nil {
			return b.charge, e
		}
		observed, e := entryAt(ctx, receiver, a.Path, metadata, byteLimit)
		if e != nil || !equalEntry(observed, a.Before) {
			return b.charge, ErrChanged
		}
		chosen = append(chosen, a)
	}
	for _, a := range chosen {
		if e := b.copyEntry(ctx, receiver, a.Path, a.Path, *a.Before, metadata); e != nil {
			return b.charge, e
		}
		observed, e := entryAt(ctx, receiver, a.Path, metadata, byteLimit)
		if e != nil || !equalEntry(observed, a.Before) {
			return b.charge, ErrChanged
		}
	}
	if e := b.finish(ctx); e != nil {
		return b.charge, e
	}
	return b.charge, nil
}

// FinalizeStageDirectories restores reviewed directory modes and mtimes after
// all rsync batches. Records must cover exactly the receiver's directory After
// actions, regardless of which endpoint supplied their contents.
func FinalizeStageDirectories(ctx context.Context, receiver, stage *Root, plan syncreconcile.Plan, receiverSide syncreconcile.Side, metadata Metadata, records []DirectoryMetadata) error {
	if ctx == nil {
		return fmt.Errorf("stage context required")
	}
	if e := VerifyPlanRoot(plan, receiverSide, receiver); e != nil {
		return e
	}
	if e := privateStage(receiver, stage); e != nil {
		return e
	}
	expected := map[string]syncreconcile.Entry{}
	for _, a := range plan.Actions() {
		if a.Side == receiverSide && a.After != nil && a.After.Kind == syncreconcile.Directory {
			expected[a.Path] = *a.After
		}
	}
	if len(records) != len(expected) {
		return ErrChanged
	}
	byPath := map[string]DirectoryMetadata{}
	for _, record := range records {
		want, ok := expected[record.Path]
		if !ok || record.Mode > 0777 || byPath[record.Path].Path != "" || metadata.Permissions && record.Mode&0500 != 0500 {
			return ErrChanged
		}
		value := "v1"
		if metadata.Permissions {
			value += fmt.Sprintf(":mode=%03o", record.Mode)
		}
		if metadata.ModTimes {
			value += fmt.Sprintf(":mtime=%d", record.MTimeSec)
		}
		h := sha256.Sum256([]byte(value))
		if hex.EncodeToString(h[:]) != want.MetadataHash {
			return ErrChanged
		}
		byPath[record.Path] = record
	}
	paths := make([]string, 0, len(expected))
	for name := range expected {
		paths = append(paths, name)
	}
	slices.SortFunc(paths, func(a, b string) int {
		if x, y := strings.Count(a, "/"), strings.Count(b, "/"); x != y {
			return y - x
		}
		return strings.Compare(a, b)
	})
	for _, name := range paths {
		if e := ctx.Err(); e != nil {
			return e
		}
		record := byPath[name]
		h, base, e := stage.parent(name)
		if e != nil {
			return e
		}
		fd, e := unix.Openat(h.fd, base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			h.Close()
			return e
		}
		if metadata.Permissions {
			e = unix.Fchmod(fd, record.Mode)
		}
		if e == nil && metadata.ModTimes {
			e = unix.Futimes(fd, []unix.Timeval{{Sec: record.MTimeSec}, {Sec: record.MTimeSec}})
		}
		if e == nil {
			e = unix.Fsync(fd)
		}
		_ = unix.Close(fd)
		if e == nil {
			e = unix.Fsync(h.fd)
		}
		h.Close()
		if e != nil {
			return e
		}
		observed, e := entryAt(ctx, stage, name, metadata, 1<<40)
		want := expected[name]
		if e != nil || !equalEntry(observed, &want) {
			return ErrChanged
		}
	}
	return stage.Check()
}
