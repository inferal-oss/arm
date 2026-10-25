package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

const SnapshotChunkBytes = 1 << 20
const snapshotMetadataBytes = 64 << 20
const SnapshotCopyMemoryFloor = 128 << 20
const snapshotInventoryLimit = 8192
const snapshotManifestLimit = 8 << 20

// SnapshotPolicy is part of the immutable copy commitment. Neither a failed
// idle survey nor an absent policy selects the weaker creation guarantee.
type SnapshotPolicy string

const (
	SnapshotIdleNative   SnapshotPolicy = "idle-native-v1"
	SnapshotVerifiedCopy SnapshotPolicy = "verified-copy-v1"
)

func (p SnapshotPolicy) validate() error {
	if p != SnapshotIdleNative && p != SnapshotVerifiedCopy {
		return fmt.Errorf("select an explicit supported snapshot policy")
	}
	return nil
}

// SnapshotPlan is content/native evidence only. It grants no lease, source pin,
// publication, mount or consumer permission. Planning scans need separately
// admitted work; a preview cannot promise resources or create a retained pin.
type SnapshotPlan struct {
	Native     SnapshotNativeReceipt `json:"native_preparation"`
	Schema     string                `json:"schema"`
	Policy     SnapshotPolicy        `json:"policy"`
	Source     model.Artifact        `json:"source"`
	SourceTree string                `json:"source_tree_sha256"`
	Files      []SnapshotFile        `json:"files"`
	Bytes      int64                 `json:"bytes"`
	SHA256     string                `json:"sha256"`
}

type SnapshotFile struct {
	Version model.ExportFile `json:"version"`
	Chunks  []string         `json:"chunks"`
}

// SnapshotCopyIntent must be committed with the new managed destination lease,
// source pin and finalization/budget reservations by the Manager before mkdir.
// Destination is a new allocation name, never a caller-selected/adopted path.
type SnapshotCopyIntent struct {
	Key           string          `json:"key"`
	Destination   string          `json:"destination_storage_lease"`
	Plan          SnapshotPlan    `json:"plan"`
	DiskPromise   int64           `json:"full_copy_disk_promise"`
	CopyResources model.Resources `json:"copy_resources"`
	CopyFDs       int             `json:"copy_fd_budget"`
}

type SnapshotCopiedFile struct {
	Source SnapshotFile     `json:"source"`
	Output model.ExportFile `json:"output"`
}

// SnapshotGeneration identifies the independent copied objects. It is not a
// public share manifest or permission. S publication must separately bind its
// host/account/grants/consumer leases and immutable metadata mapping.
type SnapshotGeneration struct {
	Schema     string               `json:"schema"`
	Policy     SnapshotPolicy       `json:"policy"`
	Key        string               `json:"copy_key"`
	Generation string               `json:"generation"`
	Artifact   model.Artifact       `json:"artifact"`
	SourceSHA  string               `json:"source_plan_sha256"`
	Files      []SnapshotCopiedFile `json:"files"`
	Bytes      int64                `json:"bytes"`
	Method     string               `json:"copy_method"`
	Sealing    string               `json:"sealing"`
	SHA256     string               `json:"sha256"`
}

// SnapshotCopyResult never turns a timeout or Close error into settled duty.
// A partial allocation remains managed and charged for explicit reconciliation.
// No path is removed here. FilesClosed means each original close returned nil;
// it does not claim physical/APFS-exclusive block reclamation.
type SnapshotCopyResult struct {
	Native       SnapshotNativeReceipt `json:"native_copy"`
	Phase        string                `json:"phase"`
	Artifact     *model.Artifact       `json:"artifact,omitempty"`
	Generation   SnapshotGeneration    `json:"generation"`
	FilesClosed  bool                  `json:"files_closed"`
	UnknownClose bool                  `json:"unknown_close"`
}

// SnapshotCallbacks is an internal authority integration boundary. Intent must
// durably reserve the destination ordinary lease/source pin/budgets BEFORE any
// allocation; Owned must durably record the actual inode BEFORE payload writes.
// Prepared commits evidence only after all original file/native closes join.
// A failed/lost callback is lookup/reconcile-only, never permission to recopy.
// Current checks entitlement, native policy and its original elapsed deadline;
// callbacks must not renew a lease or rearm the <=5min publication review.
type SnapshotCallbacks struct {
	Intent   func(context.Context, SnapshotCopyIntent) error
	Owned    func(context.Context, SnapshotCopyIntent, model.Artifact) error
	Prepared func(context.Context, SnapshotCopyIntent, SnapshotCopyResult) error
	Current  func(context.Context) error
}

func snapshotCurrent(ctx context.Context, current func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("current snapshot authority required")
	}
	return current(ctx)
}

func snapshotHash(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if len(raw) > snapshotManifestLimit {
		return "", fmt.Errorf("snapshot canonical evidence exceeds bound")
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}

func snapshotDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func (p SnapshotPlan) validate() error {
	if err := p.Policy.validate(); err != nil {
		return err
	}
	if err := p.Native.validateClosed(); err != nil {
		return err
	}
	if p.Schema != "arm.storage.snapshot-plan/v2" || p.Source.Identity == nil || !p.Source.Identity.BirthKnown || p.Source.Phase != "owned" || !validName.MatchString(p.Source.Name) || !snapshotDigest(p.SourceTree) || p.Bytes < 0 || p.Bytes > MaxExportBytes || len(p.Files) < 1 || len(p.Files) > MaxExportFiles {
		return fmt.Errorf("invalid complete snapshot source plan")
	}
	selections := make([]model.ExportSelection, len(p.Files))
	var total int64
	for i, f := range p.Files {
		v := f.Version
		selections[i] = v.ExportSelection
		if !v.Identity.BirthKnown || v.Identity.Owner != p.Source.Identity.Owner || v.Size < 0 || v.Size > MaxExportBytes-total || v.OutputIdentity != nil || !snapshotDigest(v.SHA256) || len(f.Chunks) != int((v.Size+SnapshotChunkBytes-1)/SnapshotChunkBytes) || f.Chunks == nil {
			return fmt.Errorf("snapshot source file/hash/chunk evidence differs")
		}
		total += v.Size
		for _, chunk := range f.Chunks {
			if !snapshotDigest(chunk) {
				return fmt.Errorf("invalid canonical chunk hash")
			}
		}
	}
	normal, err := NormalizeExportSelections(selections)
	if err != nil || total != p.Bytes {
		return fmt.Errorf("invalid snapshot selections/bytes")
	}
	for i := range normal {
		if normal[i] != selections[i] {
			return fmt.Errorf("snapshot selections must be canonical and sorted")
		}
	}
	want := p.SHA256
	p.SHA256 = ""
	got, err := snapshotHash(p)
	if err != nil || !snapshotDigest(want) || want != got {
		return fmt.Errorf("snapshot plan commitment differs")
	}
	return nil
}

// snapshotRetain owns a duplicate provider FD, rather than borrowing its raw
// integer across a concurrent Provider.Close/reuse. It never adopts a new root.
func (p *Provider) snapshotRetain(owners *snapshotFDSet) (*Provider, error) {
	if p == nil || p.root == nil {
		return nil, fmt.Errorf("managed provider unavailable")
	}
	file, err := owners.duplicate(p.root, "provider")
	if err != nil {
		return nil, err
	}
	copy := &Provider{path: p.path, root: file, identity: p.identity, mount: p.mount}
	if err = copy.validateReadOnlyNamed(); err != nil {
		return nil, errors.Join(err, owners.close(file))
	}
	id, err := durableIdentity(int(file.Fd()), "")
	if err != nil || id != p.identity {
		return nil, errors.Join(fmt.Errorf("retained snapshot provider identity changed"), err, owners.close(file))
	}
	return copy, nil
}

// PlanSnapshot applies the explicit policy's bounded native/content checks; it
// creates no destination. CopySnapshot checks observed consistency again.
// The Manager must freshly check source access and authority immediately before
// the publication commit attempt; a delayed commit can finish after a change.
func (p *Provider) PlanSnapshot(ctx context.Context, a model.Artifact, selections []model.ExportSelection, maxBytes int64, policy SnapshotPolicy, current func(context.Context) error) (plan SnapshotPlan, resultErr error) {
	if err := policy.validate(); err != nil {
		return plan, err
	}
	owners := newSnapshotFDSet()
	defer func() {
		resultErr = errors.Join(resultErr, owners.closeAll())
		plan.Native = owners.receipt()
		if resultErr == nil {
			plan.SHA256 = ""
			plan.SHA256, resultErr = snapshotHash(plan)
			if resultErr == nil {
				resultErr = plan.validate()
			}
		}
	}()
	encoded, err := json.Marshal(a)
	if err != nil {
		return plan, err
	}
	var detachedArtifact model.Artifact
	if err = json.Unmarshal(encoded, &detachedArtifact); err != nil {
		return plan, err
	}
	a = detachedArtifact
	if current == nil {
		return plan, fmt.Errorf("snapshot current-authority check required")
	}
	if err := snapshotCurrent(ctx, current); err != nil {
		return plan, err
	}
	provider, err := p.snapshotRetain(owners)
	if err != nil {
		return plan, err
	}
	root, err := provider.snapshotOpenArtifact(ctx, a, owners, "source-root")
	if err != nil {
		return plan, err
	}
	selections, err = NormalizeExportSelections(selections)
	if err != nil {
		return plan, err
	}
	tree, err := provider.snapshotInventory(ctx, root, a, nil, policy, owners)
	if err != nil {
		return plan, err
	}
	plan = SnapshotPlan{Schema: "arm.storage.snapshot-plan/v2", Policy: policy, Source: a, SourceTree: tree, Files: []SnapshotFile{}}
	buffer := make([]byte, SnapshotChunkBytes)
	for _, selected := range selections {
		if err = snapshotCurrent(ctx, current); err != nil {
			return plan, err
		}
		file, e := provider.snapshotReadFile(ctx, root, selected, nil, buffer, min(maxBytes, int64(MaxExportBytes))-plan.Bytes, owners)
		if e != nil {
			return plan, e
		}
		if file.Version.Size > min(maxBytes, int64(MaxExportBytes))-plan.Bytes {
			return plan, fmt.Errorf("snapshot exceeds admitted full-copy budget")
		}
		plan.Bytes += file.Version.Size
		plan.Files = append(plan.Files, file)
	}
	if err = provider.snapshotSourceMatches(ctx, root, plan, current, buffer, owners); err != nil {
		return plan, err
	}
	return plan, nil
}

// CopySnapshot creates an independent allocation, copies real bytes and returns
// closed prepared evidence only. No existing allocation/path is adopted, no
// remote service is exposed, and this function cannot publish a generation.
func (p *Provider) CopySnapshot(ctx context.Context, intent SnapshotCopyIntent, callbacks SnapshotCallbacks) (result SnapshotCopyResult, resultErr error) {
	return p.copySnapshot(ctx, intent, callbacks, newSnapshotFDSet())
}

// The fresh per-call owner is private. Tests may report a failure AFTER a real
// native Sync/Close; no environment, request or public API can select controls.
func (p *Provider) copySnapshot(ctx context.Context, intent SnapshotCopyIntent, callbacks SnapshotCallbacks, owners *snapshotFDSet) (result SnapshotCopyResult, resultErr error) {
	result.Phase = "not-started"
	if owners == nil || len(owners.records) != 0 || len(owners.files) != 0 || owners.owned != 0 || owners.closeFile == nil || owners.syncFile == nil {
		return result, fmt.Errorf("fresh original snapshot FD owner required")
	}
	// Freeze caller-owned slices/pointers before any callback may run.
	raw, err := json.Marshal(intent)
	if err != nil || len(raw) > snapshotManifestLimit {
		return result, fmt.Errorf("bounded snapshot intent required")
	}
	var detachedIntent SnapshotCopyIntent
	if err = json.Unmarshal(raw, &detachedIntent); err != nil {
		return result, err
	}
	intent = detachedIntent
	if err = intent.Plan.validate(); err != nil {
		return result, err
	}
	if callbacks.Intent == nil || callbacks.Owned == nil || callbacks.Prepared == nil || callbacks.Current == nil || !validName.MatchString(intent.Destination) || intent.Destination == intent.Plan.Source.Name || len(intent.Key) < 1 || len(intent.Key) > 128 || intent.DiskPromise < intent.Plan.Bytes+snapshotMetadataBytes || intent.CopyResources.CPU <= 0 || intent.CopyResources.Memory < SnapshotCopyMemoryFloor || intent.CopyResources.Disk != 0 || intent.CopyFDs != 8 {
		return result, fmt.Errorf("new allocation/full-copy admission and all durable checkpoints required")
	}
	if p == nil {
		return result, fmt.Errorf("managed provider unavailable")
	}
	p.mu.Lock()
	if p.snapshotSlots == nil {
		p.snapshotSlots = make(chan struct{}, 2)
	}
	slots := p.snapshotSlots
	p.mu.Unlock()
	select {
	case slots <- struct{}{}:
	default:
		return result, fmt.Errorf("both owned snapshot copy slots are occupied")
	}
	// This defer is registered before descriptor cleanup and runs afterward.
	// Failed original closes retain the slot; a timeout never manufactures join.
	defer func() {
		if !result.UnknownClose && !errors.Is(resultErr, errSnapshotClose) {
			<-slots
		}
	}()
	if err = snapshotCurrent(ctx, callbacks.Current); err != nil {
		return result, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, owners.closeAll())
		result.Native = owners.receipt()
		result.FilesClosed = result.Native.Closed
		result.UnknownClose = result.Native.Unknown
	}()
	provider, err := p.snapshotRetain(owners)
	if err != nil {
		return result, err
	}
	var root, destination *os.File
	root, err = provider.snapshotOpenArtifact(ctx, intent.Plan.Source, owners, "source-root")
	if err != nil {
		return result, err
	}
	var callbackIntent SnapshotCopyIntent
	if err = json.Unmarshal(raw, &callbackIntent); err != nil {
		return result, err
	}
	if err = callbacks.Intent(ctx, callbackIntent); err != nil {
		result.Phase = "intent-unknown"
		return result, err
	}
	result.Phase = "intent-committed"
	buffer := make([]byte, SnapshotChunkBytes)
	if err = provider.snapshotSourceMatches(ctx, root, intent.Plan, callbacks.Current, buffer, owners); err != nil {
		return result, err
	}
	if err = snapshotCurrent(ctx, callbacks.Current); err != nil {
		return result, err
	}
	available, err := availableExportBytes(int(provider.root.Fd()))
	if err != nil || intent.DiskPromise > available {
		return result, fmt.Errorf("insufficient current shared filesystem availability")
	}
	allocation, err := provider.snapshotAllocate(ctx, intent.Destination, owners)
	result.Artifact = allocation
	if err != nil {
		result.Phase = "allocation-unknown"
		return result, err
	}
	artifact := *allocation
	result.Artifact = &artifact
	result.Phase = "allocation-owned"
	callbackIntent = SnapshotCopyIntent{}
	if err = json.Unmarshal(raw, &callbackIntent); err != nil {
		return result, err
	}
	callbackArtifact := artifact
	callbackIdentity := *artifact.Identity
	callbackArtifact.Identity = &callbackIdentity
	if err = callbacks.Owned(ctx, callbackIntent, callbackArtifact); err != nil {
		return result, err
	}
	destination, err = provider.snapshotOpenArtifact(ctx, artifact, owners, "destination-root")
	if err != nil {
		return result, err
	}
	result.Phase = "copying"
	generation := SnapshotGeneration{Schema: "arm.storage.snapshot-generation/v2", Policy: intent.Plan.Policy, Key: intent.Key, Generation: intent.Destination, Artifact: artifact, SourceSHA: intent.Plan.SHA256, Files: []SnapshotCopiedFile{}, Bytes: intent.Plan.Bytes, Method: "ordinary-independent-copy", Sealing: "portable-private-copy-unsealed"}
	for _, expected := range intent.Plan.Files {
		if err = snapshotCurrent(ctx, callbacks.Current); err != nil {
			return result, err
		}
		output, e := provider.snapshotCopyFile(ctx, root, destination, artifact, expected, callbacks.Current, buffer, owners)
		if e != nil {
			result.UnknownClose = errors.Is(e, errSnapshotClose)
			return result, e
		}
		generation.Files = append(generation.Files, SnapshotCopiedFile{Source: expected, Output: output})
	}
	if err = provider.snapshotSourceMatches(ctx, root, intent.Plan, callbacks.Current, buffer, owners); err != nil {
		return result, err
	}
	if err = owners.sync(destination); err != nil {
		return result, err
	}
	if err = provider.snapshotVerifyGeneration(ctx, destination, generation, callbacks.Current, buffer, owners); err != nil {
		return result, err
	}
	named, err := provider.snapshotOpenArtifact(ctx, artifact, owners, "destination-root")
	if err != nil {
		return result, err
	}
	if err = owners.close(named); err != nil {
		result.UnknownClose = true
		return result, errors.Join(errSnapshotClose, err)
	}
	generation.SHA256, err = snapshotHash(generation)
	if err != nil {
		return result, err
	}
	// Prepared is after exact original descriptor closure, including source and
	// provider duplicate. The dependency pin itself remains Manager-owned.
	closeErr := owners.closeAll()
	if closeErr != nil {
		result.UnknownClose = true
		return result, errors.Join(errSnapshotClose, closeErr)
	}
	result.Native = owners.receipt()
	result.FilesClosed = result.Native.Closed
	result.Generation = generation
	result.Phase = "prepared"
	if err = snapshotCurrent(ctx, callbacks.Current); err != nil {
		result.Phase = "prepared-fenced"
		return result, err
	}
	callbackIntent = SnapshotCopyIntent{}
	if err = json.Unmarshal(raw, &callbackIntent); err != nil {
		return result, err
	}
	preparedRaw, err := json.Marshal(result)
	if err != nil || len(preparedRaw) > snapshotManifestLimit {
		result.Phase = "prepared-fenced"
		return result, fmt.Errorf("bounded closed prepared checkpoint required")
	}
	var callbackResult SnapshotCopyResult
	if err = json.Unmarshal(preparedRaw, &callbackResult); err != nil {
		return result, err
	}
	if err = callbacks.Prepared(ctx, callbackIntent, callbackResult); err != nil {
		result.Phase = "prepared-unknown"
		return result, err
	}
	return result, nil
}

var errSnapshotClose = errors.New("original snapshot descriptor close unresolved")

func (p *Provider) snapshotCopyFile(ctx context.Context, root, destination *os.File, artifact model.Artifact, expected SnapshotFile, current func(context.Context) error, buffer []byte, owners *snapshotFDSet) (output model.ExportFile, resultErr error) {
	file, err := owners.openFile(root, expected.Version.Source, "source-file")
	if err != nil {
		return output, err
	}
	defer func() {
		if e := owners.close(file); e != nil {
			resultErr = errors.Join(resultErr, errSnapshotClose, e)
		}
	}()
	before, err := exportFileVersion(file, expected.Version.ExportSelection, p.identity.Owner, p.mount)
	if err != nil || !sameExportVersion(before, expected.Version) {
		return output, fmt.Errorf("source changed before snapshot copy")
	}
	out, err := owners.createOutput(ctx, p, destination, artifact, expected.Version.Path)
	if err != nil {
		return output, err
	}
	defer func() {
		if e := owners.close(out); e != nil {
			resultErr = errors.Join(resultErr, errSnapshotClose, e)
		}
	}()
	hash := sha256.New()
	for offset, index := int64(0), 0; offset < before.Size; index++ {
		if err = snapshotCurrent(ctx, current); err != nil {
			return output, err
		}
		n := int(min(int64(SnapshotChunkBytes), before.Size-offset))
		if _, err = io.ReadFull(file, buffer[:n]); err != nil {
			return output, err
		}
		h := sha256.Sum256(buffer[:n])
		if hex.EncodeToString(h[:]) != expected.Chunks[index] {
			return output, fmt.Errorf("source content differs from reviewed canonical chunk")
		}
		if err = snapshotCurrent(ctx, current); err != nil {
			return output, err
		}
		if err = p.snapshotCheckOutputFile(ctx, destination, artifact, expected.Version.Path, out, offset, owners); err != nil {
			return output, err
		}
		written, e := out.Write(buffer[:n])
		if e != nil {
			return output, e
		}
		if written != n {
			return output, io.ErrShortWrite
		}
		hash.Write(buffer[:n])
		offset += int64(n)
	}
	after, err := exportFileVersion(file, expected.Version.ExportSelection, p.identity.Owner, p.mount)
	if err != nil || !sameExportVersion(before, after) || hex.EncodeToString(hash.Sum(nil)) != expected.Version.SHA256 {
		return output, fmt.Errorf("source changed during snapshot copy")
	}
	if err = out.Chmod(0400); err != nil {
		return output, err
	}
	if err = owners.sync(out); err != nil {
		return output, err
	}
	output, err = exportFileVersion(out, expected.Version.ExportSelection, p.identity.Owner, p.mount)
	if err != nil {
		return output, err
	}
	if output.Identity == expected.Version.Identity || output.Links != 1 {
		return output, fmt.Errorf("snapshot did not create independent regular file")
	}
	output.SHA256 = expected.Version.SHA256
	return output, nil
}

func (p *Provider) snapshotReadFile(ctx context.Context, root *os.File, selected model.ExportSelection, expected *SnapshotFile, buffer []byte, limit int64, owners *snapshotFDSet) (result SnapshotFile, resultErr error) {
	file, err := owners.openFile(root, selected.Source, "verified-file")
	if err != nil {
		return result, err
	}
	defer func() {
		if e := owners.close(file); e != nil {
			resultErr = errors.Join(resultErr, errSnapshotClose, e)
		}
	}()
	before, err := exportFileVersion(file, selected, p.identity.Owner, p.mount)
	if err != nil {
		return result, err
	}
	if before.Size > limit {
		return result, fmt.Errorf("snapshot source exceeds admitted read/copy bound")
	}
	result = SnapshotFile{Version: before, Chunks: []string{}}
	hash := sha256.New()
	for offset := int64(0); offset < before.Size; {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		n := int(min(int64(SnapshotChunkBytes), before.Size-offset))
		if _, err = io.ReadFull(file, buffer[:n]); err != nil {
			return result, err
		}
		hash.Write(buffer[:n])
		h := sha256.Sum256(buffer[:n])
		result.Chunks = append(result.Chunks, hex.EncodeToString(h[:]))
		offset += int64(n)
	}
	after, err := exportFileVersion(file, selected, p.identity.Owner, p.mount)
	if err != nil || !sameExportVersion(before, after) {
		return result, fmt.Errorf("snapshot source changed during complete hashing")
	}
	result.Version.SHA256 = hex.EncodeToString(hash.Sum(nil))
	if expected != nil {
		a, _ := json.Marshal(result)
		b, _ := json.Marshal(expected)
		if string(a) != string(b) {
			return result, fmt.Errorf("snapshot full source content/version differs")
		}
	}
	return result, nil
}

func (p *Provider) snapshotSourceMatches(ctx context.Context, root *os.File, plan SnapshotPlan, current func(context.Context) error, buffer []byte, owners *snapshotFDSet) error {
	if err := snapshotCurrent(ctx, current); err != nil {
		return err
	}
	tree, err := p.snapshotInventory(ctx, root, plan.Source, nil, plan.Policy, owners)
	if err != nil || tree != plan.SourceTree {
		return errors.Join(fmt.Errorf("snapshot original source inventory changed"), err)
	}
	for _, expected := range plan.Files {
		if _, err = p.snapshotReadFile(ctx, root, expected.Version.ExportSelection, &expected, buffer, expected.Version.Size, owners); err != nil {
			return err
		}
	}
	check, err := p.snapshotOpenArtifact(ctx, plan.Source, owners, "source-final-check")
	if err != nil {
		return err
	}
	// A retained original FD alone does not prove the current named path/access.
	after, err := p.snapshotInventory(ctx, check, plan.Source, nil, plan.Policy, owners)
	closeErr := owners.close(check)
	if err != nil || closeErr != nil || after != tree {
		return errors.Join(fmt.Errorf("snapshot original named source changed"), err, closeErr)
	}
	return snapshotCurrent(ctx, current)
}

// Both policies use the same bounded, confined native inventory. Only explicit
// idle-native additionally surveys process activity. This is a nondestructive
// copy primitive; ordinary cleanup and reclamation keep their activity checks.
func (p *Provider) snapshotInventory(ctx context.Context, root *os.File, artifact model.Artifact, expected map[string]bool, policy SnapshotPolicy, owners *snapshotFDSet) (string, error) {
	if err := policy.validate(); err != nil {
		return "", err
	}
	type entry struct {
		Path      string
		Version   model.ExportFile
		Directory bool
	}
	queue := []string{""}
	entries := []entry{}
	identities := map[model.FileIdentity]bool{}
	links := map[model.FileIdentity]uint64{}
	bytes, queuedBytes := 0, 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		path := queue[0]
		queue = queue[1:]
		queuedBytes -= len(path)
		var file *os.File
		var err error
		if path == "" {
			file, err = owners.acquire("inventory-root", func() (int, error) {
				return unix.Openat(int(root.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			})
		} else {
			file, err = owners.openFile(root, path, "inventory-entry")
		}
		if err != nil {
			return "", err
		}
		var st unix.Stat_t
		err = unix.Fstat(int(file.Fd()), &st)
		if err != nil {
			return "", errors.Join(err, owners.close(file))
		}
		mount, err := mountIdentity(int(file.Fd()))
		id, idErr := durableIdentity(int(file.Fd()), "")
		if err != nil || idErr != nil || mount != p.mount || id.Owner != p.identity.Owner || st.Mode&07022 != 0 || (st.Mode&unix.S_IFMT != unix.S_IFREG && st.Mode&unix.S_IFMT != unix.S_IFDIR) {
			return "", errors.Join(fmt.Errorf("snapshot source inventory ownership/type/mount differs"), err, idErr, owners.close(file))
		}
		identities[Identity(&st)] = true
		v := model.ExportFile{ExportSelection: model.ExportSelection{Source: path, Path: path}, Identity: id, Size: st.Size, Mode: uint32(st.Mode) & 0777, Links: uint64(st.Nlink), ModifiedSecond: st.Mtim.Sec, ModifiedNanos: st.Mtim.Nsec, ChangedSecond: st.Ctim.Sec, ChangedNanos: st.Ctim.Nsec}
		directory := st.Mode&unix.S_IFMT == unix.S_IFDIR
		if expected != nil {
			wantDirectory, exists := expected[path]
			if !exists || wantDirectory != directory || (directory && st.Mode&0077 != 0) {
				return "", errors.Join(fmt.Errorf("snapshot destination has an unexpected/unprotected entry"), owners.close(file))
			}
		}
		if !directory {
			links[id]++
		}
		entries = append(entries, entry{path, v, directory})
		bytes += len(path) + 256
		if len(entries)+len(queue) > snapshotInventoryLimit || bytes+queuedBytes > snapshotManifestLimit {
			return "", errors.Join(fmt.Errorf("snapshot source inventory exceeds admitted bound"), owners.close(file))
		}
		if directory {
			for {
				if err = ctx.Err(); err != nil {
					return "", errors.Join(err, owners.close(file))
				}
				children, readErr := file.ReadDir(64)
				for _, child := range children {
					next := filepath.Join(path, child.Name())
					if _, e := exportRelative(next); e != nil || len(queue)+len(entries) >= snapshotInventoryLimit {
						return "", errors.Join(fmt.Errorf("snapshot source inventory path/entry bound"), e, owners.close(file))
					}
					queuedBytes += len(next)
					if bytes+queuedBytes > snapshotManifestLimit {
						return "", errors.Join(fmt.Errorf("snapshot source inventory memory bound"), owners.close(file))
					}
					queue = append(queue, next)
				}
				if errors.Is(readErr, io.EOF) {
					break
				}
				if readErr != nil {
					return "", errors.Join(readErr, owners.close(file))
				}
			}
		}
		if err = owners.close(file); err != nil {
			return "", err
		}
	}
	if artifact.Identity == nil || entries[0].Version.Identity != *artifact.Identity {
		return "", fmt.Errorf("snapshot source root identity changed")
	}
	if expected != nil && len(entries) != len(expected) {
		return "", fmt.Errorf("snapshot complete destination namespace differs")
	}
	for _, entry := range entries {
		if !entry.Directory && links[entry.Version.Identity] != entry.Version.Links {
			return "", fmt.Errorf("snapshot source has external/changing hardlinks")
		}
	}
	if policy == SnapshotIdleNative {
		active, err := activeUsers(ctx, p.Path(artifact.Name), identities)
		if err != nil || len(active) != 0 {
			return "", errors.Join(fmt.Errorf("snapshot source must be verifiably idle: %v", active), err)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return snapshotHash(entries)
}

func (p *Provider) snapshotVerifyGeneration(ctx context.Context, root *os.File, generation SnapshotGeneration, current func(context.Context) error, buffer []byte, owners *snapshotFDSet) error {
	// Verify complete payload and independent identities, plus exact selected
	// namespace through the bounded breadth-first native inventory. Snapshot output
	// deliberately strips execute/write bits, rather than preserving source mode.
	namespace := map[string]bool{"": true}
	for _, copied := range generation.Files {
		if err := snapshotCurrent(ctx, current); err != nil {
			return err
		}
		expected := copied.Source.Version
		namespace[expected.Path] = false
		for dir := filepath.Dir(expected.Path); dir != "."; dir = filepath.Dir(dir) {
			namespace[dir] = true
		}
		selected := model.ExportSelection{Source: expected.Path, Path: expected.Path}
		actual, err := p.snapshotReadFile(ctx, root, selected, nil, buffer, copied.Output.Size, owners)
		if err != nil {
			return err
		}
		if actual.Version.Identity != copied.Output.Identity || actual.Version.SHA256 != expected.SHA256 || !sameExportVersion(actual.Version, model.ExportFile{ExportSelection: selected, Identity: copied.Output.Identity, Size: copied.Output.Size, Mode: copied.Output.Mode, Links: copied.Output.Links, ModifiedSecond: copied.Output.ModifiedSecond, ModifiedNanos: copied.Output.ModifiedNanos, ChangedSecond: copied.Output.ChangedSecond, ChangedNanos: copied.Output.ChangedNanos}) || strings.Join(actual.Chunks, ",") != strings.Join(copied.Source.Chunks, ",") {
			return fmt.Errorf("snapshot destination differs from complete canonical content/native evidence")
		}
	}
	if err := verifyExportRoot(root, p.identity.Owner); err != nil {
		return err
	}
	// This branch is reached only for the new exclusive allocation made by this
	// CopySnapshot call. It is not an exception for adopted/published storage.
	if _, err := p.snapshotInventory(ctx, root, generation.Artifact, namespace, generation.Policy, owners); err != nil {
		return err
	}
	return snapshotCurrent(ctx, current)
}
