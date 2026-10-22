package cargoprofile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRealOwnedCargoPreparationBuildCheckAndNoRun(t *testing.T) {
	cargo, rustc, rustdoc := os.Getenv("ARM_TEST_CARGO"), os.Getenv("ARM_TEST_RUSTC"), os.Getenv("ARM_TEST_RUSTDOC")
	if bin := os.Getenv("ARM_TEST_TOOLCHAIN_BIN"); bin != "" {
		cargo, rustc, rustdoc = filepath.Join(bin, "cargo"), filepath.Join(bin, "rustc"), filepath.Join(bin, "rustdoc")
	}
	if cargo == "" || rustc == "" || rustdoc == "" {
		t.Skip("explicit existing native tools required")
	}
	root := fixtureRoot(t)
	source := filepath.Join(root, "source")
	cache := filepath.Join(root, "cache")
	write(t, filepath.Join(source, "Cargo.toml"), "[package]\nname='arm_owned_prepare'\nversion='0.1.0'\nedition='2021'\n")
	write(t, filepath.Join(source, "src", "main.rs"), "fn main(){println!(\"owned-output\");}\n#[test] fn do_not_run(){panic!(\"tests must not execute\");}\n")
	// A hostile local setting must lose to the last owned policy, including
	// the compiler job budget and no-implicit-toolchain-install environment.
	write(t, filepath.Join(source, ".cargo", "config.toml"), "[env]\nRUSTUP_AUTO_INSTALL={value='1',force=true}\n[build]\njobs=8\n")
	write(t, filepath.Join(source, "build.rs"), `fn main(){
 let text=format!("jobs={}\ninstall={}\n",std::env::var("NUM_JOBS").unwrap(),std::env::var("RUSTUP_AUTO_INSTALL").unwrap());
 std::fs::write(std::env::var("ARM_PREPARE_OBSERVATION").unwrap(),text).unwrap();
}`)
	observation := filepath.Join(root, "build-observation")
	env := []string{"ARM_PREPARE_OBSERVATION=" + observation, "HOME=" + root, "PATH=" + os.Getenv("PATH"), "CARGO_HOME=" + cache, "RUSTC=" + rustc, "RUSTDOC=" + rustdoc, "CARGO_NET_OFFLINE=true"}
	if path := os.Getenv("ARM_TEST_TOOL_LIBRARY_PATH"); path != "" {
		env = append(env, "LD_LIBRARY_PATH="+path)
	}
	for _, kind := range []string{"build", "check", "test"} {
		t.Run(kind, func(t *testing.T) {
			allocation, id := allocationFixture(t)
			owned, e := CreateOwnedPaths(context.Background(), allocation, id, strings.Repeat("b", 32), func(BirthStep) error { return nil })
			if e != nil {
				t.Fatal(e)
			}
			args := []string{cargo, kind, "--offline"}
			if kind == "test" {
				args = append(args, "--no-run")
			}
			command, e := Parse(args, 1)
			if e != nil {
				t.Fatal(e)
			}
			var notices int
			prepared, e := Prepare(context.Background(), command, source, owned, env, func(*FileEvidence) error { notices++; return nil })
			if e != nil {
				t.Fatal(e)
			}
			if notices != 2 || prepared.Plan.Jobs != 1 || prepared.Policy.SHA256 == "" || len(prepared.Source.Members) != 1 {
				t.Fatal("incomplete prepared evidence", notices)
			}
			if e = prepared.Revalidate(context.Background(), env); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, prepared.Tools.Cargo.Path, prepared.Plan.Arguments...)
			cmd.Dir = prepared.Audit.CWD
			cmd.Env = prepared.Plan.Environment
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("real owned %s: %v\n%s", kind, e, out)
			}
			observed, e := os.ReadFile(observation)
			if e != nil || string(observed) != "jobs=1\ninstall=0\n" {
				t.Fatal("owned policy did not control actual build-script environment", string(observed), e)
			}
			if strings.Contains(string(out), "tests must not execute") {
				t.Fatal("test body executed")
			}
			if kind == "build" {
				binary := filepath.Join(owned.Target.Path, "debug", "arm_owned_prepare")
				out, e = exec.CommandContext(ctx, binary).Output()
				if e != nil || string(out) != "owned-output\n" {
					t.Fatal("built target did not run", string(out), e)
				}
			}
			if _, e = os.Stat(filepath.Join(owned.Build.Path, "debug")); e != nil {
				t.Fatal("distinct intermediate directory unused", e)
			}
		})
	}
}
