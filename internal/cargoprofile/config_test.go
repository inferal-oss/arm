package cargoprofile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fixtureRoot(t *testing.T) string {
	t.Helper()
	p, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func write(t *testing.T, p, data string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestConfigPrecedenceLegacyAbsenceAndRedaction(t *testing.T) {
	root := fixtureRoot(t)
	home := filepath.Join(root, "cache")
	cwd := filepath.Join(root, "project", "member")
	os.MkdirAll(cwd, 0700)
	write(t, filepath.Join(home, "config.toml"), "[build]\njobs=2\n[http]\nproxy='private-secret-token'\n")
	write(t, filepath.Join(root, "project", ".cargo", "config"), "[build]\njobs=3\ntarget-dir='old-target'\n")
	write(t, filepath.Join(root, "project", ".cargo", "config.toml"), "this ignored file is deliberately not TOML")
	write(t, filepath.Join(cwd, ".cargo", "config.toml"), "[build]\njobs=4\n")
	a, e := Discover(context.Background(), cwd, home, nil)
	if e != nil {
		t.Fatal(e)
	}
	jobs, _ := a.Effective("build.jobs")
	if jobs.Jobs != 4 {
		t.Fatal(jobs)
	}
	path, _ := a.Effective("build.target-dir")
	if path.Path != filepath.Join(root, "project", "old-target") {
		t.Fatal(path)
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), "private-secret-token") {
		t.Fatal("raw secret leaked")
	}
	if e = a.Revalidate(context.Background()); e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(cwd, ".cargo", "config"), "[build]\njobs=1\n")
	if a.Revalidate(context.Background()) == nil {
		t.Fatal("new higher priority legacy config missed")
	}
}
func TestUnsupportedConfigAndLinksFailClosed(t *testing.T) {
	for _, data := range []string{"include=['other.toml']", "[build]\nrustc-wrapper='private-wrapper-secret'", "[target.'cfg(unix)']\nrustflags=['--emit=/outside']", "[env]\nCARGO_MAKEFLAGS={value='--jobserver-auth=9,10',force=true}", "[env]\nRUSTFLAGS={value='private-secret',force=true}"} {
		root := fixtureRoot(t)
		write(t, filepath.Join(root, ".cargo", "config.toml"), data)
		_, e := Discover(context.Background(), root, filepath.Join(root, "cache"), nil)
		if e == nil || strings.Contains(e.Error(), "private-") {
			t.Fatal("unsafe config or secret diagnostic", e)
		}
	}
	root := fixtureRoot(t)
	write(t, filepath.Join(root, "real"), "[build]\njobs=1")
	os.Mkdir(filepath.Join(root, ".cargo"), 0700)
	os.Symlink(filepath.Join(root, "real"), filepath.Join(root, ".cargo", "config.toml"))
	if _, e := Discover(context.Background(), root, filepath.Join(root, "cache"), nil); e == nil {
		t.Fatal("config symlink accepted")
	}
}
func TestPolicyHasLastControlsAndNoStaleJobserver(t *testing.T) {
	root := fixtureRoot(t)
	os.Mkdir(filepath.Join(root, "cwd"), 0700)
	environment := []string{"HOME=" + root, "CARGO_HOME=" + filepath.Join(root, "cache"), "CARGO_BUILD_JOBS=99", "CARGO_TARGET_DIR=/wrong", "MAKEFLAGS=--jobserver-auth=777,778", "NORMAL_SECRET=do-not-log"}
	a, e := Discover(context.Background(), filepath.Join(root, "cwd"), filepath.Join(root, "cache"), environment)
	if e != nil {
		t.Fatal(e)
	}
	c, _ := Parse([]string{"cargo", "build"}, 1)
	roots := Roots{filepath.Join(root, "target"), filepath.Join(root, "build"), filepath.Join(root, "control")}
	plan, e := Compile(c, a, roots, Tools{"/tool/rustc", "/tool/rustdoc"}, environment)
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, entry := range plan.Environment {
		key, _, _ := strings.Cut(entry, "=")
		if seen[key] {
			t.Fatal("duplicate env", key)
		}
		seen[key] = true
		if strings.Contains(entry, "777,778") {
			t.Fatal("stale jobserver")
		}
	}
	if seen["MAKEFLAGS"] || seen["CARGO_MAKEFLAGS"] {
		t.Fatal("caller jobserver survived")
	}
	if strings.Contains(string(plan.Policy), "CARGO_MAKEFLAGS") {
		t.Fatal("fresh Cargo jobserver would be overridden")
	}
	raw, _ := json.Marshal(plan)
	if strings.Contains(string(raw), "do-not-log") {
		t.Fatal("environment leaked")
	}
	if plan.Arguments[len(plan.Arguments)-2] != "--config" {
		t.Fatal("policy is not last", plan.Arguments)
	}
	roots.Build = filepath.Join(roots.Target, "nested")
	if _, e = Compile(c, a, roots, Tools{"/tool/rustc", "/tool/rustdoc"}, environment); e == nil {
		t.Fatal("nested roots accepted")
	}
	c.Arguments = []string{"build", "--target-dir", "/escape"}
	if _, e = Compile(c, a, plan.Roots, Tools{"/tool/rustc", "/tool/rustdoc"}, environment); e == nil {
		t.Fatal("forged command accepted")
	}
}

func TestConfigLimitsCancellationAndCargoHome(t *testing.T) {
	root := fixtureRoot(t)
	home, e := ResolveCargoHome(root, []string{"HOME=" + root, "CARGO_HOME=relative-cache"})
	if e != nil || home != filepath.Join(root, "relative-cache") {
		t.Fatal(home, e)
	}
	if _, e = ResolveCargoHome(root, []string{"CARGO_HOME=/one", "CARGO_HOME=/two"}); e == nil {
		t.Fatal("ambiguous Cargo home accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Discover(ctx, root, home, nil); e == nil {
		t.Fatal("cancelled audit ran")
	}
	write(t, filepath.Join(root, ".cargo", "config.toml"), strings.Repeat("#", MaxConfigBytes+1))
	if _, e = Discover(context.Background(), root, home, nil); e == nil {
		t.Fatal("oversize config accepted")
	}
	write(t, filepath.Join(root, ".cargo", "config.toml"), "[build]\njobs=1\n")
	a, e := Discover(context.Background(), root, home, nil)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, ".cargo", "config.toml")
	os.Rename(path, path+".old")
	write(t, path, "[build]\njobs=1\n")
	if a.Revalidate(context.Background()) == nil {
		t.Fatal("replacement config inode accepted")
	}
	for _, key := range []string{"CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_RUSTFLAGS", "CARGO_UNSTABLE_BUILD_STD", "RUSTC_BOOTSTRAP", "CARGO_BUILD_TARGET"} {
		if _, e = Discover(context.Background(), root, home, []string{key + "=private-secret"}); e == nil || strings.Contains(e.Error(), "private-secret") {
			t.Fatal("unmodeled environment accepted or leaked", key, e)
		}
	}
}

func TestInvocationDirectoryReplacementInvalidatesAudit(t *testing.T) {
	root := fixtureRoot(t)
	cwd := filepath.Join(root, "project")
	os.Mkdir(cwd, 0700)
	a, e := Discover(context.Background(), cwd, filepath.Join(root, "cache"), nil)
	if e != nil {
		t.Fatal(e)
	}
	os.Rename(cwd, cwd+".old")
	os.Mkdir(cwd, 0700)
	if a.Revalidate(context.Background()) == nil {
		t.Fatal("replaced physical cwd accepted")
	}
}

func TestInvalidUTF8ConfigAndPathsFailClosed(t *testing.T) {
	root := fixtureRoot(t)
	write(t, filepath.Join(root, ".cargo", "config.toml"), "[env]\nSECRET='"+string([]byte{0xff})+"'\n")
	if _, e := Discover(context.Background(), root, filepath.Join(root, "cache"), nil); e == nil {
		t.Fatal("invalid UTF-8 config accepted")
	}
	if _, e := ResolveCargoHome(root, []string{"CARGO_HOME=" + string([]byte{0xff})}); e == nil {
		t.Fatal("invalid UTF-8 path accepted")
	}
}

func TestConfiguredTargetsPreserveOriginsAndHomogeneousMerge(t *testing.T) {
	root := fixtureRoot(t)
	home := filepath.Join(root, "cache")
	cwd := filepath.Join(root, "project")
	os.Mkdir(cwd, 0700)
	write(t, filepath.Join(home, "config.toml"), "[build]\ntarget=['aarch64-apple-darwin','wasm32-unknown-unknown']\n")
	write(t, filepath.Join(cwd, ".cargo", "config.toml"), "[build]\ntarget=['aarch64-unknown-linux-gnu']\n")
	a, e := Discover(context.Background(), cwd, home, nil)
	if e != nil {
		t.Fatal(e)
	}
	targets, e := a.EffectiveTargets()
	if e != nil || len(targets) != 3 || targets[0].Origin != filepath.Join(home, "config.toml") || targets[2].Origin != filepath.Join(cwd, ".cargo", "config.toml") {
		t.Fatal(targets, e)
	}
	last, _ := a.Effective("build.target")
	last.Targets[0] = "caller-mutated"
	again, _ := a.EffectiveTargets()
	if again[2].Selector != "aarch64-unknown-linux-gnu" {
		t.Fatal("mutable target slice escaped accessor")
	}
	write(t, filepath.Join(home, "config.toml"), "[build]\ntarget='wasm32-unknown-unknown'\n")
	if _, e = Discover(context.Background(), cwd, home, nil); e == nil {
		t.Fatal("mixed target forms silently merged")
	}
	write(t, filepath.Join(cwd, ".cargo", "config.toml"), "[build]\ntarget='aarch64-unknown-linux-gnu'\n")
	a, e = Discover(context.Background(), cwd, home, nil)
	if e != nil {
		t.Fatal(e)
	}
	targets, e = a.EffectiveTargets()
	if e != nil || len(targets) != 1 || targets[0].Selector != "aarch64-unknown-linux-gnu" || targets[0].Origin != filepath.Join(cwd, ".cargo", "config.toml") {
		t.Fatal(targets, e)
	}
}

func TestUnmodeledProtectedConfigEnvironmentSelectorsRefused(t *testing.T) {
	for _, key := range []string{"CARGO_BUILD_TARGET", "CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_LINKER", "CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_RUNNER", "CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_RUSTFLAGS"} {
		for _, value := range []string{"", "private-secret"} {
			root := fixtureRoot(t)
			write(t, filepath.Join(root, ".cargo", "config.toml"), "[env]\n"+key+"={value="+strconv.Quote(value)+",force=true}\n")
			if _, e := Discover(context.Background(), root, filepath.Join(root, "cache"), nil); e == nil || strings.Contains(e.Error(), "private-secret") {
				t.Fatal("unmodeled child selector accepted or leaked", key, e)
			}
		}
	}
	for _, key := range []string{"CARGO_BUILD_RUSTC", "CARGO_BUILD_RUSTDOC"} {
		root := fixtureRoot(t)
		write(t, filepath.Join(root, ".cargo", "config.toml"), "[env]\n"+key+"='private-compiler'\n")
		if _, e := Discover(context.Background(), root, filepath.Join(root, "cache"), nil); e == nil || strings.Contains(e.Error(), "private-compiler") {
			t.Fatal("nonempty compiler replacement accepted or leaked", key, e)
		}
	}
}
