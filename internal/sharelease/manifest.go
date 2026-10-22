package sharelease

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/yrashk/arm/internal/sharefs"
)

const ManifestPartBytes = 256 << 10
const MaxManifestParts = sharefs.MaxManifestBytes / ManifestPartBytes
const MaxManifestEncodedPartBytes = (ManifestPartBytes + 2) / 3 * 4

type ManifestPart struct {
	Share, Generation string
	Index             int
	Data              string
}

func (s ManifestSummary) Validate() error {
	if !digest(s.SHA256) || s.RawBytes < 1 || s.RawBytes > sharefs.MaxManifestBytes || s.Parts != (s.RawBytes+ManifestPartBytes-1)/ManifestPartBytes || s.StoredBytes != base64Size(s.RawBytes, s.Parts) || s.Entries < 1 || s.Entries > sharefs.MaxEntries || s.Chunks < 0 || s.Chunks > sharefs.MaxChunks || s.ContentBytes < 0 || s.ContentBytes > int64(s.Chunks)*sharefs.ChunkBytes {
		return fmt.Errorf("invalid complete manifest summary")
	}
	return nil
}
func base64Size(raw, parts int) int {
	if parts < 1 {
		return 0
	}
	return (parts-1)*MaxManifestEncodedPartBytes + ((raw-(parts-1)*ManifestPartBytes+2)/3)*4
}
func EncodeManifest(m sharefs.Manifest) ([]ManifestPart, ManifestSummary, error) {
	if err := m.Validate(sharefs.DefaultLimits()); err != nil {
		return nil, ManifestSummary{}, err
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, ManifestSummary{}, err
	}
	if len(raw) > sharefs.MaxManifestBytes {
		return nil, ManifestSummary{}, fmt.Errorf("complete share manifest exceeds bound")
	}
	parts := []ManifestPart{}
	summary := ManifestSummary{SHA256: m.SHA256, RawBytes: len(raw), Entries: len(m.Entries)}
	for _, e := range m.Entries {
		summary.Chunks += len(e.Chunks)
		if !e.Directory {
			summary.ContentBytes += e.Version.Size
		}
	}
	for off := 0; off < len(raw); off += ManifestPartBytes {
		part := ManifestPart{m.Binding.Share, m.Binding.Generation, len(parts), base64.StdEncoding.EncodeToString(raw[off:min(off+ManifestPartBytes, len(raw))])}
		parts = append(parts, part)
		summary.StoredBytes += len(part.Data)
	}
	summary.Parts = len(parts)
	if err = summary.Validate(); err != nil {
		return nil, ManifestSummary{}, err
	}
	return parts, summary, nil
}
func DecodeManifest(parts []ManifestPart, s ManifestSummary) (sharefs.Manifest, error) {
	var m sharefs.Manifest
	if err := s.Validate(); err != nil {
		return m, err
	}
	if len(parts) != s.Parts || len(parts) == 0 || len(parts) > MaxManifestParts {
		return m, fmt.Errorf("complete manifest part roster required")
	}
	raw := make([]byte, 0, s.RawBytes)
	stored := 0
	for i, p := range parts {
		if p.Index != i || !id(p.Share) || !id(p.Generation) || p.Share != parts[0].Share || p.Generation != parts[0].Generation || len(p.Data) > MaxManifestEncodedPartBytes {
			return m, fmt.Errorf("manifest part identity, order or bound differs")
		}
		b, err := base64.StdEncoding.Strict().DecodeString(p.Data)
		if err != nil || base64.StdEncoding.EncodeToString(b) != p.Data || i < len(parts)-1 && len(b) != ManifestPartBytes || len(b) < 1 || len(b) > s.RawBytes-len(raw) {
			return m, fmt.Errorf("manifest part is not exact canonical bounded data")
		}
		raw = append(raw, b...)
		stored += len(p.Data)
	}
	if len(raw) != s.RawBytes || stored != s.StoredBytes {
		return m, fmt.Errorf("manifest part byte accounting differs")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	canonical, err := json.Marshal(m)
	if err != nil || !bytes.Equal(canonical, raw) {
		return sharefs.Manifest{}, fmt.Errorf("manifest is not exact canonical producer evidence")
	}
	if err = m.Validate(sharefs.DefaultLimits()); err != nil {
		return sharefs.Manifest{}, err
	}
	if m.SHA256 != s.SHA256 || m.Binding.Share != parts[0].Share || m.Binding.Generation != parts[0].Generation || len(m.Entries) != s.Entries {
		return sharefs.Manifest{}, fmt.Errorf("manifest commitment differs")
	}
	_, summary, err := EncodeManifest(m)
	if err != nil || summary != s {
		return sharefs.Manifest{}, fmt.Errorf("manifest summary differs")
	}
	return m, nil
}
