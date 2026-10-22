// Package distribution creates a single portable companion bundle using Go's
// own compiler and archive libraries. Its manifest is integrity metadata only;
// it is not a signed release or network download trust source.
package distribution

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxBinary = 256 << 20

func epochTime() time.Time { return time.Unix(0, 0).UTC() }

var platforms = [][2]string{{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}}
var versionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,95}$`)

const tagVariable = "github.com/yrashk/arm/internal/buildinfo.BundleTag"

type Artifact struct {
	Name   string `json:"name"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Go     string `json:"go_version"`
	Size   int64  `json:"size_bytes"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Schema    int        `json:"schema"`
	Version   string     `json:"arm_version"`
	Protocol  int        `json:"lifecycle_protocol"`
	Integrity string     `json:"integrity_scope"`
	Artifacts []Artifact `json:"artifacts"`
}
type Options struct {
	Root, GoBinary, Output, Version string
	Protocol                        int
	Log                             io.Writer
}

// Build uses the selected Go to build four CGo-free siblings, verifies build
// metadata/bytes, then publishes one non-overwriting archive. No source/binary
// downloads or compilers on the destination are involved.
func Build(ctx context.Context, o Options) (string, Manifest, error) {
	var manifest Manifest
	if !versionRE.MatchString(o.Version) || o.Version == "." || o.Version == ".." || o.Protocol < 1 {
		return "", manifest, fmt.Errorf("invalid bundle version/protocol")
	}
	root, e := filepath.Abs(o.Root)
	if e != nil {
		return "", manifest, e
	}
	goPath, e := exec.LookPath(o.GoBinary)
	if e != nil {
		return "", manifest, e
	}
	goPath, e = filepath.Abs(goPath)
	if e != nil {
		return "", manifest, e
	}
	output := o.Output
	if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	if e = os.MkdirAll(output, 0755); e != nil {
		return "", manifest, e
	}
	archivePath := filepath.Join(output, "arm-"+o.Version+".tar.gz")
	if _, e = os.Lstat(archivePath); e == nil {
		return "", manifest, fmt.Errorf("archive already exists; choose another output directory")
	} else if !os.IsNotExist(e) {
		return "", manifest, e
	}
	temp, e := os.MkdirTemp(output, ".arm-bundle-")
	if e != nil {
		return "", manifest, e
	}
	defer os.RemoveAll(temp)
	manifest, e = buildPayloads(ctx, o, root, goPath, temp)
	if e != nil {
		return "", manifest, e
	}
	raw, e := json.MarshalIndent(manifest, "", "  ")
	if e != nil {
		return "", manifest, e
	}
	raw = append(raw, '\n')
	if e = os.WriteFile(filepath.Join(temp, "manifest.json"), raw, 0644); e != nil {
		return "", manifest, e
	}
	if e = os.WriteFile(filepath.Join(temp, "arm"), []byte(launcher), 0755); e != nil {
		return "", manifest, e
	}
	readme := fmt.Sprintf("ARM %s — portable companion bundle\n\nExtract the complete directory, then run ./arm version or ./arm --help.\nThe POSIX launcher selects this machine's darwin/linux arm64/amd64 executable,\npreserves the caller's working directory and arguments, and needs no Go or C\ncompiler. Keep all four companion binaries beside the selected executable:\narm mesh connect can select a matching sibling for separately reviewed\nmissing-node installation. No destination download is configured.\n\nmanifest.json records ARM version, lifecycle protocol, Go versions, sizes and\nSHA256 hashes. It is local integrity metadata, NOT a signature/authenticated\nrelease manifest. Verify source/artifact trust independently.\n\nBuild: make bundle-archive GO=/absolute/path/to/go [BUNDLE_DIR=dist]\nOutput: arm-%s.tar.gz; existing archives are never overwritten.\n", o.Version, o.Version)
	if e = os.WriteFile(filepath.Join(temp, "README.txt"), []byte(readme), 0644); e != nil {
		return "", manifest, e
	}
	staged := filepath.Join(temp, "archive.tar.gz")
	if e = writeArchive(ctx, staged, temp, "arm-"+o.Version, manifest); e != nil {
		return "", manifest, e
	}
	if e = VerifyArchive(ctx, staged); e != nil {
		return "", manifest, e
	}
	// Hard-link publication is atomic/no-replace; the output and staging share a
	// directory filesystem. The private staged name is removed only after sync.
	if e = os.Chmod(staged, 0644); e != nil {
		return "", manifest, e
	}
	if e = os.Link(staged, archivePath); e != nil {
		return "", manifest, e
	}
	dir, e := os.Open(output)
	if e != nil {
		return "", manifest, e
	}
	e = dir.Sync()
	dir.Close()
	if e != nil {
		return "", manifest, e
	}
	return archivePath, manifest, nil
}
func buildEnvironment(goos, arch string) []string {
	out := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOAMD64", "GOARM64", "GOTOOLCHAIN", "GOWORK":
			continue
		}
		out = append(out, entry)
	}
	return append(out, "GOOS="+goos, "GOARCH="+arch, "CGO_ENABLED=0", "GOFLAGS=", "GOAMD64=v1", "GOARM64=v8.0", "GOTOOLCHAIN=local", "GOWORK=off")
}
func inspect(path, name, goos, arch, flags string) (Artifact, error) {
	a := Artifact{Name: name, OS: goos, Arch: arch}
	info, e := buildinfo.ReadFile(path)
	if e != nil {
		return a, e
	}
	if info.Path != "github.com/yrashk/arm/cmd/arm" {
		return a, fmt.Errorf("unexpected executable module")
	}
	settings := map[string]string{}
	for _, s := range info.Settings {
		if _, ok := settings[s.Key]; ok {
			return a, fmt.Errorf("duplicate build setting")
		}
		settings[s.Key] = s.Value
	}
	if settings["GOOS"] != goos || settings["GOARCH"] != arch || settings["CGO_ENABLED"] != "0" || settings["-trimpath"] != "true" || (settings["-ldflags"] != "" && settings["-ldflags"] != flags) {
		return a, fmt.Errorf("binary platform/CGo/module/build settings mismatch: %s", name)
	}
	a.Go = info.GoVersion
	file, e := os.Open(path)
	if e != nil {
		return a, e
	}
	defer file.Close()
	stat, e := file.Stat()
	if e != nil || !stat.Mode().IsRegular() || stat.Size() < 1 || stat.Size() > maxBinary {
		return a, fmt.Errorf("invalid bounded executable")
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(file, maxBinary+1))
	if e != nil || n != stat.Size() {
		return a, fmt.Errorf("binary read changed")
	}
	a.Size = n
	a.SHA256 = hex.EncodeToString(h.Sum(nil))
	return a, nil
}
func writeArchive(ctx context.Context, destination, source, prefix string, m Manifest) error {
	f, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	gzipWriter := gzip.NewWriter(f)
	gzipWriter.Header.ModTime = time.Unix(0, 0)
	gzipWriter.Header.OS = 255
	writer := tar.NewWriter(gzipWriter)
	fail := func(err error) error { writer.Close(); gzipWriter.Close(); f.Close(); return err }
	epoch := time.Unix(0, 0).UTC()
	if e = writer.WriteHeader(&tar.Header{Name: prefix + "/", Mode: 0755, Typeflag: tar.TypeDir, ModTime: epoch, Format: tar.FormatUSTAR}); e != nil {
		return fail(e)
	}
	names := []string{"arm", "manifest.json", "README.txt"}
	for _, a := range m.Artifacts {
		names = append(names, a.Name)
	}
	for _, name := range names {
		if e = ctx.Err(); e != nil {
			return fail(e)
		}
		content, e := os.Open(filepath.Join(source, name))
		if e != nil {
			return fail(e)
		}
		info, e := content.Stat()
		if e != nil {
			content.Close()
			return fail(e)
		}
		mode := int64(0644)
		if name == "arm" || strings.HasPrefix(name, "arm-") {
			mode = 0755
		}
		if e = writer.WriteHeader(&tar.Header{Name: prefix + "/" + name, Mode: mode, Typeflag: tar.TypeReg, Size: info.Size(), ModTime: epoch, Format: tar.FormatUSTAR}); e == nil {
			_, e = io.Copy(writer, content)
		}
		content.Close()
		if e != nil {
			return fail(e)
		}
	}
	if e = writer.Close(); e != nil {
		return fail(e)
	}
	if e = gzipWriter.Close(); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	return f.Close()
}

const launcher = `#!/bin/sh
set -eu
case "$(uname -s)" in Darwin) arm_os=darwin;; Linux) arm_os=linux;; *) printf '%s\n' 'ARM bundle: unsupported operating system (need macOS or Linux)' >&2; exit 1;; esac
case "$(uname -m)" in arm64|aarch64) arm_arch=arm64;; x86_64|amd64) arm_arch=amd64;; *) printf '%s\n' 'ARM bundle: unsupported architecture (need arm64 or amd64)' >&2; exit 1;; esac
# Resolve only the launcher directory in a subshell; never change caller cwd.
case "$0" in */*) arm_self=$0;; *) arm_self=$(command -v "$0") || exit 1;; esac
case "$arm_self" in /*) ;; *) arm_self="$PWD/$arm_self";; esac
arm_dir=$(CDPATH= cd -P "$(dirname "$arm_self")" && pwd -P)
arm_binary="$arm_dir/arm-$arm_os-$arm_arch"
if ! test -x "$arm_binary"; then printf '%s\n' 'ARM bundle: matching companion missing; extract the complete bundle directory' >&2; exit 1; fi
exec "$arm_binary" "$@"
`

func buildPayloads(ctx context.Context, o Options, root, goPath, temp string) (Manifest, error) {
	var manifest Manifest
	var e error
	manifest = Manifest{Schema: 1, Version: o.Version, Protocol: o.Protocol, Integrity: "Local build integrity only; not an authenticated release manifest or permission to download/execute arbitrary bytes.", Artifacts: []Artifact{}}
	tag := "arm-bundle-v1:" + o.Version + ":" + fmt.Sprint(o.Protocol)
	flags := "-X=" + tagVariable + "=" + tag
	for _, platform := range platforms {
		if e = ctx.Err(); e != nil {
			return manifest, e
		}
		name := "arm-" + platform[0] + "-" + platform[1]
		path := filepath.Join(temp, name)
		if o.Log != nil {
			fmt.Fprintf(o.Log, "Building %s with %s\n", name, goPath)
		}
		cmd := exec.CommandContext(ctx, goPath, "build", "-trimpath", "-buildvcs=false", "-ldflags="+flags, "-o", path, "./cmd/arm")
		cmd.Dir = root
		cmd.Env = buildEnvironment(platform[0], platform[1])
		cmd.Stdout = o.Log
		cmd.Stderr = o.Log
		if e = cmd.Run(); e != nil {
			return manifest, fmt.Errorf("build %s: %w", name, e)
		}
		artifact, e := inspect(path, name, platform[0], platform[1], flags)
		if e != nil {
			return manifest, e
		}
		manifest.Artifacts = append(manifest.Artifacts, artifact)
	}
	return manifest, nil
}
