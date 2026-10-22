package distribution

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// VerifyArchive checks the exact closed archive layout, content hashes, Go
// build metadata. ARM version/protocol are expected source-build values and
// are checked against actual runtime during provisioning. It never
// extracts a supplied archive path or executes a foreign architecture binary.
func VerifyArchive(ctx context.Context, archivePath string) error {
	file, e := os.Open(archivePath)
	if e != nil {
		return e
	}
	defer file.Close()
	buffered := bufio.NewReader(file)
	compressed, e := gzip.NewReader(buffered)
	if e != nil {
		return e
	}
	defer compressed.Close()
	compressed.Multistream(false)
	reader := tar.NewReader(compressed)
	temp, e := os.MkdirTemp("", "arm-bundle-check-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	type entry struct {
		header tar.Header
		local  string
		raw    []byte
	}
	entries := map[string]entry{}
	count := 0
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if count > 8 || strings.Contains(h.Name, "\\") || path.IsAbs(h.Name) || strings.ContainsRune(h.Name, 0) || strings.TrimSuffix(h.Name, "/") != path.Clean(h.Name) || len(h.PAXRecords) > 0 || h.Format != tar.FormatUSTAR || h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || h.Linkname != "" || !h.ModTime.Equal(epochTime()) {
			return fmt.Errorf("unsafe or unfamiliar bundle archive header")
		}
		if _, ok := entries[h.Name]; ok {
			return fmt.Errorf("duplicate bundle member")
		}
		item := entry{header: *h}
		switch h.Typeflag {
		case tar.TypeDir:
			if h.Mode != 0755 || h.Size != 0 || strings.Count(h.Name, "/") != 1 {
				return fmt.Errorf("invalid bundle directory")
			}
		case tar.TypeReg:
			if strings.Count(h.Name, "/") != 1 || h.Size < 1 || h.Size > maxBinary {
				return fmt.Errorf("invalid bounded bundle file")
			}
			name := path.Base(h.Name)
			if name == "manifest.json" || name == "README.txt" || name == "arm" {
				if h.Size > 65536 {
					return fmt.Errorf("text member exceeds bound")
				}
				item.raw, e = io.ReadAll(reader)
				if e != nil {
					return e
				}
			} else {
				item.local = filepath.Join(temp, fmt.Sprintf("member-%d", count))
				out, e := os.OpenFile(item.local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					return e
				}
				n, copyErr := io.Copy(out, reader)
				closeErr := out.Close()
				if copyErr != nil || closeErr != nil || n != h.Size {
					return fmt.Errorf("bundle member read failed")
				}
			}
		default:
			return fmt.Errorf("bundle forbids links, special members and extended headers")
		}
		entries[h.Name] = item
	}
	tail, e := io.ReadAll(io.LimitReader(compressed, 1025))
	if e != nil || len(tail) != 0 {
		return fmt.Errorf("unexpected data after bundle archive")
	}
	if _, e = buffered.Peek(1); e != io.EOF {
		return fmt.Errorf("trailing or concatenated gzip data")
	}
	if count != 8 {
		return fmt.Errorf("bundle must contain one directory and seven known files")
	}
	var manifest Manifest
	found := 0
	for name, item := range entries {
		if path.Base(name) == "manifest.json" {
			found++
			decoder := json.NewDecoder(bytes.NewReader(item.raw))
			decoder.DisallowUnknownFields()
			if e = decoder.Decode(&manifest); e != nil {
				return e
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				return fmt.Errorf("multiple manifest JSON values")
			}
		}
	}
	if found != 1 || manifest.Schema != 1 || !versionRE.MatchString(manifest.Version) || manifest.Version == "." || manifest.Version == ".." || manifest.Protocol < 1 || len(manifest.Artifacts) != 4 || manifest.Integrity == "" {
		return fmt.Errorf("invalid bundle manifest")
	}
	canonical, e := json.MarshalIndent(manifest, "", "  ")
	if e != nil {
		return e
	}
	canonical = append(canonical, '\n')
	for name, item := range entries {
		if path.Base(name) == "manifest.json" && !bytes.Equal(canonical, item.raw) {
			return fmt.Errorf("manifest must use the producer's canonical exact keys; duplicates/aliases are refused")
		}
	}
	prefix := "arm-" + manifest.Version + "/"
	dir, ok := entries[prefix]
	if !ok || dir.header.Typeflag != tar.TypeDir {
		return fmt.Errorf("bundle root/version mismatch")
	}
	for _, name := range []string{"arm", "manifest.json", "README.txt"} {
		item, ok := entries[prefix+name]
		mode := int64(0644)
		if name == "arm" {
			mode = 0755
		}
		if !ok || item.header.Typeflag != tar.TypeReg || item.header.Mode != mode || len(item.raw) == 0 {
			return fmt.Errorf("missing or unsafe bundle text member")
		}
		if name == "arm" && string(item.raw) != launcher {
			return fmt.Errorf("launcher differs from reviewed platform selector")
		}
	}
	flags := "-X=" + tagVariable + "=arm-bundle-v1:" + manifest.Version + ":" + fmt.Sprint(manifest.Protocol)
	for i, a := range manifest.Artifacts {
		p := platforms[i]
		expected := "arm-" + p[0] + "-" + p[1]
		sum, err := hex.DecodeString(a.SHA256)
		if a.Name != expected || a.OS != p[0] || a.Arch != p[1] || err != nil || len(sum) != 32 || a.SHA256 != strings.ToLower(a.SHA256) || a.Size < 1 || a.Size > maxBinary || !strings.HasPrefix(a.Go, "go1.") {
			return fmt.Errorf("invalid platform artifact descriptor")
		}
		item, ok := entries[prefix+a.Name]
		if !ok || item.local == "" || item.header.Mode != 0755 || item.header.Size != a.Size {
			return fmt.Errorf("missing or unsafe platform artifact")
		}
		actual, err := inspect(item.local, a.Name, a.OS, a.Arch, flags)
		if err != nil {
			return err
		}
		if actual != a {
			return fmt.Errorf("manifest digest/size/Go version differs from executable bytes")
		}
	}
	return nil
}
