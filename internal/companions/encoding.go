package companions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
)

func Canonical(m Manifest) ([]byte, string, error) {
	if e := m.Validate(m.Version, m.Protocol); e != nil {
		return nil, "", e
	}
	raw, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return nil, "", e
	}
	raw = append(raw, '\n')
	h := sha256.Sum256(raw)
	return raw, hex.EncodeToString(h[:]), nil
}

// OpenCompressed forwards the exact reviewed compressed bytes for onward
// catalog seeding. EOF verifies the size/digest; no recompression or file write.
func (c *Catalog) OpenCompressed(goos, arch string) (io.ReadCloser, Payload, error) {
	p, ok := c.Lookup(goos, arch)
	if !ok {
		return nil, p, fmt.Errorf("unsupported companion platform")
	}
	f, e := c.files.Open("generated/" + p.Name + ".gz")
	if e != nil {
		return nil, p, e
	}
	return &compressedPayload{file: f, reader: io.LimitReader(f, p.CompressedSize+1), expected: p, hash: sha256.New()}, p, nil
}

type compressedPayload struct {
	file     fs.File
	reader   io.Reader
	expected Payload
	hash     hash.Hash
	size     int64
	failure  error
	done     bool
}

func (r *compressedPayload) Read(p []byte) (int, error) {
	if r.failure != nil {
		return 0, r.failure
	}
	if r.done {
		return 0, io.EOF
	}
	n, e := r.reader.Read(p)
	r.size += int64(n)
	r.hash.Write(p[:n])
	if r.size > r.expected.CompressedSize {
		r.failure = fmt.Errorf("compressed companion exceeds approved size")
		return n, r.failure
	}
	if e == io.EOF {
		if r.size != r.expected.CompressedSize || hex.EncodeToString(r.hash.Sum(nil)) != r.expected.CompressedSHA256 {
			r.failure = fmt.Errorf("compressed companion digest/size differs")
			return n, r.failure
		}
		r.done = true
	}
	return n, e
}
func (r *compressedPayload) Close() error {
	e := r.file.Close()
	if r.failure != nil {
		return r.failure
	}
	return e
}

// Frames streams the four exact compressed payloads in canonical manifest
// order, opening only one file at a time. The receiver already has exact lengths.
func (c *Catalog) Frames() io.ReadCloser { return &frameReader{catalog: c} }

type frameReader struct {
	catalog *Catalog
	index   int
	current io.ReadCloser
	failure error
}

func (r *frameReader) Read(p []byte) (int, error) {
	if r.failure != nil {
		return 0, r.failure
	}
	for r.index < len(r.catalog.manifest.Artifacts) {
		if r.current == nil {
			a := r.catalog.manifest.Artifacts[r.index]
			f, _, e := r.catalog.OpenCompressed(a.OS, a.Arch)
			if e != nil {
				r.failure = e
				return 0, e
			}
			r.current = f
		}
		n, e := r.current.Read(p)
		if e == io.EOF {
			ce := r.current.Close()
			r.current = nil
			r.index++
			if ce != nil {
				r.failure = ce
				return n, ce
			}
			if n > 0 {
				return n, nil
			}
			continue
		}
		if e != nil {
			r.failure = e
		}
		return n, e
	}
	return 0, io.EOF
}
func (r *frameReader) Close() error {
	if r.current != nil {
		return r.current.Close()
	}
	return r.failure
}
