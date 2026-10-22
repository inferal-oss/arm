package cargoprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const MaxMetadataBytes = 8 << 20

// Metadata contains only paths consumed for preparation. It confers neither
// owned-storage status nor permission to erase any Cargo-reported path.
type Metadata struct {
	WorkspaceRoot   string `json:"workspace_root"`
	TargetDirectory string `json:"target_directory"`
	BuildDirectory  string `json:"build_directory"`
}

// DecodeMetadata accepts future unknown fields but refuses duplicate JSON keys,
// missing/null/wrong-typed consumed fields, trailing values and path mismatch.
func DecodeMetadata(raw []byte, roots Roots) (Metadata, error) {
	var out Metadata
	if len(raw) == 0 || len(raw) > MaxMetadataBytes || !utf8.Valid(raw) {
		return out, fmt.Errorf("Cargo metadata exceeds byte bounds")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := uniqueMetadata(d, 0); e != nil {
		return out, fmt.Errorf("Cargo metadata must be one bounded unambiguous JSON value")
	}
	if _, e := d.Token(); e != io.EOF {
		return out, fmt.Errorf("Cargo metadata has trailing data")
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil || fields == nil {
		return out, fmt.Errorf("Cargo metadata must be an object")
	}
	values := []struct {
		key   string
		value *string
	}{{"workspace_root", &out.WorkspaceRoot}, {"target_directory", &out.TargetDirectory}, {"build_directory", &out.BuildDirectory}}
	for key := range fields {
		for _, item := range values {
			if key != item.key && strings.EqualFold(key, item.key) {
				return Metadata{}, fmt.Errorf("Cargo metadata uses an ambiguous consumed field alias")
			}
		}
	}
	for _, item := range values {
		value, ok := fields[item.key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, item.value) != nil || len(*item.value) > 4096 || !filepath.IsAbs(*item.value) || filepath.Clean(*item.value) != *item.value || !validText(*item.value) {
			return Metadata{}, fmt.Errorf("Cargo metadata lacks a canonical supported %s", item.key)
		}
	}
	if out.TargetDirectory != roots.Target || out.BuildDirectory != roots.Build {
		return Metadata{}, fmt.Errorf("Cargo reported output paths differ from authoritative managed plan")
	}
	return out, nil
}
func uniqueMetadata(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("nesting limit")
	}
	token, e := d.Token()
	if e != nil {
		return e
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return e
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate key")
			}
			seen[name] = true
			if e = uniqueMetadata(d, depth+1); e != nil {
				return e
			}
		}
		closing, e := d.Token()
		if e != nil || closing != json.Delim('}') {
			return fmt.Errorf("object closure")
		}
	case '[':
		for d.More() {
			if e := uniqueMetadata(d, depth+1); e != nil {
				return e
			}
		}
		closing, e := d.Token()
		if e != nil || closing != json.Delim(']') {
			return fmt.Errorf("array closure")
		}
	default:
		return fmt.Errorf("unexpected delimiter")
	}
	return nil
}
