// Package workerpackage contains private pure parsing of untrusted declarations.
// No declaration is provenance, native identity, compatibility or admission.
package workerpackage

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxManifestBytes = 64 << 10
	maxFiles         = 128
	maxDeclaredBytes = uint64(512 << 20)
	maxPathBytes     = 1024
	maxPathParts     = 64
	maxComponent     = 128
)

type candidate struct {
	schema                                uint64
	os, arch, build, protocol, entrypoint string
	files                                 []fileDeclaration
}

type fileDeclaration struct {
	path   string
	size   uint64
	sha256 string
}

func refused() error { return fmt.Errorf("invalid untrusted worker package declaration") }

func parse(raw []byte) (candidate, error) {
	// Length must precede UTF-8, JSON scanning, copying or normalization.
	if len(raw) > maxManifestBytes || len(raw) == 0 || !utf8.Valid(raw) {
		return candidate{}, refused()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := delimiter(d, '{'); err != nil {
		return candidate{}, err
	}
	var c candidate
	var seen uint8
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return candidate{}, refused()
		}
		key, ok := token.(string)
		if !ok {
			return candidate{}, refused()
		}
		var bit uint8
		switch key {
		case "schema":
			bit = 1
		case "os":
			bit = 2
		case "arch":
			bit = 4
		case "build":
			bit = 8
		case "protocol":
			bit = 16
		case "entrypoint":
			bit = 32
		case "files":
			bit = 64
		default:
			return candidate{}, refused()
		}
		if seen&bit != 0 {
			return candidate{}, refused()
		}
		seen |= bit
		switch key {
		case "schema":
			c.schema, err = integer(d)
		case "os":
			c.os, err = text(d)
		case "arch":
			c.arch, err = text(d)
		case "build":
			c.build, err = text(d)
		case "protocol":
			c.protocol, err = text(d)
		case "entrypoint":
			c.entrypoint, err = text(d)
		case "files":
			c.files, err = files(d)
		}
		if err != nil {
			return candidate{}, err
		}
	}
	if seen != 127 {
		return candidate{}, refused()
	}
	if err := delimiter(d, '}'); err != nil {
		return candidate{}, err
	}
	if _, err := d.Token(); err != io.EOF {
		return candidate{}, refused()
	}
	if err := validate(c); err != nil {
		return candidate{}, err
	}
	return c, nil
}

func delimiter(d *json.Decoder, want json.Delim) error {
	token, err := d.Token()
	if err != nil || token != want {
		return refused()
	}
	return nil
}

func text(d *json.Decoder) (string, error) {
	token, err := d.Token()
	s, ok := token.(string)
	if err != nil || !ok {
		return "", refused()
	}
	return s, nil
}

func integer(d *json.Decoder) (uint64, error) {
	token, err := d.Token()
	number, ok := token.(json.Number)
	if err != nil || !ok {
		return 0, refused()
	}
	s := string(number)
	if len(s) == 0 || len(s) > 20 || len(s) > 1 && s[0] == '0' {
		return 0, refused()
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, refused()
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, refused()
	}
	return n, nil
}

func files(d *json.Decoder) ([]fileDeclaration, error) {
	if err := delimiter(d, '['); err != nil {
		return nil, err
	}
	var out []fileDeclaration
	set := fileSet{paths: make(map[string]struct{})}
	for d.More() {
		// Refuse before reading/allocating another object, even if malformed.
		if len(out) >= maxFiles {
			return nil, refused()
		}
		f, err := file(d)
		if err != nil {
			return nil, err
		}
		if err := set.add(f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, refused()
	}
	if err := delimiter(d, ']'); err != nil {
		return nil, err
	}
	return out, nil
}

func file(d *json.Decoder) (fileDeclaration, error) {
	if err := delimiter(d, '{'); err != nil {
		return fileDeclaration{}, err
	}
	var f fileDeclaration
	var seen uint8
	for d.More() {
		key, err := text(d)
		if err != nil {
			return fileDeclaration{}, err
		}
		var bit uint8
		switch key {
		case "path":
			bit = 1
		case "size_bytes":
			bit = 2
		case "sha256":
			bit = 4
		default:
			return fileDeclaration{}, refused()
		}
		if seen&bit != 0 {
			return fileDeclaration{}, refused()
		}
		seen |= bit
		switch key {
		case "path":
			f.path, err = text(d)
		case "size_bytes":
			f.size, err = integer(d)
		case "sha256":
			f.sha256, err = text(d)
		}
		if err != nil {
			return fileDeclaration{}, err
		}
	}
	if seen != 7 {
		return fileDeclaration{}, refused()
	}
	if err := delimiter(d, '}'); err != nil {
		return fileDeclaration{}, err
	}
	return f, nil
}

// Identifiers follow the existing private version spelling: alphanumeric first,
// then ASCII alphanumeric or . _ + -. These are labels, not protocol negotiation.
func identifier(s string, limit int) bool {
	if len(s) == 0 || len(s) > limit {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i == 0 || c != '.' && c != '_' && c != '+' && c != '-' {
			return false
		}
	}
	return true
}

func validPath(path string) bool {
	if len(path) == 0 || len(path) > maxPathBytes {
		return false
	}
	if path != "arm" && (!strings.HasPrefix(path, "assets/") || len(path) == len("assets/")) {
		return false
	}
	parts, start := 0, 0
	for i := 0; i <= len(path); i++ {
		if i < len(path) && path[i] != '/' {
			c := path[i]
			if c < 0x20 || c > 0x7e || c == '\\' || c == ':' || c == '%' {
				return false
			}
			continue
		}
		part := path[start:i]
		parts++
		if parts > maxPathParts || len(part) == 0 || len(part) > maxComponent || part == "." || part == ".." {
			return false
		}
		start = i + 1
	}
	return true
}

func digest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9' || s[i] >= 'a' && s[i] <= 'f') {
			return false
		}
	}
	return true
}

type fileSet struct {
	paths map[string]struct{}
	total uint64
}

func (set *fileSet) add(f fileDeclaration) error {
	if !validPath(f.path) || !digest(f.sha256) || f.size > maxDeclaredBytes || f.path == "arm" && f.size == 0 || f.size > maxDeclaredBytes-set.total {
		return refused()
	}
	fold := strings.ToLower(f.path) // path is already bounded ASCII
	if _, ok := set.paths[fold]; ok {
		return refused()
	}
	for previous := range set.paths {
		if strings.HasPrefix(fold, previous+"/") || strings.HasPrefix(previous, fold+"/") {
			return refused()
		}
	}
	set.paths[fold] = struct{}{}
	set.total += f.size // checked above, including aggregate/overflow
	return nil
}

func validate(c candidate) error {
	if len(c.files) < 1 || len(c.files) > maxFiles || c.schema != 1 ||
		(c.os != "darwin" && c.os != "linux") || (c.arch != "amd64" && c.arch != "arm64") ||
		!identifier(c.build, 128) || !identifier(c.protocol, 64) || c.entrypoint != "arm" {
		return refused()
	}
	set := fileSet{paths: make(map[string]struct{})}
	for _, f := range c.files {
		if err := set.add(f); err != nil {
			return err
		}
	}
	if _, ok := set.paths["arm"]; !ok {
		return refused()
	}
	return nil
}

// canonical revalidates even manually constructed candidates. Fixed-capacity
// output and checked appends cannot emit a manifest beyond the raw parser cap.
func canonical(c candidate) ([]byte, error) {
	if err := validate(c); err != nil {
		return nil, err
	}
	ordered := append([]fileDeclaration(nil), c.files...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].path < ordered[j].path })
	out := make([]byte, 0, maxManifestBytes)
	put := func(s string) error {
		if len(s) > maxManifestBytes-len(out) {
			return refused()
		}
		out = append(out, s...)
		return nil
	}
	quote := func(s string) error { return put(strconv.Quote(s)) }
	for _, s := range []string{`{"schema":1,"os":`, strconv.Quote(c.os), `,"arch":`, strconv.Quote(c.arch), `,"build":`, strconv.Quote(c.build), `,"protocol":`, strconv.Quote(c.protocol), `,"entrypoint":"arm","files":[`} {
		if err := put(s); err != nil {
			return nil, err
		}
	}
	for i, f := range ordered {
		if i > 0 {
			if err := put(","); err != nil {
				return nil, err
			}
		}
		if err := put(`{"path":`); err != nil {
			return nil, err
		}
		if err := quote(f.path); err != nil {
			return nil, err
		}
		if err := put(`,"size_bytes":` + strconv.FormatUint(f.size, 10) + `,"sha256":`); err != nil {
			return nil, err
		}
		if err := quote(f.sha256); err != nil {
			return nil, err
		}
		if err := put("}"); err != nil {
			return nil, err
		}
	}
	if err := put("]}"); err != nil {
		return nil, err
	}
	return out, nil
}

func commitment(c candidate) ([32]byte, error) {
	raw, err := canonical(c)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}
