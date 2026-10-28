package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/mesh"
	"getarm.org/internal/model"
)

func TestDaemonMeshEnsureResolvesFreshConfigAndPreservesRecordedReplay(t *testing.T) {
	t.Setenv("ARM_EXTENSION_TEST", "1")
	driver := extensionFixture(t, t.TempDir(), "extension-driver")
	for _, recorded := range []bool{false, true} {
		name := "fresh-config"
		if recorded {
			name = "recorded-policy"
		}
		t.Run(name, func(t *testing.T) {
			base, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			home := filepath.Join(base, "arm")
			if e = os.Mkdir(home, 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(home, "config.toml"), []byte("schema=1\n[daemon]\ncpu=2\nmemory='512KiB'\n"), 0600); e != nil {
				t.Fatal(e)
			}
			if recorded {
				p := mesh.DefaultStartup()
				p.Resources.CPU = 1
				p.Resources.Memory = 1024
				if e = mesh.SaveStartupPolicy(home, p); e != nil {
					t.Fatal(e)
				}
			}
			a := app{home: home, ctx: t.Context()}
			inspection, e := a.inspectMeshLifecycle()
			if e != nil {
				t.Fatal(e)
			}
			entries, e := os.ReadDir(home)
			wantEntries := 1
			if recorded {
				wantEntries = 2
			}
			if e != nil || len(entries) != wantEntries {
				t.Fatal("inspection created state", entries, e)
			}
			want := model.Resources{CPU: 2, Memory: 512 << 10}
			if recorded {
				want = model.Resources{CPU: 1, Memory: 1024}
			}
			if inspection.Startup.Resources != want {
				t.Fatal("wrong inspected precedence", inspection.Startup)
			}
			var child *exec.Cmd
			var done chan error
			t.Cleanup(func() {
				if child == nil {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				var result any
				_ = client.Call(ctx, home, "shutdown", model.ShutdownRequest{}, &result)
				select {
				case err := <-done:
					if err != nil {
						t.Error("original backend exit", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("fixture backend did not stop")
					_ = child.Process.Kill()
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("original backend Wait remained unresolved")
					}
				}
			})
			launch := func(cmd *exec.Cmd) error {
				joined := strings.Join(cmd.Args, " ")
				if !recorded && (strings.Contains(joined, "--cpu") || strings.Contains(joined, "--memory") || strings.Contains(joined, "--disk") || strings.Contains(joined, "--listen")) {
					t.Fatal("generated defaults became flags", cmd.Args)
				}
				cmd.Path = driver
				cmd.Args[0] = driver
				if e := cmd.Start(); e != nil {
					return e
				}
				child = cmd
				done = make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				return nil
			}
			actual, e := a.ensureMeshBackendWithLauncher(inspection.Startup, launch)
			if e != nil || !actual.Running || actual.Startup.Resources != want {
				t.Fatal("actual ensure ignored resolved policy", actual, e)
			}
			// A healthy backend and its recorded startup remain authoritative even
			// after the desired next ordinary-start file changes.
			if e = os.WriteFile(filepath.Join(home, "config.toml"), []byte("schema=1\n[daemon]\ncpu=3\n"), 0600); e != nil {
				t.Fatal(e)
			}
			again, e := a.ensureMeshBackend(inspection.Startup)
			if e != nil || !again.Running || again.Startup.Resources != want {
				t.Fatal("running policy was replaced", again, e)
			}
			if !recorded {
				// A healthy legacy backend has actual settings even if its optional
				// successful-launch history is absent and next-start config is bad.
				if e = os.Remove(filepath.Join(home, "daemon-launch.json")); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(home, "config.toml"), []byte("schema=999"), 0600); e != nil {
					t.Fatal(e)
				}
				legacy, e := a.ensureMeshBackend(inspection.Startup)
				if e != nil || !legacy.Running || legacy.Startup.Resources != want {
					t.Fatal("desired config hid healthy legacy authority", legacy, e)
				}
			}
		})
	}
}

func TestDaemonMeshEnsureChangedConfigurationRefusesBeforeBackendOpen(t *testing.T) {
	t.Setenv("ARM_EXTENSION_TEST", "1")
	driver := extensionFixture(t, t.TempDir(), "extension-driver")
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	home := filepath.Join(base, "arm")
	if e = os.Mkdir(home, 0700); e != nil {
		t.Fatal(e)
	}
	config := filepath.Join(home, "config.toml")
	if e = os.WriteFile(config, []byte("schema=1\n[daemon]\ncpu=2"), 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	a := app{home: home, ctx: ctx}
	inspection, e := a.inspectMeshLifecycle()
	if e != nil {
		t.Fatal(e)
	}
	var child *exec.Cmd
	done := make(chan error, 1)
	waitConsumed := false
	t.Cleanup(func() {
		if child == nil || waitConsumed {
			return
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = child.Process.Kill()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("original changed-config fixture Wait unresolved")
			}
		}
	})
	launch := func(cmd *exec.Cmd) error {
		// Change exactly the mutable input after ensure's expected-policy check.
		if e = os.WriteFile(config, []byte("schema=1\n[daemon]\ncpu=3"), 0600); e != nil {
			return e
		}
		cmd.Path = driver
		cmd.Args[0] = driver
		if e = cmd.Start(); e != nil {
			return e
		}
		child = cmd
		go func() { done <- cmd.Wait() }()
		return nil
	}
	actual, e := a.ensureMeshBackendWithLauncher(inspection.Startup, launch)
	if e == nil || actual.Running || child == nil {
		t.Fatal("changed configuration was accepted", actual, e)
	}
	select {
	case err := <-done:
		waitConsumed = true
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 {
			t.Fatal("guard did not cause exact CLI refusal", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("changed-config child did not exit")
	}
	for _, name := range []string{"state.db", "endpoint.json", "daemon-launch.json"} {
		if _, e = os.Lstat(filepath.Join(home, name)); !os.IsNotExist(e) {
			t.Fatal("guard created backend state", name, e)
		}
	}
	raw, e := os.ReadFile(filepath.Join(home, "daemon.log"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(raw), "configuration_changed") {
		t.Fatal("missing actionable guard diagnosis", string(raw))
	}
}
