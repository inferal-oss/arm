package workerpackage

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const testDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const golden = `{"schema":1,"os":"darwin","arch":"arm64","build":"v1.2.3+local","protocol":"worker.v1","entrypoint":"arm","files":[{"path":"arm","size_bytes":1,"sha256":"` + testDigest + `"}]}`

func base() candidate {
	return candidate{schema: 1, os: "darwin", arch: "arm64", build: "v1.2.3+local", protocol: "worker.v1", entrypoint: "arm", files: []fileDeclaration{{"arm", 1, testDigest}}}
}

// Ordinary test JSON comes from an independent encoder, not canonical().
func declaration(t *testing.T, c candidate) []byte {
	t.Helper()
	files := make([]map[string]any, 0, len(c.files))
	for _, f := range c.files {
		files = append(files, map[string]any{"path": f.path, "size_bytes": f.size, "sha256": f.sha256})
	}
	raw, err := json.Marshal(map[string]any{"schema": c.schema, "os": c.os, "arch": c.arch, "build": c.build, "protocol": c.protocol, "entrypoint": c.entrypoint, "files": files})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func accepted(t *testing.T, raw []byte) candidate {
	t.Helper()
	before := append([]byte(nil), raw...)
	c, err := parse(raw)
	if err != nil {
		t.Fatal("valid declaration refused", err)
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("parse mutated raw input")
	}
	return c
}

func rejected(t *testing.T, raw []byte) {
	t.Helper()
	before := append([]byte(nil), raw...)
	if _, err := parse(raw); err == nil {
		t.Fatal("invalid declaration accepted")
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("refusal mutated raw input")
	}
}

func TestManifestDeclaredPlatformsAndCanonicalGolden(t *testing.T) {
	for _, os := range []string{"darwin", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(os+"/"+arch, func(t *testing.T) {
				want := base()
				want.os, want.arch = os, arch
				got := accepted(t, declaration(t, want))
				if !reflect.DeepEqual(got, want) {
					t.Fatal("declared metadata changed")
				}
			})
		}
	}
	c := accepted(t, []byte(golden))
	raw, err := canonical(c)
	if err != nil || string(raw) != golden {
		t.Fatal("canonical fixed fields/integers differ", string(raw), err)
	}
	if !reflect.DeepEqual(accepted(t, raw), c) {
		t.Fatal("canonical roundtrip changed declarations")
	}
	got, err := commitment(c)
	if err != nil || hex.EncodeToString(got[:]) != "580ae3a6d5cfab8b585304817c30b5376f79731ba9eccad1583288d12a684027" {
		t.Fatal("canonical commitment differs", hex.EncodeToString(got[:]), err)
	}
}

func TestManifestCanonicalOrderEscapesAndChangedCommitments(t *testing.T) {
	c := base()
	c.files = append(c.files, fileDeclaration{"assets/Z.js", 0, testDigest}, fileDeclaration{`assets/a "quoted".js`, 7, testDigest})
	before := append([]fileDeclaration(nil), c.files...)
	raw, err := canonical(c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.files, before) {
		t.Fatal("canonical mutated file order")
	}
	parsed := accepted(t, raw)
	if parsed.files[0].path != "arm" || parsed.files[1].path != "assets/Z.js" || parsed.files[2].path != `assets/a "quoted".js` {
		t.Fatal("not bytewise path ordering")
	}
	reversed := c
	reversed.files = []fileDeclaration{c.files[2], c.files[1], c.files[0]}
	again, err := canonical(reversed)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("order changed canonical bytes", err)
	}
	escaped := strings.ReplaceAll(golden, `"schema"`, `"s\u0063hema"`)
	escaped = strings.ReplaceAll(escaped, `"arm"`, `"\u0061rm"`)
	if round, err := canonical(accepted(t, []byte(escaped))); err != nil || string(round) != golden {
		t.Fatal("ordinary decoded escapes did not canonicalize", err)
	}
	initial, err := commitment(c)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*candidate){
		"os": func(c *candidate) { c.os = "linux" }, "arch": func(c *candidate) { c.arch = "amd64" },
		"build": func(c *candidate) { c.build = "v2" }, "protocol": func(c *candidate) { c.protocol = "worker.v2" },
		"path": func(c *candidate) { c.files[1].path = "assets/Y.js" }, "size": func(c *candidate) { c.files[1].size = 1 },
		"digest": func(c *candidate) { c.files[1].sha256 = strings.Repeat("a", 64) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := c
			changed.files = append([]fileDeclaration(nil), c.files...)
			mutate(&changed)
			h, err := commitment(changed)
			if err != nil || h == initial {
				t.Fatal("changed declared data did not change commitment", err)
			}
		})
	}
}

func TestManifestExactDeclaredBoundaries(t *testing.T) {
	for _, kind := range []string{"raw", "count", "build", "protocol", "component", "path", "parts", "file-bytes", "total-bytes"} {
		t.Run(kind, func(t *testing.T) {
			good, bad := base(), base()
			switch kind {
			case "raw":
				raw := append([]byte(golden), bytes.Repeat([]byte{' '}, maxManifestBytes-len(golden))...)
				accepted(t, raw)
				rejected(t, append(raw, ' '))
				return
			case "count":
				for i := 1; i < maxFiles; i++ {
					good.files = append(good.files, fileDeclaration{fmt.Sprintf("assets/%03d", i), 0, testDigest})
				}
				bad.files = append(append([]fileDeclaration(nil), good.files...), fileDeclaration{"assets/extra", 0, testDigest})
			case "build":
				good.build = strings.Repeat("b", 128)
				bad.build = good.build + "b"
			case "protocol":
				good.protocol = strings.Repeat("p", 64)
				bad.protocol = good.protocol + "p"
			case "component":
				good.files = append(good.files, fileDeclaration{"assets/" + strings.Repeat("a", 128), 0, testDigest})
				bad.files = append(bad.files, fileDeclaration{"assets/" + strings.Repeat("a", 129), 0, testDigest})
			case "path":
				path := func(n int) string {
					out := "assets/"
					left := n - len(out)
					for left > maxComponent {
						out += strings.Repeat("a", maxComponent) + "/"
						left -= maxComponent + 1
					}
					return out + strings.Repeat("b", left)
				}
				good.files = append(good.files, fileDeclaration{path(1024), 0, testDigest})
				bad.files = append(bad.files, fileDeclaration{path(1025), 0, testDigest})
			case "parts":
				good.files = append(good.files, fileDeclaration{"assets/" + strings.Repeat("a/", 62) + "a", 0, testDigest})
				bad.files = append(bad.files, fileDeclaration{"assets/" + strings.Repeat("a/", 63) + "a", 0, testDigest})
			case "file-bytes":
				good.files[0].size = maxDeclaredBytes
				bad.files[0].size = maxDeclaredBytes + 1
			case "total-bytes":
				good.files = append(good.files, fileDeclaration{"assets/a", maxDeclaredBytes - 1, testDigest})
				bad.files = append(bad.files, fileDeclaration{"assets/a", maxDeclaredBytes, testDigest})
			}
			accepted(t, declaration(t, good))
			rejected(t, declaration(t, bad))
			if raw, err := canonical(good); err != nil {
				t.Fatal("exact bound did not encode", err)
			} else {
				accepted(t, raw)
			}
			if _, err := canonical(bad); err == nil {
				t.Fatal("manual excess candidate encoded")
			}
		})
	}
}

func TestManifestStrictObjectsTypesNumbersAndTrailing(t *testing.T) {
	variants := map[string]string{
		"empty": "", "root-null": "null", "root-array": "[]", "trailing-value": golden + "{}", "trailing-garbage": golden + "x",
		"unknown-root":           strings.Replace(golden, `"schema":1`, `"unknown":0,"schema":1`, 1),
		"duplicate-root":         strings.Replace(golden, `"schema":1`, `"schema":1,"schema":1`, 1),
		"escaped-duplicate-root": strings.Replace(golden, `"schema":1`, `"schema":1,"s\u0063hema":1`, 1),
		"case-root":              strings.Replace(golden, `"schema"`, `"SCHEMA"`, 1),
		"fold-root":              strings.Replace(golden, `"schema"`, `"\u017fchema"`, 1),
		"missing-root":           strings.Replace(golden, `"schema":1,`, "", 1),
		"unknown-file":           strings.Replace(golden, `"path":"arm"`, `"extra":null,"path":"arm"`, 1),
		"duplicate-file":         strings.Replace(golden, `"path":"arm"`, `"path":"arm","path":"arm"`, 1),
		"escaped-duplicate-file": strings.Replace(golden, `"path":"arm"`, `"path":"arm","p\u0061th":"arm"`, 1),
		"case-file":              strings.Replace(golden, `"path"`, `"PATH"`, 1),
		"fold-file":              strings.Replace(golden, `"size_bytes"`, `"\u017fize_bytes"`, 1),
		"missing-file":           strings.Replace(golden, `"size_bytes":1,`, "", 1),
		"files-null":             strings.Replace(golden, `"files":[`, `"files":null,"other":[`, 1),
		"files-empty":            strings.Replace(golden, golden[strings.Index(golden, `"files":`):], `"files":[]}`, 1),
		"file-not-object":        strings.Replace(golden, `[{"path":"arm","size_bytes":1,"sha256":"`+testDigest+`"}]`, `[true]`, 1),
		"os-type":                strings.Replace(golden, `"os":"darwin"`, `"os":false`, 1),
		"size-type":              strings.Replace(golden, `"size_bytes":1`, `"size_bytes":"1"`, 1),
		"schema-type":            strings.Replace(golden, `"schema":1`, `"schema":null`, 1),
		"surrogate":              strings.Replace(golden, `"build":"v1.2.3+local"`, `"build":"\ud800"`, 1),
	}
	for _, number := range []string{"-1", "-0", "1.0", "1e0", "1E+0", "01", "+1", "18446744073709551616", "9999999999999999999999999999999999"} {
		variants["size-number/"+number] = strings.Replace(golden, `"size_bytes":1`, `"size_bytes":`+number, 1)
		variants["schema-number/"+number] = strings.Replace(golden, `"schema":1`, `"schema":`+number, 1)
	}
	for name, raw := range variants {
		t.Run(name, func(t *testing.T) { rejected(t, []byte(raw)) })
	}
	invalidUTF8 := []byte(golden)
	invalidUTF8[bytes.Index(invalidUTF8, []byte("v1.2.3"))] = 0xff
	rejected(t, invalidUTF8)
}

func TestManifestPathDigestAndSetRefusals(t *testing.T) {
	for name, path := range map[string]string{
		"absolute": "/assets/a", "outside": "bin/arm", "assets-root": "assets", "empty-asset": "assets/",
		"empty-component": "assets//a", "dot": "assets/./a", "parent": "assets/../a", "trailing": "assets/a/",
		"backslash": `assets/a\b`, "colon": "assets/a:b", "percent": "assets/a%20b", "control": "assets/a\n", "del": "assets/a\x7f", "unicode": "assets/é",
	} {
		t.Run(name, func(t *testing.T) {
			c := base()
			c.files = append(c.files, fileDeclaration{path, 0, testDigest})
			rejected(t, declaration(t, c))
		})
	}
	for name, mutate := range map[string]func(*candidate){
		"missing-arm": func(c *candidate) { c.files[0].path = "assets/a" }, "empty-arm": func(c *candidate) { c.files[0].size = 0 },
		"duplicate": func(c *candidate) { c.files = append(c.files, c.files[0]) },
		"case-fold": func(c *candidate) {
			c.files = append(c.files, fileDeclaration{"assets/A", 0, testDigest}, fileDeclaration{"assets/a", 0, testDigest})
		},
		"prefix": func(c *candidate) {
			c.files = append(c.files, fileDeclaration{"assets/a", 0, testDigest}, fileDeclaration{"assets/a/b", 0, testDigest})
		},
		"reverse-prefix": func(c *candidate) {
			c.files = append(c.files, fileDeclaration{"assets/A/b", 0, testDigest}, fileDeclaration{"assets/a", 0, testDigest})
		},
		"sha-upper": func(c *candidate) { c.files[0].sha256 = strings.ToUpper(testDigest) },
		"sha-short": func(c *candidate) { c.files[0].sha256 = testDigest[:63] }, "sha-long": func(c *candidate) { c.files[0].sha256 = testDigest + "0" },
		"sha-nonhex": func(c *candidate) { c.files[0].sha256 = strings.Repeat("g", 64) },
		"wrong-os":   func(c *candidate) { c.os = "Darwin" }, "wrong-arch": func(c *candidate) { c.arch = "x86_64" }, "wrong-schema": func(c *candidate) { c.schema = 2 },
		"entrypoint": func(c *candidate) { c.entrypoint = "assets/arm" }, "empty-build": func(c *candidate) { c.build = "" }, "build-space": func(c *candidate) { c.build = "bad build" },
		"protocol-slash": func(c *candidate) { c.protocol = "worker/v1" }, "empty-protocol": func(c *candidate) { c.protocol = "" },
		"manual-overflow": func(c *candidate) { c.files[0].size = ^uint64(0) },
	} {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutate(&c)
			rejected(t, declaration(t, c))
			before := append([]fileDeclaration(nil), c.files...)
			if out, err := canonical(c); err == nil || out != nil {
				t.Fatal("malformed manual candidate encoded")
			}
			if h, err := commitment(c); err == nil || h != [32]byte{} {
				t.Fatal("malformed candidate got commitment")
			}
			if !reflect.DeepEqual(before, c.files) {
				t.Fatal("manual refusal mutated candidate")
			}
		})
	}
}

func TestManifestManualEncodedSizeAndConcurrentPurity(t *testing.T) {
	c := base()
	for i := 1; i < maxFiles; i++ {
		c.files = append(c.files, fileDeclaration{fmt.Sprintf("assets/%03d/", i) + strings.Repeat("a/", 4) + strings.Repeat("b", 128) + "/" + strings.Repeat("c", 128) + "/" + strings.Repeat("d", 128) + "/" + strings.Repeat("e", 128), 0, testDigest})
	}
	if err := validate(c); err != nil {
		t.Fatal("fixture individual ceilings invalid", err)
	}
	if raw, err := canonical(c); err == nil || raw != nil {
		t.Fatal("encoded raw-byte ceiling was ignored")
	}
	if _, err := commitment(c); err == nil {
		t.Fatal("oversized canonical declaration got commitment")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 8 {
				c, err := parse([]byte(golden))
				if err != nil {
					t.Error(err)
					return
				}
				raw, err := canonical(c)
				if err != nil || string(raw) != golden {
					t.Error("concurrent pure roundtrip changed", err)
					return
				}
			}
		})
	}
	wg.Wait()
}
