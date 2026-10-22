package companions

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/fstest"
)

func sum(p []byte) string { h := sha256.Sum256(p); return hex.EncodeToString(h[:]) }
func fixture(t *testing.T) (fstest.MapFS, Manifest) {
	t.Helper()
	files := fstest.MapFS{}
	m := Manifest{Schema: 1, Version: "0.1.0-dev", Protocol: 1, Integrity: "fixture integrity only", Artifacts: []Payload{}}
	for _, p := range platforms {
		plain := []byte("thin fixture " + p[0] + "/" + p[1])
		var out bytes.Buffer
		gz := gzip.NewWriter(&out)
		gz.Write(plain)
		gz.Close()
		name := "arm-" + p[0] + "-" + p[1]
		compressed := out.Bytes()
		m.Artifacts = append(m.Artifacts, Payload{Name: name, OS: p[0], Arch: p[1], Go: "go1.27.3", Size: int64(len(plain)), SHA256: sum(plain), CompressedSize: int64(len(compressed)), CompressedSHA256: sum(compressed)})
		files["generated/"+name+".gz"] = &fstest.MapFile{Data: compressed, Mode: 0600}
	}
	raw, _ := json.MarshalIndent(m, "", "  ")
	files["generated/manifest.json"] = &fstest.MapFile{Data: append(raw, '\n'), Mode: 0600}
	return files, m
}
func TestThinHasNoEmbeddedCatalog(t *testing.T) {
	c, e := Embedded("0.1.0-dev", 1)
	if e != nil || c != nil {
		t.Fatal(c, e)
	}
}
func TestAllFourCompanionsStreamWithBoundedIntegrityAndImmutableMetadata(t *testing.T) {
	files, m := fixture(t)
	catalog, e := Load(files, m.Version, m.Protocol)
	if e != nil {
		t.Fatal(e)
	}
	changed := catalog.Manifest()
	changed.Artifacts[0].SHA256 = "caller mutation"
	if catalog.Manifest().Artifacts[0].SHA256 == "caller mutation" {
		t.Fatal("mutable manifest escaped")
	}
	for _, p := range platforms {
		stream, a, e := catalog.Open(p[0], p[1])
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(stream)
		if e != nil || stream.Close() != nil || sum(data) != a.SHA256 || int64(len(data)) != a.Size {
			t.Fatal("invalid selected stream", e)
		}
	}
	if _, _, e = catalog.Open("other", "other"); e == nil {
		t.Fatal("unsupported platform selected")
	}
}
func TestCorruptTruncatedAndConcatenatedPayloadsRefused(t *testing.T) {
	for _, kind := range []string{"corrupt", "truncated", "plaintext-size", "plaintext-digest", "concatenated"} {
		t.Run(kind, func(t *testing.T) {
			files, m := fixture(t)
			p := &m.Artifacts[0]
			file := files["generated/"+p.Name+".gz"]
			switch kind {
			case "corrupt":
				file.Data[15] ^= 0xff
			case "truncated":
				file.Data = file.Data[:len(file.Data)-5]
				p.CompressedSize = int64(len(file.Data))
				p.CompressedSHA256 = sum(file.Data)
			case "plaintext-size":
				p.Size--
			case "plaintext-digest":
				p.SHA256 = strings.Repeat("a", 64)
			case "concatenated":
				file.Data = append(file.Data, files["generated/"+m.Artifacts[1].Name+".gz"].Data...)
				p.CompressedSize = int64(len(file.Data))
				p.CompressedSHA256 = sum(file.Data)
			}
			raw, _ := json.MarshalIndent(m, "", "  ")
			files["generated/manifest.json"].Data = append(raw, '\n')
			catalog, e := Load(files, m.Version, m.Protocol)
			if e != nil {
				return
			}
			reader, _, e := catalog.Open(p.OS, p.Arch)
			if e != nil {
				return
			}
			_, e = io.Copy(io.Discard, reader)
			reader.Close()
			if e == nil {
				t.Fatal("invalid stream accepted")
			}
		})
	}
}
func TestManifestAliasesPathsVersionProtocolAndSizeFailClosed(t *testing.T) {
	for _, kind := range []string{"alias", "path", "version", "protocol", "size"} {
		t.Run(kind, func(t *testing.T) {
			files, m := fixture(t)
			switch kind {
			case "path":
				m.Artifacts[0].Name = "../escape"
			case "version":
				m.Version = "wrong"
			case "protocol":
				m.Protocol = 2
			case "size":
				m.Artifacts[0].Size = MaxPayloadBytes + 1
			}
			raw, _ := json.MarshalIndent(m, "", "  ")
			if kind == "alias" {
				raw = bytes.Replace(raw, []byte(`"schema"`), []byte(`"SCHEMA"`), 1)
			}
			files["generated/manifest.json"].Data = append(raw, '\n')
			if _, e := Load(files, "0.1.0-dev", 1); e == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}
