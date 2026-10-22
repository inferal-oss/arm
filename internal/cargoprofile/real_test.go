package cargoprofile

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Set all three concrete installed tools explicitly for mandatory real gates.
// Tests never resolve/install toolchains or read a user's Cargo home/cache.
func TestRealCargoDiscoveryPolicyAndNoRun(t *testing.T) {
	cargo, rustc, rustdoc := os.Getenv("ARM_TEST_CARGO"), os.Getenv("ARM_TEST_RUSTC"), os.Getenv("ARM_TEST_RUSTDOC")
	if cargo == "" || rustc == "" || rustdoc == "" {
		t.Skip("explicit concrete ARM_TEST_CARGO/RUSTC/RUSTDOC required for real offline fixture")
	}
	for _, tool := range []string{cargo, rustc, rustdoc} {
		if !filepath.IsAbs(tool) {
			t.Fatal("absolute existing tool required")
		}
		if _, e := os.Stat(tool); e != nil {
			t.Fatal(e)
		}
	}
	// A named custom target is resolved through RUST_TARGET_PATH by actual
	// stable rustc. A deliberately invalid spec proves lookup without compiling
	// or enabling nightly/bootstrap/installing a toolchain.
	customRoot := fixtureRoot(t)
	write(t, filepath.Join(customRoot, "arm-private-target.json"), "{}")
	probe := func(search bool) []byte {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, rustc, "--print", "cfg", "--target", "arm-private-target")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + customRoot}
		if search {
			cmd.Env = append(cmd.Env, "RUST_TARGET_PATH="+customRoot)
		}
		raw, e := cmd.CombinedOutput()
		if e == nil {
			t.Fatal("invalid custom target unexpectedly accepted")
		}
		return raw
	}
	without, with := probe(false), probe(true)
	if string(without) == string(with) || !strings.Contains(string(with), "llvm-target") && !strings.Contains(string(with), "custom targets are unstable") {
		t.Fatalf("rustc custom search differential missing: %s / %s", without, with)
	}
	root := fixtureRoot(t)
	workspace := filepath.Join(root, "workspace")
	member := filepath.Join(workspace, "member")
	home := filepath.Join(root, "cargo-home")
	evidence := filepath.Join(root, "observed")
	write(t, filepath.Join(workspace, "Cargo.toml"), "[workspace]\nmembers=['member']\nresolver='2'\n")
	write(t, filepath.Join(member, "Cargo.toml"), "[package]\nname='arm_cargo_fixture'\nversion='0.1.0'\nedition='2021'\n")
	write(t, filepath.Join(member, "src", "lib.rs"), "#[test] fn never_execute() { panic!(\"ARM_TEST_MUST_NOT_RUN\"); }\npub fn value()->u32{1}\n")
	write(t, filepath.Join(member, "build.rs"), `fn main(){
 let keys=["NUM_JOBS","CARGO_BUILD_JOBS","CARGO_TARGET_DIR","CARGO_BUILD_BUILD_DIR","OUT_DIR","CARGO_MAKEFLAGS"];
 let observed=keys.iter().map(|k|format!("{}={}\n",k,std::env::var(k).unwrap_or_default())).collect::<String>();
 std::fs::write(std::env::var("ARM_EVIDENCE").unwrap(),observed).unwrap();
}`)
	write(t, filepath.Join(home, "config.toml"), "[build]\njobs=4\ntarget-dir='home-target'\nbuild-dir='home-build'\n")
	write(t, filepath.Join(workspace, ".cargo", "config.toml"), "[build]\njobs=2\ntarget-dir='workspace-target'\nbuild-dir='workspace-build'\n")
	write(t, filepath.Join(member, ".cargo", "config"), "[build]\njobs=3\ntarget-dir='member-target'\nbuild-dir='member-build'\n[env]\nCARGO_BUILD_JOBS={value='99',force=true}\nCARGO_TARGET_DIR={value='/unowned-target',force=true}\n")
	write(t, filepath.Join(member, ".cargo", "config.toml"), "[build]\njobs=88\ntarget-dir='ignored-target'\n")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "CARGO_HOME=" + home, "RUSTC=" + rustc, "RUSTDOC=" + rustdoc, "CARGO_NET_OFFLINE=true", "RUSTUP_AUTO_INSTALL=0", "RUSTUP_SKIP_UPDATE_CHECK=1", "ARM_EVIDENCE=" + evidence}
	run := func(cwd string, env []string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cargo, args...)
		cmd.Dir = cwd
		cmd.Env = env
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("real Cargo %v failed: %v\n%s", args, e, out)
		}
		if len(out) > 8<<20 {
			t.Fatal("tool output exceeds fixture bound")
		}
		return out
	}
	t.Logf("installed tool: %s", strings.TrimSpace(string(run(root, env, "--version"))))
	metadata := func(cwd string, args ...string) map[string]any {
		t.Helper()
		argv := append([]string{"metadata", "--no-deps", "--offline", "--format-version", "1"}, args...)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cargo, argv...)
		cmd.Dir = cwd
		cmd.Env = env
		out, e := cmd.Output()
		if e != nil {
			t.Fatal(e)
		}
		var result map[string]any
		if e = json.Unmarshal(out, &result); e != nil {
			t.Fatal(e)
		}
		return result
	}
	for _, item := range []struct{ cwd, target, build string }{{workspace, "workspace-target", "workspace-build"}, {member, "member-target", "member-build"}} {
		actual := metadata(item.cwd)
		if actual["target_directory"] != filepath.Join(item.cwd, item.target) || actual["build_directory"] != filepath.Join(item.cwd, item.build) {
			t.Fatal("Cargo output-path precedence differs", actual["target_directory"], actual["build_directory"])
		}
		a, e := Discover(context.Background(), item.cwd, home, env)
		if e != nil {
			t.Fatal(e)
		}
		target, _ := a.Effective("build.target-dir")
		build, _ := a.Effective("build.build-dir")
		if target.Path != actual["target_directory"] || build.Path != actual["build_directory"] {
			t.Fatal("modeled origin differs from Cargo", target, build)
		}
	}
	other := filepath.Join(root, "other")
	write(t, filepath.Join(other, ".cargo", "config.toml"), "[build]\ntarget-dir='elsewhere-target'\nbuild-dir='elsewhere-build'\n")
	actual := metadata(other, "--manifest-path", filepath.Join(workspace, "Cargo.toml"))
	if actual["workspace_root"] != workspace || actual["target_directory"] != filepath.Join(other, "elsewhere-target") {
		t.Fatal("manifest-path changed discovery context", actual)
	}
	linked := filepath.Join(root, "linked-member")
	if e := os.Symlink(member, linked); e != nil {
		t.Fatal(e)
	}
	a, e := Discover(context.Background(), linked, home, env)
	if e != nil || a.CWD != member {
		t.Fatal(e, a.CWD)
	}
	relativeCWD := filepath.Join(root, "relative-home-invocation")
	os.Mkdir(relativeCWD, 0700)
	relativeEnv := append([]string{}, env...)
	for i, entry := range relativeEnv {
		if strings.HasPrefix(entry, "CARGO_HOME=") {
			relativeEnv[i] = "CARGO_HOME=../cargo-home"
		}
	}
	resolved, e := ResolveCargoHome(relativeCWD, relativeEnv)
	if e != nil || resolved != home {
		t.Fatal("relative Cargo home", resolved, e)
	}
	ctxRelative, cancelRelative := context.WithTimeout(context.Background(), 30*time.Second)
	relativeCmd := exec.CommandContext(ctxRelative, cargo, "metadata", "--offline", "--no-deps", "--format-version", "1", "--manifest-path", filepath.Join(workspace, "Cargo.toml"))
	relativeCmd.Dir = relativeCWD
	relativeCmd.Env = relativeEnv
	relativeRaw, e := relativeCmd.Output()
	cancelRelative()
	if e != nil {
		t.Fatal(e)
	}
	var relativeMetadata map[string]any
	json.Unmarshal(relativeRaw, &relativeMetadata)
	relativeAudit, e := Discover(context.Background(), relativeCWD, resolved, relativeEnv)
	if e != nil {
		t.Fatal(e)
	}
	relativeTarget, _ := relativeAudit.Effective("build.target-dir")
	reportedRelativeTarget, ok := relativeMetadata["target_directory"].(string)
	if !ok || relativeTarget.Path != filepath.Clean(reportedRelativeTarget) {
		t.Fatal("Cargo home relative origin differs", relativeTarget, relativeMetadata["target_directory"])
	}
	linkedActual := metadata(linked)
	if linkedActual["target_directory"] != filepath.Join(member, "member-target") {
		t.Fatal("symlink cwd discovery differs", linkedActual["target_directory"])
	}
	aliasTarget := filepath.Join(root, "alias-escape")
	config := filepath.Join(member, ".cargo", "config")
	existing, e := os.ReadFile(config)
	if e != nil {
		t.Fatal(e)
	}
	existing = append(existing, []byte("\n[alias]\ncontrolled=['build','--jobs','8','--target-dir',"+strconv.Quote(aliasTarget)+"]\n")...)
	if e = os.WriteFile(config, existing, 0600); e != nil {
		t.Fatal(e)
	}
	run(member, env, "controlled", "--offline")
	rawAlias, e := os.ReadFile(evidence)
	if e != nil || !strings.Contains(string(rawAlias), "NUM_JOBS=8\n") {
		t.Fatal("real alias did not demonstrate hidden jobs", e, string(rawAlias))
	}
	if _, e = Parse([]string{cargo, "controlled", "--offline"}, 1); e == nil {
		t.Fatal("hidden alias accepted by closed parser")
	}
	// The alias is known data, never expanded; builtins stay literal. Re-audit
	// after the deliberate config edit before preparing the admitted plan.
	a, e = Discover(context.Background(), linked, home, env)
	if e != nil {
		t.Fatal(e)
	}
	for _, command := range []string{"build", "check", "test"} {
		argv := []string{cargo, command, "--offline"}
		if command == "test" {
			argv = append(argv, "--no-run")
		}
		parsed, e := Parse(argv, 1)
		if e != nil {
			t.Fatal(e)
		}
		roots := Roots{filepath.Join(root, command, "target"), filepath.Join(root, command, "build"), filepath.Join(root, command, "control")}
		for _, p := range []string{roots.Target, roots.Build, roots.Control} {
			os.MkdirAll(p, 0700)
		}
		plan, e := Compile(parsed, a, roots, Tools{rustc, rustdoc}, append(env, "MAKEFLAGS=--jobserver-auth=777,778"))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(roots.Control, "policy.toml"), plan.Policy, 0600); e != nil {
			t.Fatal(e)
		}
		// Use stdout only: Cargo may legitimately warn about legacy precedence.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, cargo, plan.Metadata...)
		cmd.Dir = member
		cmd.Env = plan.Environment
		raw, e := cmd.Output()
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = DecodeMetadata(raw, roots); e != nil {
			t.Fatal(e)
		}
		var m map[string]any
		json.Unmarshal(raw, &m)
		if m["target_directory"] != roots.Target || m["build_directory"] != roots.Build {
			t.Fatal("authoritative metadata mismatch", m)
		}
		run(member, plan.Environment, plan.Arguments...)
		raw, e = os.ReadFile(evidence)
		if e != nil {
			t.Fatal(e)
		}
		fields := map[string]string{}
		for _, line := range strings.Split(string(raw), "\n") {
			key, value, _ := strings.Cut(line, "=")
			fields[key] = value
		}
		if fields["NUM_JOBS"] != "1" || fields["CARGO_BUILD_JOBS"] != "1" || fields["CARGO_TARGET_DIR"] != roots.Target || fields["CARGO_BUILD_BUILD_DIR"] != roots.Build || !strings.HasPrefix(fields["OUT_DIR"], roots.Build+string(filepath.Separator)) {
			t.Fatal("actual child policy mismatch", fields)
		}
		if !strings.Contains(fields["CARGO_MAKEFLAGS"], "jobserver") || strings.Contains(fields["CARGO_MAKEFLAGS"], "777,778") {
			t.Fatal("fresh valid Cargo jobserver was lost", fields["CARGO_MAKEFLAGS"])
		}
	}
}

// A test-only transparent tracer executes the actual installed rustc unchanged;
// it records just target selectors to compare Cargo's scalar/array merging.
func TestRealCargoConfiguredTargetMerge(t *testing.T) {
	cargo, rustc := os.Getenv("ARM_TEST_CARGO"), os.Getenv("ARM_TEST_RUSTC")
	if cargo == "" || rustc == "" {
		t.Skip("explicit real Cargo/rustc tools required")
	}
	root := fixtureRoot(t)
	home := filepath.Join(root, "cache")
	cwd := filepath.Join(root, "project")
	log := filepath.Join(root, "targets")
	trace := filepath.Join(root, "trace-rustc")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	version, e := exec.CommandContext(ctx, rustc, "-vV").Output()
	cancel()
	if e != nil {
		t.Fatal(e)
	}
	host := ""
	for _, line := range strings.Split(string(version), "\n") {
		if strings.HasPrefix(line, "host: ") {
			host = strings.TrimPrefix(line, "host: ")
		}
	}
	if !targetTriple(host) {
		t.Fatal("real compiler host missing")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nnext=false\nfor arg in \"$@\"; do\n if test \"$next\" = true; then printf '%s\\n' \"$arg\" >> " + quote(log) + "; next=false; fi\n case \"$arg\" in --target) next=true;; --target=*) printf '%s\\n' \"${arg#--target=}\" >> " + quote(log) + ";; esac\ndone\nexec " + quote(rustc) + " \"$@\"\n"
	write(t, trace, script)
	os.Chmod(trace, 0700)
	write(t, filepath.Join(cwd, "Cargo.toml"), "[package]\nname='arm_target_merge'\nversion='0.1.0'\nedition='2021'\n")
	write(t, filepath.Join(cwd, "src", "lib.rs"), "pub fn value()->u32{1}\n")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "CARGO_HOME=" + home, "RUSTC=" + trace, "RUSTUP_AUTO_INSTALL=0", "CARGO_NET_OFFLINE=true"}
	for _, array := range []bool{false, true} {
		lower, upper := strconv.Quote("wasm32-unknown-unknown"), strconv.Quote(host)
		if array {
			lower = "[" + lower + "]"
			upper = "[" + upper + "]"
		}
		write(t, filepath.Join(home, "config.toml"), "[build]\ntarget="+lower+"\n")
		write(t, filepath.Join(cwd, ".cargo", "config.toml"), "[build]\ntarget="+upper+"\n")
		os.Remove(log)
		caseEnv := append(append([]string{}, env...), "CARGO_TARGET_DIR="+filepath.Join(root, "targets-"+strconv.FormatBool(array)))
		a, e := Discover(context.Background(), cwd, home, caseEnv)
		if e != nil {
			t.Fatal(e)
		}
		targets, e := a.EffectiveTargets()
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		cmd := exec.CommandContext(ctx, cargo, "check", "--offline", "--jobs", "1")
		cmd.Dir = cwd
		cmd.Env = caseEnv
		output, runErr := cmd.CombinedOutput()
		cancel()
		// A cross stdlib may deliberately be absent; we inspect real compiler
		// queries, never install it or replace failed tool output with a mock.
		if !array && runErr != nil {
			t.Fatalf("native scalar Cargo failed: %v\n%s", runErr, output)
		}
		raw, e := os.ReadFile(log)
		if e != nil {
			t.Fatal(e)
		}
		seen := map[string]bool{}
		for _, target := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			seen[target] = true
		}
		expected := map[string]bool{}
		for _, target := range targets {
			expected[target.Selector] = true
		}
		if len(seen) != len(expected) {
			t.Fatalf("actual target queries differ from merged audit: got%v expected%v\n%s", seen, expected, output)
		}
		for target := range expected {
			if !seen[target] {
				t.Fatal("effective configured target absent from actual compiler queries", target)
			}
		}
		if array && len(targets) != 2 || !array && len(targets) != 1 {
			t.Fatal(targets)
		}
	}
}
