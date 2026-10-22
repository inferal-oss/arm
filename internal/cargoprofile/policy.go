package cargoprofile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Roots are distinct intended namespaces, NOT evidence of ARM ownership. The
// later admitted storage helper must create/verify them before using this plan.
type Roots struct {
	Target  string `json:"target"`
	Build   string `json:"build"`
	Control string `json:"control"`
}

// Tools are concrete selectors supplied by a later admitted resolver. This
// layer does not verify executable identity, installed components or toolchains.
type Tools struct {
	Rustc   string
	Rustdoc string
}
type Plan struct {
	Policy       []byte   `json:"-"`
	PolicySHA256 string   `json:"policy_sha256"`
	Arguments    []string `json:"effective_arguments"`
	Metadata     []string `json:"metadata_arguments"`
	Environment  []string `json:"-"`
	Roots        Roots    `json:"roots"`
	Jobs         int      `json:"jobs"`
}

var protected = map[string]bool{
	"CARGO_HOME": true, "CARGO_BUILD_TARGET": true, "CARGO_BUILD_JOBS": true, "CARGO_TARGET_DIR": true, "CARGO_BUILD_TARGET_DIR": true, "CARGO_BUILD_BUILD_DIR": true,
	"RUSTC_BOOTSTRAP": true, "RUST_TARGET_PATH": true, "RUSTC": true, "RUSTDOC": true, "RUSTC_WRAPPER": true, "RUSTC_WORKSPACE_WRAPPER": true,
	"RUSTFLAGS": true, "CARGO_ENCODED_RUSTFLAGS": true, "RUSTDOCFLAGS": true, "CARGO_ENCODED_RUSTDOCFLAGS": true, "CARGO_BUILD_RUSTFLAGS": true, "CARGO_BUILD_RUSTDOCFLAGS": true,
	"CARGO_BUILD_RUSTC": true, "CARGO_BUILD_RUSTDOC": true, "CARGO_BUILD_RUSTC_WRAPPER": true, "CARGO_BUILD_RUSTC_WORKSPACE_WRAPPER": true,
	"MAKEFLAGS": true, "MFLAGS": true, "CARGO_MAKEFLAGS": true,
	"RUSTUP_AUTO_INSTALL": true, "RUSTUP_SKIP_UPDATE_CHECK": true, "RUSTUP_TERM_COLOR": true, "RUSTUP_LOG": true, "RUSTUP_TRACE_DIR": true,
}

func protectedKey(key string) bool {
	if protected[key] || strings.HasPrefix(key, "CARGO_UNSTABLE_") {
		return true
	}
	return strings.HasPrefix(key, "CARGO_TARGET_") && (strings.HasSuffix(key, "_RUSTFLAGS") || strings.HasSuffix(key, "_RUNNER") || strings.HasSuffix(key, "_LINKER"))
}
func controlledEnvironment(env []string) (map[string]string, error) {
	out := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("environment contains an invalid entry")
		}
		if _, ok = out[key]; ok && (protectedKey(key) || key == "HOME" || key == "PATH") {
			return nil, fmt.Errorf("duplicate protected Cargo environment key; provide one unambiguous value")
		}
		out[key] = value
	}
	for key, value := range out {
		if value == "" {
			continue
		}
		if key == "RUST_TARGET_PATH" {
			return nil, fmt.Errorf("custom target search paths are not regulated (value omitted)")
		}
		if key == "RUSTC_BOOTSTRAP" || strings.HasPrefix(key, "CARGO_UNSTABLE_") {
			return nil, fmt.Errorf("nightly/compiler bootstrap environment is not regulated")
		}
		if key == "CARGO_BUILD_TARGET" {
			return nil, fmt.Errorf("use explicit --target triples; environment target selection is not regulated yet")
		}
		if key == "RUSTFLAGS" || key == "CARGO_ENCODED_RUSTFLAGS" || key == "RUSTDOCFLAGS" || key == "CARGO_ENCODED_RUSTDOCFLAGS" || key == "RUSTC_WRAPPER" || key == "RUSTC_WORKSPACE_WRAPPER" || strings.HasPrefix(key, "CARGO_BUILD_RUST") || strings.HasPrefix(key, "CARGO_TARGET_") && (strings.HasSuffix(key, "_RUSTFLAGS") || strings.HasSuffix(key, "_LINKER") || strings.HasSuffix(key, "_RUNNER")) {
			return nil, fmt.Errorf("compiler/wrapper/target environment override is not regulated (value omitted)")
		}
	}
	return out, nil
}

// ResolveCargoHome uses explicit supplied environment, including relative
// CARGO_HOME resolved against invocation cwd. It never reads credentials.
func ResolveCargoHome(cwd string, env []string) (string, error) {
	if !filepath.IsAbs(cwd) || !validText(cwd) || len(cwd) > 4096 {
		return "", fmt.Errorf("absolute valid invocation cwd required")
	}
	values, e := controlledEnvironment(env)
	if e != nil {
		return "", e
	}
	home := values["CARGO_HOME"]
	if home == "" {
		if values["HOME"] == "" {
			return "", fmt.Errorf("HOME or explicit CARGO_HOME is required")
		}
		home = filepath.Join(values["HOME"], ".cargo")
	}
	if !filepath.IsAbs(home) {
		home = filepath.Join(cwd, home)
	}
	if !validText(home) || len(home) > 4096 {
		return "", fmt.Errorf("valid bounded Cargo home required")
	}
	return filepath.Clean(home), nil
}

// Compile returns a pure plan with an authoritative LAST policy, jobs and target
// option. It does not write files, validate real executables or launch a child.
func Compile(command Command, audit Audit, roots Roots, tools Tools, environment []string) (Plan, error) {
	var plan Plan
	canonical, err := Parse(command.Original, float64(command.Jobs))
	if err != nil || !reflect.DeepEqual(canonical, command) {
		return plan, fmt.Errorf("Cargo command differs from validated canonical input")
	}
	if command.Jobs < 1 || command.Subcommand == "" || audit.CWD == "" || audit.CargoHome == "" {
		return plan, fmt.Errorf("validated command and config evidence required")
	}
	for _, path := range []string{roots.Target, roots.Build, roots.Control, tools.Rustc, tools.Rustdoc} {
		if path == string(filepath.Separator) || !filepath.IsAbs(path) || filepath.Clean(path) != path || !validText(path) {
			return plan, fmt.Errorf("canonical absolute managed roots and verified compiler selectors required")
		}
	}
	paths := []string{roots.Target, roots.Build, roots.Control}
	for i, left := range paths {
		for j, right := range paths {
			if i != j && (left == right || strings.HasPrefix(left, right+string(filepath.Separator))) {
				return plan, fmt.Errorf("target, intermediate build and control roots must be distinct non-nested directories")
			}
		}
	}
	values, e := controlledEnvironment(environment)
	if e != nil {
		return plan, e
	}
	for _, key := range []string{"RUSTC", "RUSTDOC"} {
		expected := tools.Rustc
		if key == "RUSTDOC" {
			expected = tools.Rustdoc
		}
		if v := values[key]; v != "" && v != expected {
			return plan, fmt.Errorf("compiler replacement differs from selected verified tool (value omitted)")
		}
	}
	fixed := map[string]string{"RUSTUP_AUTO_INSTALL": "0", "RUSTUP_SKIP_UPDATE_CHECK": "1", "RUSTUP_TERM_COLOR": "never", "RUSTUP_LOG": "", "RUSTUP_TRACE_DIR": "", "CARGO_HOME": audit.CargoHome, "CARGO_BUILD_JOBS": strconv.Itoa(command.Jobs), "CARGO_TARGET_DIR": roots.Target, "CARGO_BUILD_TARGET_DIR": roots.Target, "CARGO_BUILD_BUILD_DIR": roots.Build, "RUSTC": tools.Rustc, "RUSTDOC": tools.Rustdoc, "RUSTC_BOOTSTRAP": "", "RUST_TARGET_PATH": "", "RUSTC_WRAPPER": "", "RUSTC_WORKSPACE_WRAPPER": "", "CARGO_BUILD_RUSTC": tools.Rustc, "CARGO_BUILD_RUSTDOC": tools.Rustdoc, "CARGO_BUILD_RUSTC_WRAPPER": "", "CARGO_BUILD_RUSTC_WORKSPACE_WRAPPER": "", "RUSTFLAGS": "", "CARGO_ENCODED_RUSTFLAGS": "", "RUSTDOCFLAGS": "", "CARGO_ENCODED_RUSTDOCFLAGS": "", "CARGO_BUILD_RUSTFLAGS": "", "CARGO_BUILD_RUSTDOCFLAGS": ""}
	for key := range values {
		if protectedKey(key) {
			delete(values, key)
		}
	}
	for key, value := range fixed {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	child := map[string]any{}
	for key, value := range fixed {
		if key == "CARGO_HOME" {
			continue
		}
		child[key] = map[string]any{"value": value, "force": true}
	}
	policy, e := toml.Marshal(map[string]any{"build": map[string]any{"jobs": command.Jobs, "target-dir": roots.Target, "build-dir": roots.Build, "rustc": tools.Rustc, "rustdoc": tools.Rustdoc, "rustc-wrapper": "", "rustc-workspace-wrapper": "", "rustflags": []string{}, "rustdocflags": []string{}}, "env": child})
	if e != nil {
		return plan, fmt.Errorf("private Cargo policy generation failed")
	}
	hash := sha256.Sum256(policy)
	path := filepath.Join(roots.Control, "policy.toml")
	args := append([]string{}, command.Arguments...)
	args = append(args, "--jobs", strconv.Itoa(command.Jobs), "--target-dir", roots.Target, "--config", path)
	metadata := append(command.MetadataArguments(), "--config", path)
	return Plan{Policy: policy, PolicySHA256: hex.EncodeToString(hash[:]), Arguments: args, Metadata: metadata, Environment: env, Roots: roots, Jobs: command.Jobs}, nil
}
