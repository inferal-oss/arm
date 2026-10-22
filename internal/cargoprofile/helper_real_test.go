package cargoprofile

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCargoAdmittedHelperChild(t *testing.T) {
	if os.Getenv("ARM_TEST_CARGO_HELPER") != "1" {
		return
	}
	path, e := os.Executable()
	if e != nil {
		os.Exit(125)
	}
	image, e := snapshotExecutable(context.Background(), path)
	if e != nil {
		os.Exit(125)
	}
	in, out := os.NewFile(3, "cargo-parent-input"), os.NewFile(4, "cargo-parent-output")
	runtime := HelperRuntime{SHA256: image.SHA256, Size: image.Size, Version: "fixture", Protocol: 1}
	os.Exit(RunHelper(context.Background(), in, out, runtime, os.Stdout, os.Stderr))
}
func TestRealPrivateHelperDefersWorkUntilBothDurableGates(t *testing.T) {
	cargo, rustc, rustdoc := os.Getenv("ARM_TEST_CARGO"), os.Getenv("ARM_TEST_RUSTC"), os.Getenv("ARM_TEST_RUSTDOC")
	if bin := os.Getenv("ARM_TEST_TOOLCHAIN_BIN"); bin != "" {
		cargo, rustc, rustdoc = filepath.Join(bin, "cargo"), filepath.Join(bin, "rustc"), filepath.Join(bin, "rustdoc")
	}
	if cargo == "" || rustc == "" || rustdoc == "" {
		t.Skip("explicit existing native tools required")
	}
	root := fixtureRoot(t)
	source := filepath.Join(root, "source")
	ran := filepath.Join(root, "ran")
	write(t, filepath.Join(source, "Cargo.toml"), "[package]\nname='arm_helper_gate'\nversion='0.1.0'\nedition='2021'\n")
	write(t, filepath.Join(source, "src", "main.rs"), "fn main(){}\n")
	write(t, filepath.Join(source, "build.rs"), "fn main(){std::fs::write(std::env::var(\"ARM_GATE_MARKER\").unwrap(),\"ran\").unwrap();}\n")
	for _, scenario := range []string{"parent-disappears-before-birth", "parent-disappears-after-authorization", "missing-compile-grant", "expired-compile-grant", "changed-source-after-prepared", "both-gates"} {
		t.Run(scenario, func(t *testing.T) {
			_ = os.Remove(ran)
			allocation, id := allocationFixture(t)
			program, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			image, e := snapshotExecutable(context.Background(), program)
			if e != nil {
				t.Fatal(e)
			}
			runtime := HelperRuntime{SHA256: image.SHA256, Size: image.Size, Version: "fixture", Protocol: 1}
			inR, inW, e := os.Pipe()
			if e != nil {
				t.Fatal(e)
			}
			defer inR.Close()
			defer inW.Close()
			outR, outW, e := os.Pipe()
			if e != nil {
				t.Fatal(e)
			}
			defer outR.Close()
			defer outW.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, program, "-test.run=^TestCargoAdmittedHelperChild$")
			cmd.Dir = source
			cmd.ExtraFiles = []*os.File{inR, outW}
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.Env = []string{"ARM_TEST_CARGO_HELPER=1", "ARM_GATE_MARKER=" + ran, "HOME=" + root, "PATH=" + os.Getenv("PATH"), "CARGO_HOME=" + filepath.Join(root, "cache"), "RUSTC=" + rustc, "RUSTDOC=" + rustdoc, "CARGO_NET_OFFLINE=true"}
			if p := os.Getenv("ARM_TEST_TOOL_LIBRARY_PATH"); p != "" {
				cmd.Env = append(cmd.Env, "LD_LIBRARY_PATH="+p)
			}
			var log bytes.Buffer
			cmd.Stdout, cmd.Stderr = &log, &log
			if e = cmd.Start(); e != nil {
				t.Fatal(e)
			}
			inR.Close()
			outW.Close()
			waited := false
			defer func() {
				if !waited {
					_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
					_ = cmd.Wait()
				}
			}()
			ch := HelperChannel{In: outR, Out: inW, Lease: strings.Repeat("a", 32), Nonce: strings.Repeat("b", 64)}
			command, e := Parse([]string{cargo, "build", "--offline"}, 1)
			if e != nil {
				t.Fatal(e)
			}
			init := HelperInit{Command: command, CWD: source, Allocation: allocation, AllocationIdentity: id, Runtime: runtime, Expires: time.Now().Add(time.Minute).UTC()}
			if e = ch.Send("init", init); e != nil {
				t.Fatal(e)
			}
			var hello HelperHello
			if e = ch.Receive("hello", &hello); e != nil {
				t.Fatal(e, log.String())
			}
			if hello.Process.PID != cmd.Process.Pid || hello.Process.PGID != cmd.Process.Pid || hello.Runtime != runtime {
				t.Fatal("unbound helper hello")
			}
			items, e := os.ReadDir(allocation)
			if e != nil || len(items) != 0 {
				t.Fatal("mutated before preflight gate", items, e)
			}
			if e = ch.Send("preflight", HelperGrant{Approved: true, Revision: 1, Expires: init.Expires}); e != nil {
				t.Fatal(e)
			}
			revision := 1
			preparedSeen := false
			startedSeen := false
			for {
				packet, e := ch.Next()
				if e != nil {
					t.Fatal(e, log.String())
				}
				if scenario == "parent-disappears-before-birth" {
					if packet.Kind != "birth" {
						t.Fatal(packet.Kind)
					}
					inW.Close()
					_ = cmd.Wait()
					waited = true
					items, e = os.ReadDir(allocation)
					if e != nil || len(items) != 0 {
						t.Fatal("unacknowledged birth mutated namespace", items, e)
					}
					if _, e = os.Stat(ran); !os.IsNotExist(e) {
						t.Fatal("project task ran before birth acknowledgement", e)
					}
					return
				}
				revision++
				switch packet.Kind {
				case "birth":
					var step BirthStep
					if e = DecodeCanonical(packet.Data, &step); e != nil {
						t.Fatal(e)
					}
					if e = ch.Send("recorded", HelperAck{Step: step.Step, Revision: revision}); e != nil {
						t.Fatal(e)
					}
				case "policy":
					var step HelperPolicy
					if e = DecodeCanonical(packet.Data, &step); e != nil {
						t.Fatal(e)
					}
					if e = ch.Send("recorded", HelperAck{Step: 5, Revision: revision}); e != nil {
						t.Fatal(e)
					}
				case "prepared":
					var prepared Prepared
					if e = DecodeCanonical(packet.Data, &prepared); e != nil {
						t.Fatal(e)
					}
					preparedSeen = true
					if _, e = os.Stat(ran); !os.IsNotExist(e) {
						t.Fatal("build script ran before compilation gate", e)
					}
					digest, e := PreparedDigest(prepared)
					if e != nil {
						t.Fatal(e)
					}
					if scenario == "missing-compile-grant" {
						inW.Close()
						_ = cmd.Wait()
						waited = true
						if _, e = os.Stat(ran); !os.IsNotExist(e) {
							t.Fatal("task ran without compilation grant", e)
						}
						return
					}
					expires := init.Expires
					if scenario == "expired-compile-grant" {
						expires = time.Now().Add(-time.Second).UTC()
					}
					if scenario == "changed-source-after-prepared" {
						write(t, filepath.Join(source, "Cargo.toml"), "[package]\nname='arm_helper_gate'\nversion='0.1.1'\nedition='2021'\n")
					}
					if e = ch.Send("compile", HelperGrant{Approved: true, Revision: revision, Expires: expires, EvidenceSHA256: digest}); e != nil {
						t.Fatal(e)
					}
					if scenario == "parent-disappears-after-authorization" {
						inW.Close()
						_ = cmd.Wait()
						waited = true
						// EOF can race actual exec after grant; only current
						// owned group termination is claimed, never nonexecution.
						if e := syscall.Kill(-hello.Process.PGID, 0); e != syscall.ESRCH {
							t.Fatal("parent EOF left live owned process group", e)
						}
						return
					}
				case "started":
					var started HelperStarted
					if e = DecodeCanonical(packet.Data, &started); e != nil {
						t.Fatal(e)
					}
					if !preparedSeen || started.Process.PGID != hello.Process.PGID {
						t.Fatal("Cargo escaped owned helper group")
					}
					startedSeen = true
				case "finished":
					var finished HelperFinished
					if e = DecodeCanonical(packet.Data, &finished); e != nil {
						t.Fatal(e)
					}
					if scenario == "expired-compile-grant" || scenario == "changed-source-after-prepared" {
						e = cmd.Wait()
						waited = true
						if e == nil || startedSeen || finished.CommandStarted || finished.Code != 125 {
							t.Fatal("invalid final gate reached Cargo", finished, e, log.String())
						}
						if _, e = os.Stat(ran); !os.IsNotExist(e) {
							t.Fatal("project task ran after invalid final gate", e)
						}
						if scenario == "changed-source-after-prepared" {
							write(t, filepath.Join(source, "Cargo.toml"), "[package]\nname='arm_helper_gate'\nversion='0.1.0'\nedition='2021'\n")
						}
						return
					}
					if !startedSeen || finished.Code != 0 || !finished.CommandStarted {
						t.Fatal("helper did not complete actual Cargo", finished, log.String())
					}
					e = cmd.Wait()
					waited = true
					if e != nil {
						t.Fatal(e, log.String())
					}
					if data, e := os.ReadFile(ran); e != nil || string(data) != "ran" {
						t.Fatal("actual task did not run after both gates", string(data), e)
					}
					return
				default:
					t.Fatal("unexpected helper phase", packet.Kind)
				}
			}
		})
	}
}
