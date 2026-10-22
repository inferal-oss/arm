package cargoprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sys/unix"
)

const MaxConfigBytes = 1 << 20
const MaxTotalConfigBytes = 8 << 20

// FileEvidence deliberately excludes complete TOML and arbitrary environment
// values. Even ignored/absent config slots are recorded for gate revalidation.
type FileEvidence struct {
	Path    string `json:"path"`
	Present bool   `json:"present"`
	Ignored bool   `json:"ignored"`
	Device  uint64 `json:"device,omitempty"`
	Inode   uint64 `json:"inode,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Owner   uint32 `json:"owner,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}
type Setting struct {
	Targets     []string `json:"targets,omitempty"`
	TargetArray bool     `json:"target_array,omitempty"`
	Key         string   `json:"key"`
	Origin      string   `json:"origin"`
	Jobs        int64    `json:"jobs,omitempty"`
	Path        string   `json:"path,omitempty"`
}
type DirectoryEvidence struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Owner  uint32 `json:"owner"`
	Mode   uint32 `json:"mode"`
}
type Audit struct {
	CWDIdentity DirectoryEvidence `json:"cwd_identity"`
	CWD         string            `json:"physical_cwd"`
	CargoHome   string            `json:"cargo_home"`
	Files       []FileEvidence    `json:"files"`
	Settings    []Setting         `json:"regulated_settings"`
}

// Discover is bounded, read-only and has no Cargo subprocess/global-config
// writes. It conservatively refuses includes and symlinked config namespaces;
// neither unsupported graph expansion nor a path is cleanup authority.
func Discover(ctx context.Context, cwd, cargoHome string, environment []string) (Audit, error) {
	var a Audit
	if !filepath.IsAbs(cwd) || !filepath.IsAbs(cargoHome) || len(cwd) > 4096 || len(cargoHome) > 4096 || !validText(cwd) || !validText(cargoHome) {
		return a, fmt.Errorf("absolute invocation cwd and resolved Cargo home required")
	}
	physical, e := filepath.EvalSymlinks(cwd)
	if e != nil {
		return a, fmt.Errorf("invocation cwd unavailable")
	}
	info, e := os.Stat(physical)
	if e != nil || !info.IsDir() || !validText(physical) {
		return a, fmt.Errorf("invocation cwd must be a directory")
	}
	native, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return a, fmt.Errorf("invocation cwd native identity unavailable")
	}
	a = Audit{CWD: physical, CWDIdentity: DirectoryEvidence{Device: uint64(native.Dev), Inode: native.Ino, Owner: native.Uid, Mode: uint32(native.Mode)}, CargoHome: filepath.Clean(cargoHome), Files: []FileEvidence{}, Settings: []Setting{}}
	if _, e = controlledEnvironment(environment); e != nil {
		return Audit{}, e
	}
	// Merge home first, then root ancestors to nearest cwd. Preserve each origin
	// in precedence order; policy overrides selected jobs/output values later.
	directories := []string{a.CargoHome}
	var ancestors []string
	for p := physical; ; p = filepath.Dir(p) {
		ancestors = append(ancestors, filepath.Join(p, ".cargo"))
		if filepath.Dir(p) == p {
			break
		}
		if len(ancestors) > 128 {
			return Audit{}, fmt.Errorf("Cargo ancestor depth exceeds supported bound")
		}
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		directories = append(directories, ancestors[i])
	}
	seen := map[string]bool{}
	total := 0
	deadline := time.Now().Add(2 * time.Second)
	for _, dir := range directories {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		if e = ctx.Err(); e != nil {
			return Audit{}, e
		}
		if time.Now().After(deadline) {
			return Audit{}, fmt.Errorf("Cargo config audit deadline exceeded")
		}
		legacy, e := readConfigSlot(filepath.Join(dir, "config"))
		if e != nil {
			return Audit{}, e
		}
		modern, e := readConfigSlot(filepath.Join(dir, "config.toml"))
		if e != nil {
			return Audit{}, e
		}
		if legacy.evidence.Present {
			modern.evidence.Ignored = modern.evidence.Present
		}
		a.Files = append(a.Files, legacy.evidence, modern.evidence)
		if len(a.Files) > 260 {
			return Audit{}, fmt.Errorf("Cargo config count exceeds supported bound")
		}
		for _, file := range []configSlot{legacy, modern} {
			total += len(file.raw)
			if total > MaxTotalConfigBytes {
				return Audit{}, fmt.Errorf("Cargo config byte limit exceeded")
			}
			if !file.evidence.Present || file.evidence.Ignored {
				continue
			}
			var data map[string]any
			if !utf8.Valid(file.raw) {
				return Audit{}, fmt.Errorf("Cargo configuration must be valid UTF-8 (contents omitted)")
			}
			if e = toml.Unmarshal(file.raw, &data); e != nil {
				return Audit{}, fmt.Errorf("Cargo configuration is invalid TOML (contents omitted)")
			}
			settings, e := auditConfig(data, file.evidence.Path)
			if e != nil {
				return Audit{}, e
			}
			a.Settings = append(a.Settings, settings...)
		}
	}
	if _, e = a.EffectiveTargets(); e != nil {
		return Audit{}, e
	}
	return a, nil
}

type configSlot struct {
	evidence FileEvidence
	raw      []byte
}

func readConfigSlot(path string) (configSlot, error) {
	slot := configSlot{evidence: FileEvidence{Path: path}}
	f, e := openConfigNoFollow(path)
	if e == unix.ENOENT {
		return slot, nil
	}
	if e != nil {
		return slot, fmt.Errorf("Cargo config namespace must contain regular physical paths; symlinks/unavailable paths are unsupported")
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil || !before.Mode().IsRegular() || before.Size() > MaxConfigBytes {
		return slot, fmt.Errorf("Cargo config must be a bounded regular file")
	}
	raw, e := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if e != nil || len(raw) > MaxConfigBytes {
		return slot, fmt.Errorf("Cargo config read exceeds bounds")
	}
	after, e := f.Stat()
	namedFile, ne := openConfigNoFollow(path)
	var named os.FileInfo
	if ne == nil {
		named, ne = namedFile.Stat()
		namedFile.Close()
	}
	if e != nil || ne != nil || !os.SameFile(before, after) || !os.SameFile(before, named) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return slot, fmt.Errorf("Cargo config changed during audit")
	}
	st, ok := after.Sys().(*syscall.Stat_t)
	if !ok {
		return slot, fmt.Errorf("Cargo config native identity unavailable")
	}
	h := sha256.Sum256(raw)
	slot.evidence = FileEvidence{Path: path, Present: true, Device: uint64(st.Dev), Inode: st.Ino, Size: int64(len(raw)), Owner: st.Uid, Mode: uint32(st.Mode), SHA256: hex.EncodeToString(h[:])}
	slot.raw = raw
	return slot, nil
}
func openConfigNoFollow(path string) (*os.File, error) {
	fd, e := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/")
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}
func auditConfig(data map[string]any, origin string) ([]Setting, error) {
	out := []Setting{}
	if _, ok := data["include"]; ok {
		return nil, fmt.Errorf("Cargo config includes require a version-aware graph and are not supported yet")
	}
	if _, ok := data["unstable"]; ok {
		return nil, fmt.Errorf("nightly Cargo configuration is not regulated")
	}
	build, ok := data["build"].(map[string]any)
	if _, exists := data["build"]; exists && !ok {
		return nil, fmt.Errorf("Cargo build configuration must be a table")
	}
	for key, value := range build {
		switch key {
		case "jobs":
			n, ok := value.(int64)
			if !ok || n == 0 || n > 65536 || n < -65536 {
				return nil, fmt.Errorf("Cargo build.jobs must be a bounded nonzero integer")
			}
			out = append(out, Setting{Key: "build.jobs", Origin: origin, Jobs: n})
		case "target-dir", "build-dir":
			path, ok := value.(string)
			if !ok || !validText(path) || len(path) > 4096 {
				return nil, fmt.Errorf("Cargo output directory setting must be a bounded string")
			}
			if strings.ContainsAny(path, "{}") {
				return nil, fmt.Errorf("Cargo directory templates require version-aware expansion and are not supported yet")
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(filepath.Dir(filepath.Dir(origin)), path)
			}
			out = append(out, Setting{Key: "build." + key, Origin: origin, Path: filepath.Clean(path)})
		case "rustc", "rustdoc", "rustc-wrapper", "rustc-workspace-wrapper", "rustflags", "rustdocflags":
			if !emptyControl(value) {
				return nil, fmt.Errorf("Cargo build.%s is not supported in managed compilation; remove the override or use generic supervision", key)
			}
		case "target":
			setting := Setting{Key: "build.target", Origin: origin, Targets: []string{}}
			switch v := value.(type) {
			case string:
				if !targetTriple(v) {
					return nil, fmt.Errorf("configured target selector has unsupported syntax")
				}
				setting.Targets = append(setting.Targets, v)
			case []any:
				if len(v) > 64 {
					return nil, fmt.Errorf("configured target array exceeds supported bound")
				}
				setting.TargetArray = true
				for _, item := range v {
					target, ok := item.(string)
					if !ok || !targetTriple(target) {
						return nil, fmt.Errorf("configured target selector has unsupported syntax")
					}
					setting.Targets = append(setting.Targets, target)
				}
			default:
				return nil, fmt.Errorf("configured target has unsupported type")
			}
			out = append(out, setting)
		}
	}
	if _, exists := data["target"]; exists {
		if _, ok := data["target"].(map[string]any); !ok {
			return nil, fmt.Errorf("Cargo target configuration must be a table")
		}
	}
	if targets, ok := data["target"].(map[string]any); ok {
		for _, value := range targets {
			table, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("Cargo target configuration must be tables")
			}
			for _, key := range []string{"rustflags", "rustdocflags", "linker", "runner"} {
				if value, ok := table[key]; ok && !emptyControl(value) {
					return nil, fmt.Errorf("target-specific Cargo compiler/linker/runner overrides are not supported")
				}
			}
		}
	}
	if _, exists := data["env"]; exists {
		if _, ok := data["env"].(map[string]any); !ok {
			return nil, fmt.Errorf("Cargo env configuration must be a table")
		}
	}
	if env, ok := data["env"].(map[string]any); ok {
		for key, value := range env {
			if protectedKey(key) {
				if key == "CARGO_BUILD_TARGET" {
					return nil, fmt.Errorf("use modeled build.target configuration or explicit --target; config environment target selection is not regulated")
				}
				if strings.HasPrefix(key, "CARGO_TARGET_") && (strings.HasSuffix(key, "_RUSTFLAGS") || strings.HasSuffix(key, "_LINKER") || strings.HasSuffix(key, "_RUNNER")) {
					return nil, fmt.Errorf("target-specific config environment overrides are not regulated (value omitted)")
				}
				if key == "RUSTC_BOOTSTRAP" || strings.HasPrefix(key, "CARGO_UNSTABLE_") {
					return nil, fmt.Errorf("nightly/compiler bootstrap configuration environment is not regulated")
				}
				if key == "RUST_TARGET_PATH" {
					return nil, fmt.Errorf("custom target search paths in Cargo env are unsupported (value omitted)")
				}
				if key == "CARGO_HOME" {
					return nil, fmt.Errorf("Cargo does not support CARGO_HOME in config env; select an explicit invocation Cargo home")
				}
				if key == "MAKEFLAGS" || key == "MFLAGS" || key == "CARGO_MAKEFLAGS" {
					return nil, fmt.Errorf("Cargo config must not replace the fresh inherited jobserver")
				}
				scalar := value
				switch v := value.(type) {
				case string:
				case map[string]any:
					if _, ok := v["value"].(string); !ok {
						return nil, fmt.Errorf("protected Cargo env setting requires a string value")
					}
					for key, value := range v {
						if key != "value" && key != "force" && key != "relative" {
							return nil, fmt.Errorf("protected Cargo env table has unsupported fields")
						}
						if key == "force" || key == "relative" {
							if _, ok := value.(bool); !ok {
								return nil, fmt.Errorf("protected Cargo env flags require booleans")
							}
						}
					}
				default:
					return nil, fmt.Errorf("protected Cargo env setting has unsupported type")
				}
				if table, ok := value.(map[string]any); ok {
					scalar = table["value"]
				}
				if key == "RUSTC" || key == "RUSTDOC" || strings.HasPrefix(key, "CARGO_BUILD_RUST") || strings.Contains(key, "WRAPPER") || strings.Contains(key, "RUSTFLAGS") || strings.Contains(key, "RUSTDOCFLAGS") || strings.HasSuffix(key, "_LINKER") || strings.HasSuffix(key, "_RUNNER") {
					if !emptyControl(scalar) {
						return nil, fmt.Errorf("Cargo environment compiler/wrapper override is unsupported (value omitted)")
					}
				}
				out = append(out, Setting{Key: "env." + key, Origin: origin})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
func emptyControl(v any) bool {
	if s, ok := v.(string); ok {
		return s == ""
	}
	if a, ok := v.([]any); ok {
		return len(a) == 0
	}
	return false
}

// Revalidate compares all native/hash/absence evidence, without rerunning a tool
// or rewriting immutable admission history. It does not freeze project files.
func (a Audit) Revalidate(ctx context.Context) error {
	physical, e := filepath.EvalSymlinks(a.CWD)
	if e != nil || physical != a.CWD {
		return fmt.Errorf("Cargo invocation directory changed")
	}
	st, e := os.Stat(a.CWD)
	if e != nil {
		return fmt.Errorf("Cargo invocation directory unavailable")
	}
	native, ok := st.Sys().(*syscall.Stat_t)
	if !ok || a.CWDIdentity != (DirectoryEvidence{Device: uint64(native.Dev), Inode: native.Ino, Owner: native.Uid, Mode: uint32(native.Mode)}) {
		return fmt.Errorf("Cargo invocation directory native identity changed")
	}
	for _, before := range a.Files {
		if e := ctx.Err(); e != nil {
			return e
		}
		after, e := readConfigSlot(before.Path)
		if e != nil {
			return e
		}
		after.evidence.Ignored = before.Ignored
		if !reflect.DeepEqual(before, after.evidence) {
			return fmt.Errorf("Cargo config identity/content/absence changed; prepare a fresh invocation")
		}
	}
	return nil
}

// Effective returns the last supported declaration according to discovered
// Cargo home/ancestor precedence. For build.target use EffectiveTargets for
// merged arrays and per-selector origins. Returned target slices are copied.
func (a Audit) Effective(key string) (Setting, bool) {
	for i := len(a.Settings) - 1; i >= 0; i-- {
		if a.Settings[i].Key == key {
			setting := a.Settings[i]
			setting.Targets = append([]string{}, setting.Targets...)
			return setting, true
		}
	}
	return Setting{}, false
}

// Target preserves each effective selector's configuration origin. Membership
// in the selected rustc built-in target list is a later admitted resolver gate.
type Target struct {
	Selector string `json:"selector"`
	Origin   string `json:"origin"`
}

// EffectiveTargets implements supported homogeneous Cargo scalar/array merge:
// deeper scalars replace; arrays concatenate home-to-nearest with provenance.
// Mixed scalar/array settings are refused rather than inventing Cargo semantics.
func (a Audit) EffectiveTargets() ([]Target, error) {
	out := []Target{}
	seen := false
	array := false
	for _, setting := range a.Settings {
		if setting.Key != "build.target" {
			continue
		}
		if seen && array != setting.TargetArray {
			return nil, fmt.Errorf("mixed scalar/array build.target configuration is not supported; use one consistent form")
		}
		seen = true
		array = setting.TargetArray
		if !array {
			out = nil
		}
		if len(setting.Targets) > 64 || !array && len(setting.Targets) != 1 {
			return nil, fmt.Errorf("configured target evidence exceeds supported form")
		}
		for _, selector := range setting.Targets {
			if !targetTriple(selector) {
				return nil, fmt.Errorf("configured target evidence has unsupported selector")
			}
			out = append(out, Target{Selector: selector, Origin: setting.Origin})
			if len(out) > 64 {
				return nil, fmt.Errorf("effective target array exceeds supported bound")
			}
		}
	}
	return out, nil
}
