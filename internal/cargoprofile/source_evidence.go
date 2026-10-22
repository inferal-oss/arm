package cargoprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const MaxSourceManifests = 256
const MaxSourceBytes = 8 << 20

// SourceEvidence observes manifests, lock presence and workspace identity. It
// neither snapshots all source files nor conveys cleanup authority over them.
type SourceEvidence struct {
	Workspace         string            `json:"workspace"`
	WorkspaceIdentity DirectoryEvidence `json:"workspace_identity"`
	Members           []string          `json:"member_manifests"`
	Files             []FileEvidence    `json:"files"`
}

func sourceField(fields map[string]json.RawMessage, key string) (json.RawMessage, error) {
	for name := range fields {
		if name != key && strings.EqualFold(name, key) {
			return nil, fmt.Errorf("Cargo source metadata has ambiguous consumed field alias")
		}
	}
	raw, ok := fields[key]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("Cargo source metadata lacks required consumed field")
	}
	return raw, nil
}
func sourceString(fields map[string]json.RawMessage, key string, max int) (string, error) {
	raw, e := sourceField(fields, key)
	if e != nil {
		return "", e
	}
	var text string
	if json.Unmarshal(raw, &text) != nil || len(text) > max || !validText(text) {
		return "", fmt.Errorf("Cargo source metadata string exceeds supported bounds")
	}
	return text, nil
}
func CaptureSource(ctx context.Context, raw []byte, roots Roots) (out SourceEvidence, err error) {
	metadata, e := DecodeMetadata(raw, roots)
	if e != nil {
		return out, e
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(raw, &fields); e != nil {
		return out, e
	}
	packagesRaw, e := sourceField(fields, "packages")
	if e != nil {
		return out, e
	}
	membersRaw, e := sourceField(fields, "workspace_members")
	if e != nil {
		return out, e
	}
	var packages []map[string]json.RawMessage
	var members []string
	if json.Unmarshal(packagesRaw, &packages) != nil || len(packages) < 1 || len(packages) > MaxSourceManifests || json.Unmarshal(membersRaw, &members) != nil || len(members) < 1 || len(members) > MaxSourceManifests {
		return out, fmt.Errorf("Cargo workspace manifest count/type exceeds supported bounds")
	}
	paths := map[string]string{}
	for _, pkg := range packages {
		id, e := sourceString(pkg, "id", 8192)
		if e != nil {
			return out, e
		}
		path, e := sourceString(pkg, "manifest_path", 4096)
		if e != nil {
			return out, e
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "Cargo.toml" || paths[id] != "" {
			return out, fmt.Errorf("Cargo workspace package identity/path is unsupported")
		}
		paths[id] = path
	}
	out.Workspace = metadata.WorkspaceRoot
	out.WorkspaceIdentity, e = directoryEvidence(out.Workspace)
	if e != nil {
		return out, e
	}
	all := map[string]bool{filepath.Join(out.Workspace, "Cargo.toml"): true, filepath.Join(out.Workspace, "Cargo.lock"): true}
	seenIDs := map[string]bool{}
	seenPaths := map[string]bool{}
	for _, id := range members {
		path := paths[id]
		if len(id) > 8192 || !validText(id) || path == "" || seenIDs[id] || seenPaths[path] {
			return SourceEvidence{}, fmt.Errorf("Cargo workspace members are missing/ambiguous")
		}
		relative, e := filepath.Rel(out.Workspace, path)
		if e != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return SourceEvidence{}, fmt.Errorf("external workspace members are not regulated by initial managed Cargo")
		}
		seenIDs[id], seenPaths[path] = true, true
		all[path] = true
		out.Members = append(out.Members, path)
	}
	sort.Strings(out.Members)
	names := make([]string, 0, len(all))
	for path := range all {
		names = append(names, path)
	}
	sort.Strings(names)
	var total int64
	for _, path := range names {
		if e = ctx.Err(); e != nil {
			return SourceEvidence{}, e
		}
		slot, e := readConfigSlot(path)
		if e != nil {
			return SourceEvidence{}, fmt.Errorf("Cargo source manifests/lock must be bounded regular physical files")
		}
		if !slot.evidence.Present && filepath.Base(path) != "Cargo.lock" {
			return SourceEvidence{}, fmt.Errorf("Cargo source manifest disappeared")
		}
		total += slot.evidence.Size
		if total > MaxSourceBytes {
			return SourceEvidence{}, fmt.Errorf("Cargo source evidence byte count exceeds supported bounds")
		}
		out.Files = append(out.Files, slot.evidence)
	}
	if e = out.Revalidate(ctx); e != nil {
		return SourceEvidence{}, e
	}
	return out, nil
}
func (s SourceEvidence) Revalidate(ctx context.Context) error {
	current, e := directoryEvidence(s.Workspace)
	if e != nil || current != s.WorkspaceIdentity {
		return fmt.Errorf("Cargo workspace directory identity changed")
	}
	if len(s.Files) < 2 || len(s.Files) > MaxSourceManifests+2 {
		return fmt.Errorf("Cargo source evidence manifest count unsupported")
	}
	for _, expected := range s.Files {
		if e = ctx.Err(); e != nil {
			return e
		}
		actual, e := readConfigSlot(expected.Path)
		if e != nil || !reflect.DeepEqual(actual.evidence, expected) {
			return fmt.Errorf("Cargo source manifest/lock changed; compilation not authorized")
		}
	}
	return nil
}
