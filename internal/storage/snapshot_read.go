package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/yrashk/arm/internal/model"
)

func (g SnapshotGeneration) validate() error {
	if err := g.Policy.validate(); err != nil {
		return err
	}
	if g.Schema != "arm.storage.snapshot-generation/v2" || !validName.MatchString(g.Generation) || g.Artifact.Name != g.Generation || g.Artifact.Identity == nil || !g.Artifact.Identity.BirthKnown || g.Artifact.Phase != "owned" || len(g.Key) < 1 || len(g.Key) > 128 || !snapshotDigest(g.SourceSHA) || g.Method != "ordinary-independent-copy" || g.Sealing != "portable-private-copy-unsealed" || len(g.Files) < 1 || len(g.Files) > MaxExportFiles || g.Bytes < 0 || g.Bytes > MaxExportBytes {
		return fmt.Errorf("invalid complete snapshot generation")
	}
	selections := make([]model.ExportSelection, len(g.Files))
	var bytes int64
	for i, f := range g.Files {
		v, output := f.Source.Version, f.Output
		selections[i] = v.ExportSelection
		if output.ExportSelection != v.ExportSelection || !v.Identity.BirthKnown || !output.Identity.BirthKnown || v.Identity == output.Identity || output.Identity.Owner != g.Artifact.Identity.Owner || output.Mode != 0400 || output.Links != 1 || output.Size != v.Size || output.SHA256 != v.SHA256 || !snapshotDigest(v.SHA256) || v.Size < 0 || v.Size > MaxExportBytes-bytes || len(f.Source.Chunks) != int((v.Size+SnapshotChunkBytes-1)/SnapshotChunkBytes) || f.Source.Chunks == nil || output.OutputIdentity != nil {
			return fmt.Errorf("snapshot independent file/hash/native commitment differs")
		}
		bytes += v.Size
		for _, chunk := range f.Source.Chunks {
			if !snapshotDigest(chunk) {
				return fmt.Errorf("invalid snapshot canonical chunk hash")
			}
		}
	}
	normal, err := NormalizeExportSelections(selections)
	if err != nil || bytes != g.Bytes {
		return fmt.Errorf("invalid snapshot generation file selection/bytes")
	}
	for i := range normal {
		if normal[i] != selections[i] {
			return fmt.Errorf("snapshot generation file order differs")
		}
	}
	want := g.SHA256
	g.SHA256 = ""
	got, err := snapshotHash(g)
	if err != nil || !snapshotDigest(want) || got != want {
		return fmt.Errorf("snapshot generation digest differs")
	}
	return nil
}

// ReadSnapshotChunk is an S-only object primitive, not a public read endpoint.
// It checks current generation authority before and after a fresh independent
// destination open and whole canonical chunk verification. It never reopens or
// checks the original source: source decoupling begins only after Manager's
// separate exact publication commit. Internal prepared evidence alone grants no
// call permission; the required callback must enforce that committed admission.
// A transport/adapter must additionally fence its final owned response handoff.
// Returned memory belongs exclusively to the caller; no mutable borrowed cache
// buffer can acquire verification authority. F/prefetch is not implemented.
type SnapshotReadResult struct {
	Data   []byte                `json:"-"`
	Native SnapshotNativeReceipt `json:"native_read"`
}

func (p *Provider) ReadSnapshotChunk(ctx context.Context, generation SnapshotGeneration, path string, chunk int, current func(context.Context) error) (result SnapshotReadResult, resultErr error) {
	raw, err := json.Marshal(generation)
	if err != nil || len(raw) > snapshotManifestLimit {
		return result, fmt.Errorf("bounded snapshot generation required")
	}
	var detachedGeneration SnapshotGeneration
	if err = json.Unmarshal(raw, &detachedGeneration); err != nil {
		return result, err
	}
	generation = detachedGeneration
	if err := generation.validate(); err != nil {
		return result, err
	}
	if current == nil || chunk < 0 {
		return result, fmt.Errorf("current published-generation authority and exact chunk required")
	}
	if err := snapshotCurrent(ctx, current); err != nil {
		return result, err
	}
	var selected *SnapshotCopiedFile
	for i := range generation.Files {
		if generation.Files[i].Output.Path == path {
			selected = &generation.Files[i]
			break
		}
	}
	if selected == nil || chunk >= len(selected.Source.Chunks) {
		return result, fmt.Errorf("snapshot path/canonical chunk is not in this generation")
	}
	owners := newSnapshotFDSet()
	defer func() {
		resultErr = errors.Join(resultErr, owners.closeAll())
		result.Native = owners.receipt()
		if resultErr != nil || !result.Native.Closed {
			result.Data = nil
		}
	}()
	provider, err := p.snapshotRetain(owners)
	if err != nil {
		return result, err
	}
	root, err := provider.snapshotOpenArtifact(ctx, generation.Artifact, owners, "generation-root")
	if err != nil {
		return result, err
	}
	file, err := owners.openFile(root, path, "generation-file")
	if err != nil {
		return result, err
	}
	selection := model.ExportSelection{Source: path, Path: path}
	before, err := exportFileVersion(file, selection, provider.identity.Owner, provider.mount)
	expected := selected.Output
	expected.ExportSelection = selection
	if err != nil || !sameExportVersion(before, expected) {
		return result, fmt.Errorf("snapshot backing identity/version changed")
	}
	offset := int64(chunk) * SnapshotChunkBytes
	data := make([]byte, int(min(int64(SnapshotChunkBytes), before.Size-offset)))
	if _, err = io.ReadFull(io.NewSectionReader(file, offset, int64(len(data))), data); err != nil {
		return result, err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != selected.Source.Chunks[chunk] {
		return result, fmt.Errorf("snapshot complete canonical chunk digest differs")
	}
	after, err := exportFileVersion(file, selection, provider.identity.Owner, provider.mount)
	if err != nil || !sameExportVersion(before, after) {
		return result, fmt.Errorf("snapshot backing changed during verified read")
	}
	// Recheck the current managed namespace after the read; a held descriptor
	// alone cannot authorize a substituted/quarantined destination allocation.
	check, err := provider.snapshotOpenArtifact(ctx, generation.Artifact, owners, "generation-final-check")
	if err != nil {
		return result, err
	}
	if err = owners.close(check); err != nil {
		return result, errors.Join(errSnapshotClose, err)
	}
	if err = snapshotCurrent(ctx, current); err != nil {
		return result, err
	}
	result.Data = data
	return result, nil
}
