package cargoprofile

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const MaxToolBytes int64 = 256 << 20

type ExecutableEvidence struct {
	Path     string    `json:"path"`
	Device   uint64    `json:"device"`
	Inode    uint64    `json:"inode"`
	Owner    uint32    `json:"owner"`
	Mode     uint32    `json:"mode"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified_at"`
	SHA256   string    `json:"sha256"`
}
type Toolchain struct {
	Schema          int                 `json:"schema"`
	Selector        string              `json:"selector"`
	Selected        ExecutableEvidence  `json:"selected_executable"`
	Rustup          *ExecutableEvidence `json:"rustup,omitempty"`
	Selection       string              `json:"selection_method"`
	Toolchain       string              `json:"toolchain,omitempty"`
	Cargo           ExecutableEvidence  `json:"cargo"`
	Rustc           ExecutableEvidence  `json:"rustc"`
	Rustdoc         ExecutableEvidence  `json:"rustdoc"`
	CargoVersion    string              `json:"cargo_version"`
	RustcVersion    string              `json:"rustc_version"`
	RustdocVersion  string              `json:"rustdoc_version"`
	Host            string              `json:"host"`
	Sysroot         string              `json:"sysroot"`
	SysrootIdentity DirectoryEvidence   `json:"sysroot_identity"`
	BuiltinTargets  []string            `json:"builtin_targets"`
	CheckedTargets  []Target            `json:"checked_targets"`
	Detail          string              `json:"detail"`
}

// ResolveTools must run inside the already admitted supervised helper group.
// It performs bounded installed-tool queries and hashing, never installation,
// compilation, manager locking or ownership/adoption of Cargo/Rustup caches.
func ResolveTools(parent context.Context, command Command, audit Audit, environment []string) (Toolchain, error) {
	var result Toolchain
	canonical, e := Parse(command.Original, float64(command.Jobs))
	if e != nil || !reflect.DeepEqual(canonical, command) {
		return result, fmt.Errorf("validated canonical Cargo command required")
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	values, e := controlledEnvironment(environment)
	if e != nil {
		return result, e
	}
	env := queryEnvironment(values, audit.CargoHome)
	selected, e := resolveInstalledSelector(command.Executable, audit.CWD, values["PATH"])
	if e != nil {
		return result, e
	}
	original, e := snapshotExecutable(ctx, selected)
	if e != nil {
		return result, e
	}
	result = Toolchain{Schema: 1, Selector: command.Executable, Selected: original, Selection: "concrete-installed-siblings", Detail: "Installed executable hashes and sysroot-directory identity are observations, not a release signature or a snapshot of toolchain/shared libraries"}
	siblingRustup := filepath.Join(filepath.Dir(original.Path), "rustup")
	cargoInfo, e := os.Stat(original.Path)
	if e != nil {
		return Toolchain{}, fmt.Errorf("selected Cargo identity unavailable")
	}
	if candidate, err := os.Stat(siblingRustup); err == nil && os.SameFile(cargoInfo, candidate) {
		rustup, e := snapshotExecutable(ctx, siblingRustup)
		if e != nil {
			return Toolchain{}, e
		}
		result.Rustup = &rustup
		result.Selection = "installed-rustup-query"
		active, e := toolQuery(ctx, audit.CWD, env, rustup.Path, "show", "active-toolchain")
		if e != nil {
			return Toolchain{}, fmt.Errorf("existing active Rustup toolchain unavailable; automatic installation is disabled")
		}
		name, e := activeToolchain(active)
		if e != nil {
			return Toolchain{}, e
		}
		result.Toolchain = name
		paths := make([]string, 3)
		for i, name := range []string{"cargo", "rustc", "rustdoc"} {
			raw, e := toolQuery(ctx, audit.CWD, env, rustup.Path, "which", "--toolchain", result.Toolchain, name)
			if e != nil {
				return Toolchain{}, fmt.Errorf("existing selected toolchain component unavailable; installation is disabled")
			}
			paths[i], e = onePath(raw)
			if e != nil {
				return Toolchain{}, e
			}
		}
		result.Cargo, e = snapshotExecutable(ctx, paths[0])
		if e != nil {
			return Toolchain{}, e
		}
		result.Rustc, e = snapshotExecutable(ctx, paths[1])
		if e != nil {
			return Toolchain{}, e
		}
		result.Rustdoc, e = snapshotExecutable(ctx, paths[2])
		if e != nil {
			return Toolchain{}, e
		}
	} else {
		result.Cargo = original
		result.Rustc, e = snapshotExecutable(ctx, filepath.Join(filepath.Dir(original.Path), "rustc"))
		if e != nil {
			return Toolchain{}, fmt.Errorf("standalone Cargo requires existing native sibling rustc and rustdoc")
		}
		result.Rustdoc, e = snapshotExecutable(ctx, filepath.Join(filepath.Dir(original.Path), "rustdoc"))
		if e != nil {
			return Toolchain{}, fmt.Errorf("standalone Cargo requires existing native sibling rustc and rustdoc")
		}
	}
	for key, expected := range map[string]ExecutableEvidence{"RUSTC": result.Rustc, "RUSTDOC": result.Rustdoc} {
		if value := values[key]; value != "" {
			path, e := resolveInstalledSelector(value, audit.CWD, values["PATH"])
			if e != nil {
				return Toolchain{}, fmt.Errorf("compiler replacement is not supported")
			}
			actual, e := snapshotExecutable(ctx, path)
			if e != nil || actual.Device != expected.Device || actual.Inode != expected.Inode {
				return Toolchain{}, fmt.Errorf("compiler replacement differs from selected installed toolchain (value omitted)")
			}
		}
	}
	// Queries use the selected concrete tools, not a PATH compiler/proxy/wrapper.
	env = queryEnvironment(values, audit.CargoHome)
	env = replaceEnvironment(env, map[string]string{"RUSTC": result.Rustc.Path, "RUSTDOC": result.Rustdoc.Path})
	cargoVersion, e := toolQuery(ctx, audit.CWD, env, result.Cargo.Path, "-vV")
	if e != nil {
		return Toolchain{}, fmt.Errorf("installed Cargo version query failed")
	}
	result.CargoVersion, e = stableRelease(cargoVersion, "cargo", 91)
	if e != nil {
		return Toolchain{}, e
	}
	rustcVersion, e := toolQuery(ctx, audit.CWD, env, result.Rustc.Path, "-vV")
	if e != nil {
		return Toolchain{}, fmt.Errorf("installed rustc version query failed")
	}
	result.RustcVersion, e = stableRelease(rustcVersion, "rustc", 91)
	if e != nil {
		return Toolchain{}, e
	}
	result.Host = versionField(rustcVersion, "host")
	if !targetTriple(result.Host) {
		return Toolchain{}, fmt.Errorf("compiler host evidence unavailable")
	}
	if host := versionField(cargoVersion, "host"); host == "" || host != result.Host {
		return Toolchain{}, fmt.Errorf("Cargo/compiler host identity differs")
	}
	rustdocVersion, e := toolQuery(ctx, audit.CWD, env, result.Rustdoc.Path, "--version")
	if e != nil {
		return Toolchain{}, fmt.Errorf("installed rustdoc version query failed")
	}
	result.RustdocVersion, e = stableRelease(rustdocVersion, "rustdoc", 91)
	if e != nil {
		return Toolchain{}, e
	}
	// Initial scope deliberately requires matching stable release components.
	if result.RustdocVersion != result.RustcVersion || result.CargoVersion != result.RustcVersion {
		return Toolchain{}, fmt.Errorf("initial managed Cargo requires matching stable Cargo/rustc/rustdoc releases")
	}
	raw, e := toolQuery(ctx, audit.CWD, env, result.Rustc.Path, "--print", "sysroot")
	if e != nil {
		return Toolchain{}, fmt.Errorf("compiler sysroot query failed")
	}
	result.Sysroot, e = onePath(raw)
	if e != nil {
		return Toolchain{}, e
	}
	result.Sysroot, e = filepath.EvalSymlinks(result.Sysroot)
	if e != nil {
		return Toolchain{}, fmt.Errorf("compiler sysroot unavailable")
	}
	result.SysrootIdentity, e = directoryEvidence(result.Sysroot)
	if e != nil {
		return Toolchain{}, e
	}
	raw, e = toolQuery(ctx, audit.CWD, env, result.Rustc.Path, "--print", "target-list")
	if e != nil {
		return Toolchain{}, fmt.Errorf("compiler builtin-target query failed")
	}
	result.BuiltinTargets, e = parseBuiltinTargets(raw)
	if e != nil {
		return Toolchain{}, e
	}
	configured, e := audit.EffectiveTargets()
	if e != nil {
		return Toolchain{}, e
	}
	targets := append([]Target{}, configured...)
	for i := 1; i < len(command.Arguments); i++ {
		if command.Arguments[i] == "--target" {
			i++
			targets = append(targets, Target{Selector: command.Arguments[i], Origin: "explicit-command"})
		} else if _, ok := valueFlags[command.Arguments[i]]; ok {
			i++
		}
	}
	result.CheckedTargets, e = checkBuiltinTargets(targets, result.BuiltinTargets)
	if e != nil {
		return Toolchain{}, e
	}
	if e = result.RevalidateTools(ctx, audit, environment); e != nil {
		return Toolchain{}, e
	}
	return result, nil
}
func activeToolchain(raw []byte) (string, error) {
	text := strings.TrimSpace(string(raw))
	line, _, more := strings.Cut(text, "\n")
	if more {
		return "", fmt.Errorf("active toolchain query has ambiguous output")
	}
	name, _, _ := strings.Cut(line, " ")
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,255}$`).MatchString(name) {
		return "", fmt.Errorf("custom path/ambiguous Rustup toolchain selection is not supported")
	}
	return name, nil
}
func onePath(raw []byte) (string, error) {
	path := strings.TrimSpace(string(raw))
	if len(path) > 4096 || !validText(path) || strings.ContainsAny(path, "\r\n") || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("tool query did not return one canonical absolute path")
	}
	return path, nil
}
func versionField(raw []byte, key string) string {
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, key+": ") {
			return strings.TrimPrefix(line, key+": ")
		}
	}
	return ""
}
func stableRelease(raw []byte, name string, minimum int) (string, error) {
	first, _, _ := strings.Cut(strings.TrimSpace(string(raw)), "\n")
	match := regexp.MustCompile(`^` + name + ` (1\.([0-9]+)\.[0-9]+)(?: |$)`).FindStringSubmatch(first)
	if len(match) != 3 {
		return "", fmt.Errorf("a supported stable installed tool release is required")
	}
	minor, e := strconv.Atoi(match[2])
	if e != nil || minor < minimum {
		return "", fmt.Errorf("separate Cargo build-directory support requires stable tools 1.91 or newer")
	}
	return match[1], nil
}
func parseBuiltinTargets(raw []byte) ([]string, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("compiler builtin-target evidence is malformed")
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 1 || len(fields) > 4096 {
		return nil, fmt.Errorf("compiler builtin-target list exceeds bounds")
	}
	seen := map[string]bool{}
	for _, target := range fields {
		if !targetTriple(target) || seen[target] {
			return nil, fmt.Errorf("compiler builtin-target evidence is malformed")
		}
		seen[target] = true
	}
	sort.Strings(fields)
	return fields, nil
}
func checkBuiltinTargets(targets []Target, builtin []string) ([]Target, error) {
	if len(targets) > 192 {
		return nil, fmt.Errorf("selected target count exceeds bound")
	}
	set := map[string]bool{}
	for _, target := range builtin {
		set[target] = true
	}
	out := append([]Target{}, targets...)
	for _, target := range out {
		if !set[target.Selector] {
			return nil, fmt.Errorf("configured/explicit target is not a verified compiler builtin; custom targets are unsupported (value omitted)")
		}
	}
	return out, nil
}
func queryEnvironment(values map[string]string, cargoHome string) []string {
	out := []string{}
	for key, value := range values {
		if protectedKey(key) || strings.HasPrefix(key, "RUSTUP_LOG") || key == "RUSTUP_TRACE_DIR" {
			continue
		}
		out = append(out, key+"="+value)
	}
	return replaceEnvironment(out, map[string]string{"CARGO_HOME": cargoHome, "RUSTUP_AUTO_INSTALL": "0", "RUSTUP_SKIP_UPDATE_CHECK": "1", "RUSTUP_TERM_COLOR": "never", "CARGO_NET_OFFLINE": "true", "RUSTC_WRAPPER": "", "RUSTC_WORKSPACE_WRAPPER": "", "RUST_TARGET_PATH": "", "RUSTC_BOOTSTRAP": ""})
}
func replaceEnvironment(env []string, values map[string]string) []string {
	all := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		all[key] = value
	}
	for key, value := range values {
		all[key] = value
	}
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+all[key])
	}
	return out
}
func resolveInstalledSelector(selector, cwd, path string) (string, error) {
	if len(selector) > 4096 || !validText(selector) {
		return "", fmt.Errorf("installed tool selector is invalid")
	}
	if strings.ContainsRune(selector, filepath.Separator) {
		if !filepath.IsAbs(selector) {
			selector = filepath.Join(cwd, selector)
		}
		return filepath.Clean(selector), nil
	}
	dirs := filepath.SplitList(path)
	if len(dirs) > 256 {
		return "", fmt.Errorf("tool PATH count exceeds bound")
	}
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) || !validText(dir) {
			return "", fmt.Errorf("relative/empty tool PATH entries are unsupported")
		}
		candidate := filepath.Join(dir, selector)
		info, e := os.Stat(candidate)
		if e == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return candidate, nil
		}
		if e != nil && !os.IsNotExist(e) {
			return "", fmt.Errorf("tool search namespace unavailable")
		}
	}
	return "", fmt.Errorf("installed tool unavailable; no download attempted")
}
func directoryEvidence(path string) (DirectoryEvidence, error) {
	f, e := openConfigNoFollow(path)
	if e != nil {
		return DirectoryEvidence{}, fmt.Errorf("tool directory must have a physical native identity")
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.IsDir() {
		return DirectoryEvidence{}, fmt.Errorf("tool directory unavailable")
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return DirectoryEvidence{}, fmt.Errorf("tool directory native identity unavailable")
	}
	return DirectoryEvidence{Device: uint64(s.Dev), Inode: s.Ino, Owner: s.Uid, Mode: uint32(s.Mode)}, nil
}
func snapshotExecutable(ctx context.Context, path string) (ExecutableEvidence, error) {
	var out ExecutableEvidence
	physical, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(physical) || len(physical) > 4096 || !validText(physical) {
		return out, fmt.Errorf("installed executable physical path unavailable")
	}
	f, e := openConfigNoFollow(physical)
	if e != nil {
		return out, fmt.Errorf("installed executable unavailable")
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0111 == 0 || before.Mode().Perm()&0022 != 0 || before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || before.Size() < 1 || before.Size() > MaxToolBytes {
		return out, fmt.Errorf("installed tool must be bounded regular executable, not writable by another account")
	}
	s, ok := before.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != 0 && s.Uid != uint32(os.Geteuid()) {
		return out, fmt.Errorf("installed tool owner/native identity unsupported")
	}
	if !nativeExecutable(f) {
		return out, fmt.Errorf("installed tool must be a native ELF/Mach-O executable; scripts/wrappers are unsupported")
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		return out, e
	}
	hash := sha256.New()
	buffer := make([]byte, 256<<10)
	var size int64
	for {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		n, re := f.Read(buffer)
		size += int64(n)
		if size > MaxToolBytes {
			return out, fmt.Errorf("tool exceeds executable bound")
		}
		hash.Write(buffer[:n])
		if re == io.EOF {
			break
		}
		if re != nil {
			return out, fmt.Errorf("installed tool read failed")
		}
	}
	after, e := f.Stat()
	named, e2 := openConfigNoFollow(physical)
	if e2 != nil {
		return out, fmt.Errorf("installed tool changed during inspection")
	}
	defer named.Close()
	namedInfo, e3 := named.Stat()
	if e != nil || e3 != nil || !os.SameFile(before, after) || !os.SameFile(before, namedInfo) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() || before.Mode() != namedInfo.Mode() || !sameExecutableMetadata(before, after) || !sameExecutableMetadata(before, namedInfo) || size != after.Size() {
		return out, fmt.Errorf("installed executable changed during inspection")
	}
	return ExecutableEvidence{Path: physical, Device: uint64(s.Dev), Inode: s.Ino, Owner: s.Uid, Mode: uint32(s.Mode), Size: size, Modified: before.ModTime().UTC(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}
func sameExecutableMetadata(a, b os.FileInfo) bool {
	x, xok := a.Sys().(*syscall.Stat_t)
	y, yok := b.Sys().(*syscall.Stat_t)
	return xok && yok && x.Uid == y.Uid && x.Gid == y.Gid && x.Mode == y.Mode
}
func nativeExecutable(f *os.File) bool {
	if runtime.GOOS == "linux" {
		x, e := elf.NewFile(f)
		if e != nil {
			return false
		}
		return (x.Type == elf.ET_EXEC || x.Type == elf.ET_DYN) && (runtime.GOARCH == "arm64" && x.Machine == elf.EM_AARCH64 || runtime.GOARCH == "amd64" && x.Machine == elf.EM_X86_64)
	}
	x, e := macho.NewFile(f)
	if e != nil {
		return false
	}
	return x.Type == macho.TypeExec && (runtime.GOARCH == "arm64" && x.Cpu == macho.CpuArm64 || runtime.GOARCH == "amd64" && x.Cpu == macho.CpuAmd64)
}

type queryCapture struct {
	bytes    []byte
	limit    int
	overflow bool
}

func (c *queryCapture) Write(p []byte) (int, error) {
	if len(p) > c.limit-len(c.bytes) {
		c.overflow = true
		return 0, fmt.Errorf("tool output limit")
	}
	c.bytes = append(c.bytes, p...)
	return len(p), nil
}
func toolQuery(parent context.Context, cwd string, env []string, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	out, diagnostic := &queryCapture{limit: 1 << 20}, &queryCapture{limit: 64 << 10}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = out, diagnostic
	cmd.WaitDelay = 250 * time.Millisecond
	e := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("installed tool query deadline exceeded")
	}
	if e != nil || out.overflow || diagnostic.overflow {
		return nil, fmt.Errorf("installed tool query failed or exceeded output bound (diagnostics omitted)")
	}
	return out.bytes, nil
}
func (t Toolchain) revalidateFiles(ctx context.Context) error {
	files := []ExecutableEvidence{t.Selected, t.Cargo, t.Rustc, t.Rustdoc}
	if t.Rustup != nil {
		files = append(files, *t.Rustup)
	}
	for _, expected := range files {
		actual, e := snapshotExecutable(ctx, expected.Path)
		if e != nil || !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("selected tool identity/content changed; compilation not authorized")
		}
	}
	actual, e := directoryEvidence(t.Sysroot)
	if e != nil || actual != t.SysrootIdentity {
		return fmt.Errorf("compiler sysroot directory identity changed")
	}
	return nil
}

// RevalidateTools rechecks installed files and current selector/toolchain without
// restarting compilation. Like ResolveTools it belongs inside the admitted group.
func (t Toolchain) RevalidateTools(parent context.Context, audit Audit, environment []string) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if e := t.revalidateFiles(ctx); e != nil {
		return e
	}
	values, e := controlledEnvironment(environment)
	if e != nil {
		return e
	}
	path, e := resolveInstalledSelector(t.Selector, audit.CWD, values["PATH"])
	if e != nil {
		return e
	}
	selected, e := snapshotExecutable(ctx, path)
	if e != nil || !reflect.DeepEqual(selected, t.Selected) {
		return fmt.Errorf("Cargo executable selector changed")
	}
	if t.Rustup != nil {
		raw, e := toolQuery(ctx, audit.CWD, queryEnvironment(values, audit.CargoHome), t.Rustup.Path, "show", "active-toolchain")
		if e != nil {
			return e
		}
		name, e := activeToolchain(raw)
		if e != nil || name != t.Toolchain {
			return fmt.Errorf("active Rustup toolchain changed")
		}
		for _, item := range []struct {
			name string
			file ExecutableEvidence
		}{{"cargo", t.Cargo}, {"rustc", t.Rustc}, {"rustdoc", t.Rustdoc}} {
			raw, e := toolQuery(ctx, audit.CWD, queryEnvironment(values, audit.CargoHome), t.Rustup.Path, "which", "--toolchain", t.Toolchain, item.name)
			if e != nil {
				return e
			}
			path, e := onePath(raw)
			if e != nil {
				return e
			}
			actual, e := snapshotExecutable(ctx, path)
			if e != nil || !reflect.DeepEqual(actual, item.file) {
				return fmt.Errorf("selected Rustup component changed")
			}
		}
	}
	return audit.Revalidate(ctx)
}
