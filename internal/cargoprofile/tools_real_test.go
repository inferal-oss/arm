package cargoprofile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealInstalledToolchainResolutionAndTargets(t *testing.T) {
	cargo, rustc, rustdoc := os.Getenv("ARM_TEST_CARGO"), os.Getenv("ARM_TEST_RUSTC"), os.Getenv("ARM_TEST_RUSTDOC")
	// Privately extracted distro tools may need shared libraries supplied by
	// the fixture. Query actual native sibling files, never bless its shell
	// wrappers as production compilers.
	if bin := os.Getenv("ARM_TEST_TOOLCHAIN_BIN"); bin != "" {
		if !filepath.IsAbs(bin) {
			t.Fatal("absolute existing native toolchain bin required")
		}
		cargo, rustc, rustdoc = filepath.Join(bin, "cargo"), filepath.Join(bin, "rustc"), filepath.Join(bin, "rustdoc")
	}
	if cargo == "" || rustc == "" || rustdoc == "" {
		t.Skip("explicit existing tools required")
	}
	root := fixtureRoot(t)
	home := filepath.Join(root, "cache")
	env := []string{"HOME=" + root, "PATH=" + os.Getenv("PATH"), "CARGO_HOME=" + home, "RUSTC=" + rustc, "RUSTDOC=" + rustdoc}
	if path := os.Getenv("ARM_TEST_TOOL_LIBRARY_PATH"); path != "" {
		env = append(env, "LD_LIBRARY_PATH="+path)
	}
	a, e := Discover(context.Background(), root, home, env)
	if e != nil {
		t.Fatal(e)
	}
	c, e := Parse([]string{cargo, "check", "--offline"}, 1)
	if e != nil {
		t.Fatal(e)
	}
	tools, e := ResolveTools(context.Background(), c, a, env)
	if e != nil {
		t.Fatal(e)
	}
	if tools.Schema != 1 || tools.Rustup != nil || tools.CargoVersion == "" || len(tools.BuiltinTargets) == 0 || tools.Selection != "concrete-installed-siblings" {
		t.Fatal(tools)
	}
	if e = tools.RevalidateTools(context.Background(), a, env); e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(root, ".cargo", "config.toml"), "[build]\ntarget=['"+tools.Host+"','wasm32-unknown-unknown']\n")
	a, e = Discover(context.Background(), root, home, env)
	if e != nil {
		t.Fatal(e)
	}
	c, e = Parse([]string{cargo, "check", "--offline", "--target", tools.Host}, 1)
	if e != nil {
		t.Fatal(e)
	}
	tools, e = ResolveTools(context.Background(), c, a, env)
	if e != nil || len(tools.CheckedTargets) != 3 {
		t.Fatal(e, tools.CheckedTargets)
	}
	c, _ = Parse([]string{cargo, "check", "--target", "syntactically-valid-custom"}, 1)
	if _, e = ResolveTools(context.Background(), c, a, env); e == nil {
		t.Fatal("unverified custom target accepted")
	}
	if _, e = os.Stat(filepath.Join(home, "registry")); !os.IsNotExist(e) {
		t.Fatal("tool resolution created registry cache", e)
	}
	t.Logf("resolved actual installed %s / %s / %s, host %s; builtins %d", tools.CargoVersion, tools.RustcVersion, tools.RustdocVersion, tools.Host, len(tools.BuiltinTargets))
}
func TestRealRustupResolutionNoImplicitInstall(t *testing.T) {
	proxy := os.Getenv("ARM_TEST_CARGO_PROXY")
	if proxy == "" {
		t.Skip("explicit existing rustup Cargo proxy required")
	}
	root := fixtureRoot(t)
	home := filepath.Join(root, "cache")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "CARGO_HOME=" + home}
	if value := os.Getenv("RUSTUP_HOME"); value != "" {
		env = append(env, "RUSTUP_HOME="+value)
	}
	a, e := Discover(context.Background(), root, home, env)
	if e != nil {
		t.Fatal(e)
	}
	c, e := Parse([]string{proxy, "check", "--offline"}, 1)
	if e != nil {
		t.Fatal(e)
	}
	tools, e := ResolveTools(context.Background(), c, a, env)
	if e != nil {
		t.Fatal(e)
	}
	if tools.Rustup == nil || tools.Toolchain == "" || tools.Selection != "installed-rustup-query" {
		t.Fatal("proxy was not bound to existing concrete toolchain", tools.Selection)
	}
	if e = tools.RevalidateTools(context.Background(), a, env); e != nil {
		t.Fatal(e)
	}
	// Select a nonexistent toolchain only in this isolated child's environment.
	// No rustup install/override/default command and no global file mutation.
	missing := "arm-uninstalled-fixture-" + strings.ReplaceAll(filepath.Base(root), " ", "-")
	denied := append(append([]string{}, env...), "RUSTUP_TOOLCHAIN="+missing)
	if _, e = ResolveTools(context.Background(), c, a, denied); e == nil {
		t.Fatal("missing toolchain unexpectedly resolved")
	}
	// Existing toolchain selection changing after preparation must refuse the
	// compilation gate as well, including when the new selection is missing.
	write(t, filepath.Join(root, "rust-toolchain.toml"), "[toolchain]\nchannel='"+missing+"'\n")
	if e = tools.RevalidateTools(context.Background(), a, env); e == nil {
		t.Fatal("changed directory toolchain selection accepted")
	}
	t.Logf("real rustup selected existing %s; missing and changed selections refused with automatic installation disabled", tools.Toolchain)
}
