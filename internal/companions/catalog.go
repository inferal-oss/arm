// Package companions exposes bounded compressed platform payloads from an
// explicit carrier build. Metadata lookup is read-only. It never installs,
// executes, grants trust, writes files or downloads a companion.
package companions

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"regexp"
	"strings"
)

const MaxPayloadBytes int64 = 256 << 20
const MaxCatalogBytes int64 = 1024 << 20

var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,95}$`)
var platforms = [][2]string{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}}

type Payload struct {
	Name             string `json:"name"`
	OS               string `json:"os"`
	Arch             string `json:"arch"`
	Go               string `json:"go_version"`
	Size             int64  `json:"size_bytes"`
	SHA256           string `json:"sha256"`
	CompressedSize   int64  `json:"compressed_size_bytes"`
	CompressedSHA256 string `json:"compressed_sha256"`
}
type Manifest struct {
	Schema    int       `json:"schema"`
	Version   string    `json:"expected_arm_version"`
	Protocol  int       `json:"expected_lifecycle_protocol"`
	Integrity string    `json:"integrity_scope"`
	Artifacts []Payload `json:"artifacts"`
}

func (m Manifest) Validate(version string, protocol int) error {
	if m.Schema != 1 || !versionPattern.MatchString(m.Version) || m.Version != version || m.Protocol != protocol || protocol < 1 || len(m.Artifacts) != 4 || m.Integrity == "" {
		return fmt.Errorf("embedded companion manifest does not match expected version/protocol")
	}
	var total, compressed int64
	for i, a := range m.Artifacts {
		p := platforms[i]
		if a.Name != "arm-"+p[0]+"-"+p[1] || a.OS != p[0] || a.Arch != p[1] || !strings.HasPrefix(a.Go, "go1.") || !digest(a.SHA256) || !digest(a.CompressedSHA256) || a.Size < 1 || a.Size > MaxPayloadBytes || a.CompressedSize < 1 || a.CompressedSize > MaxPayloadBytes {
			return fmt.Errorf("invalid bounded platform companion descriptor")
		}
		total += a.Size
		compressed += a.CompressedSize
	}
	if total > MaxCatalogBytes || compressed > MaxCatalogBytes {
		return fmt.Errorf("companion catalog exceeds bound")
	}
	return nil
}
func digest(s string) bool {
	raw, e := hex.DecodeString(s)
	return e == nil && len(raw) == 32 && len(s) == 64 && s == strings.ToLower(s)
}

type Catalog struct {
	manifest Manifest
	files    fs.FS
}

// Embedded returns nil for an ordinary thin build. A malformed present carrier
// is an error, never a silent fallback to another artifact source.
func Embedded(version string, protocol int) (*Catalog, error) {
	files := embeddedFiles()
	if files == nil {
		return nil, nil
	}
	return Load(files, version, protocol)
}
func Load(files fs.FS, version string, protocol int) (*Catalog, error) {
	f, e := files.Open("generated/manifest.json")
	if e != nil {
		return nil, e
	}
	raw, e := io.ReadAll(io.LimitReader(f, 65537))
	f.Close()
	if e != nil || len(raw) > 65536 {
		return nil, fmt.Errorf("embedded manifest exceeds bound")
	}
	var m Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&m); e != nil {
		return nil, fmt.Errorf("invalid embedded manifest")
	}
	if e = m.Validate(version, protocol); e != nil {
		return nil, e
	}
	canonical, _ := json.MarshalIndent(m, "", "  ")
	canonical = append(canonical, '\n')
	if !bytes.Equal(raw, canonical) {
		return nil, fmt.Errorf("embedded manifest must use canonical exact keys; aliases/duplicates/trailing data refused")
	}
	for _, a := range m.Artifacts {
		info, e := fs.Stat(files, "generated/"+a.Name+".gz")
		if e != nil || !info.Mode().IsRegular() || info.Size() != a.CompressedSize {
			return nil, fmt.Errorf("embedded companion absent or compressed size differs")
		}
	}
	return &Catalog{manifest: m, files: files}, nil
}
func (c *Catalog) Manifest() Manifest {
	m := c.manifest
	m.Artifacts = append([]Payload{}, m.Artifacts...)
	return m
}
func (c *Catalog) Lookup(goos, arch string) (Payload, bool) {
	for _, p := range c.manifest.Artifacts {
		if p.OS == goos && p.Arch == arch {
			return p, true
		}
	}
	return Payload{}, false
}

// Open streams exactly one thin executable, verifying compressed and plaintext
// bounds/digests at EOF. Consumers must read through EOF before accepting bytes;
// no ownership or installation authority is implied by opening this stream.
func (c *Catalog) Open(goos, arch string) (io.ReadCloser, Payload, error) {
	p, ok := c.Lookup(goos, arch)
	if !ok {
		return nil, p, fmt.Errorf("unsupported companion platform")
	}
	file, e := c.files.Open("generated/" + p.Name + ".gz")
	if e != nil {
		return nil, p, e
	}
	compressed := &hashReader{reader: io.LimitReader(file, p.CompressedSize+1), hash: sha256.New()}
	buffered := bufio.NewReader(compressed)
	gz, e := gzip.NewReader(buffered)
	if e != nil {
		file.Close()
		return nil, p, e
	}
	gz.Multistream(false)
	return &payloadReader{file: file, gzip: gz, compressed: compressed, buffered: buffered, payload: p, hash: sha256.New()}, p, nil
}

type hashReader struct {
	reader io.Reader
	hash   hash.Hash
	count  int64
}

func (r *hashReader) Read(p []byte) (int, error) {
	n, e := r.reader.Read(p)
	r.hash.Write(p[:n])
	r.count += int64(n)
	return n, e
}

type payloadReader struct {
	file       fs.File
	gzip       *gzip.Reader
	compressed *hashReader
	buffered   *bufio.Reader
	payload    Payload
	hash       hash.Hash
	count      int64
	done       bool
	failed     error
}

func (r *payloadReader) Read(p []byte) (int, error) {
	if r.failed != nil {
		return 0, r.failed
	}
	if r.done {
		return 0, io.EOF
	}
	remaining := r.payload.Size - r.count + 1
	if int64(len(p)) > remaining {
		p = p[:int(remaining)]
	}
	n, e := r.gzip.Read(p)
	r.hash.Write(p[:n])
	r.count += int64(n)
	if r.count > r.payload.Size {
		r.failed = fmt.Errorf("embedded payload exceeds approved size")
		return n, r.failed
	}
	if e == io.EOF {
		// Require exact compressed EOF as well: a second stream or trailing
		// bytes must not evade the approved compressed digest/size.
		_, drainErr := r.buffered.Peek(1)
		if drainErr != io.EOF || r.count != r.payload.Size || r.compressed.count != r.payload.CompressedSize || hex.EncodeToString(r.hash.Sum(nil)) != r.payload.SHA256 || hex.EncodeToString(r.compressed.hash.Sum(nil)) != r.payload.CompressedSHA256 {
			r.failed = fmt.Errorf("embedded payload digest/size mismatch")
			return n, r.failed
		}
		r.done = true
	}
	return n, e
}
func (r *payloadReader) Close() error {
	a := r.gzip.Close()
	b := r.file.Close()
	if r.failed != nil {
		return r.failed
	}
	if a != nil {
		return a
	}
	return b
}
