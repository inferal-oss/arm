package distribution

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/inferal-oss/arm/internal/companions"
)

// BuildCarrier stages a private source snapshot, builds four thin payloads with
// embedding disabled, then embeds their compressed bytes into ONE ARM for the
// selected supported platform (native by default).
// Generated assets never touch the caller checkout and cannot race make build.
func BuildCarrier(ctx context.Context, o Options) (string, companions.Manifest, error) {
	var manifest companions.Manifest
	goos, arch, e := carrierTarget(o)
	if e != nil {
		return "", manifest, e
	}
	if _, _, e := payloadBudget(o.Jobs); e != nil {
		return "", manifest, e
	}
	if !versionRE.MatchString(o.Version) || o.Protocol < 1 {
		return "", manifest, fmt.Errorf("invalid carrier version/protocol")
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
	final := filepath.Join(output, "arm")
	if _, e = os.Lstat(final); e == nil {
		return "", manifest, fmt.Errorf("carrier already exists; choose a fresh output directory")
	} else if !os.IsNotExist(e) {
		return "", manifest, e
	}
	temp, e := os.MkdirTemp(output, ".arm-carrier-")
	if e != nil {
		return "", manifest, e
	}
	defer os.RemoveAll(temp)
	source := filepath.Join(temp, "source")
	if e = copySource(ctx, root, source); e != nil {
		return "", manifest, e
	}
	payloadDir := filepath.Join(temp, "payloads")
	if e = os.Mkdir(payloadDir, 0700); e != nil {
		return "", manifest, e
	}
	thin, e := buildPayloads(ctx, o, source, goPath, payloadDir)
	if e != nil {
		return "", manifest, e
	}
	generated := filepath.Join(source, "internal", "companions", "generated")
	if e = os.Mkdir(generated, 0700); e != nil {
		return "", manifest, e
	}
	manifest = companions.Manifest{Schema: 1, Version: o.Version, Protocol: o.Protocol, Integrity: "Expected source-build version/protocol and verified Go platform/CGo/module plus byte digests; not release authentication. Actual target runtime is checked separately during provisioning.", Artifacts: []companions.Payload{}}
	for _, a := range thin.Artifacts {
		if e = ctx.Err(); e != nil {
			return "", manifest, e
		}
		path := filepath.Join(generated, a.Name+".gz")
		size, sum, e := compressPayload(path, filepath.Join(payloadDir, a.Name))
		if e != nil {
			return "", manifest, e
		}
		manifest.Artifacts = append(manifest.Artifacts, companions.Payload{Name: a.Name, OS: a.OS, Arch: a.Arch, Go: a.Go, Size: a.Size, SHA256: a.SHA256, CompressedSize: size, CompressedSHA256: sum})
	}
	if e = manifest.Validate(o.Version, o.Protocol); e != nil {
		return "", manifest, e
	}
	raw, e := json.MarshalIndent(manifest, "", "  ")
	if e != nil {
		return "", manifest, e
	}
	raw = append(raw, '\n')
	if e = os.WriteFile(filepath.Join(generated, "manifest.json"), raw, 0600); e != nil {
		return "", manifest, e
	}
	// Verify all four compressed streams before asking the compiler to embed.
	catalog, e := companions.Load(os.DirFS(filepath.Dir(generated)), o.Version, o.Protocol)
	if e != nil {
		return "", manifest, e
	}
	for _, p := range manifest.Artifacts {
		stream, _, e := catalog.Open(p.OS, p.Arch)
		if e != nil {
			return "", manifest, e
		}
		_, e = io.Copy(io.Discard, stream)
		ce := stream.Close()
		if e != nil {
			return "", manifest, e
		}
		if ce != nil {
			return "", manifest, ce
		}
	}
	staged := filepath.Join(temp, "arm")
	if e = buildCarrierExecutable(ctx, o, goPath, source, staged, goos, arch); e != nil {
		return "", manifest, e
	}
	file, e := os.Open(staged)
	if e != nil {
		return "", manifest, e
	}
	e = file.Sync()
	file.Close()
	if e != nil {
		return "", manifest, e
	}
	if e = os.Chmod(staged, 0755); e != nil {
		return "", manifest, e
	}
	if e = os.Link(staged, final); e != nil {
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
	return final, manifest, nil
}

func carrierTarget(o Options) (string, string, error) {
	goos, arch := o.CarrierOS, o.CarrierArch
	if (goos == "") != (arch == "") {
		return "", "", fmt.Errorf("carrier OS and architecture must be supplied together")
	}
	if goos == "" {
		goos, arch = runtime.GOOS, runtime.GOARCH
	}
	for _, platform := range platforms {
		if platform == [2]string{goos, arch} {
			return goos, arch, nil
		}
	}
	return "", "", fmt.Errorf("unsupported carrier platform %q/%q; use darwin/linux and amd64/arm64", goos, arch)
}

// This final compile is shared by native and explicitly targeted carriers. The
// inspected Go build metadata must match the chosen target before publication.
func buildCarrierExecutable(ctx context.Context, o Options, goPath, source, staged, goos, arch string) error {
	flags := identityFlags(o.Version, o.Protocol)
	if o.Log != nil {
		fmt.Fprintf(o.Log, "Building carrier %s/%s with four compressed thin payloads\n", goos, arch)
	}
	cmd := exec.CommandContext(ctx, goPath, "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-tags=armcarrier", "-ldflags="+flags, "-o", staged, "./cmd/arm")
	cmd.Dir = source
	cmd.Env = buildEnvironment(goos, arch, runtime.GOMAXPROCS(0))
	cmd.Stdout, cmd.Stderr = o.Log, o.Log
	if err := cmd.Run(); err != nil {
		return err
	}
	_, err := inspect(staged, "arm", goos, arch, flags)
	return err
}
func compressPayload(destination, source string) (int64, string, error) {
	input, e := os.Open(source)
	if e != nil {
		return 0, "", e
	}
	defer input.Close()
	output, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return 0, "", e
	}
	defer output.Close()
	h := sha256.New()
	writer := gzip.NewWriter(io.MultiWriter(output, h))
	writer.Header.ModTime = epochTime()
	writer.Header.OS = 255
	if _, e = io.Copy(writer, io.LimitReader(input, maxBinary+1)); e != nil {
		return 0, "", e
	}
	if e = writer.Close(); e != nil {
		return 0, "", e
	}
	if e = output.Sync(); e != nil {
		return 0, "", e
	}
	info, e := output.Stat()
	if e != nil {
		return 0, "", e
	}
	return info.Size(), hex.EncodeToString(h.Sum(nil)), nil
}

// Copy only module inputs used by cmd/arm, never caches/Git/toolchains or
// pre-generated payloads. Refuse symlinks/special files and excessive input.
func copySource(ctx context.Context, source, target string) error {
	if e := os.Mkdir(target, 0700); e != nil {
		return e
	}
	var total int64
	count := 0
	for _, entry := range []string{"go.mod", "go.sum", "cmd", "internal", "knowledge", "third_party/rsyncgo"} {
		e := filepath.WalkDir(filepath.Join(source, entry), func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if e := ctx.Err(); e != nil {
				return e
			}
			relative, e := filepath.Rel(source, path)
			if e != nil {
				return e
			}
			if relative == filepath.Join("internal", "companions", "generated") {
				return filepath.SkipDir
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("source snapshot refuses symlinks")
			}
			destination := filepath.Join(target, relative)
			if d.IsDir() {
				return os.MkdirAll(destination, 0700)
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() || info.Size() > 16<<20 {
				return fmt.Errorf("source input must be bounded regular files")
			}
			count++
			total += info.Size()
			if count > 8192 || total > 128<<20 {
				return fmt.Errorf("source snapshot exceeds bound")
			}
			in, e := os.Open(path)
			if e != nil {
				return e
			}
			out, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				in.Close()
				return e
			}
			n, copyErr := io.Copy(out, io.LimitReader(in, info.Size()+1))
			after, statErr := in.Stat()
			in.Close()
			closeErr := out.Close()
			if copyErr != nil || statErr != nil || closeErr != nil || n != info.Size() || !os.SameFile(info, after) || info.ModTime() != after.ModTime() || info.Size() != after.Size() {
				return fmt.Errorf("source changed during isolated snapshot: %s", strings.ReplaceAll(relative, "\n", "?"))
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	return nil
}
