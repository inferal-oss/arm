package distribution

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLauncherPreservesCallerDirectoryArgumentsAndExitStatus(t *testing.T) {
	for _, p := range platforms {
		t.Run(strings.Join(p[:], "-"), func(t *testing.T) {
			base := t.TempDir()
			bundle := filepath.Join(base, "bundle with spaces")
			caller := filepath.Join(base, "caller with spaces")
			fakepath := filepath.Join(base, "tools")
			for _, d := range []string{bundle, caller, fakepath} {
				if err := os.Mkdir(d, 0700); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(base, "received")
			write := func(name, text string) {
				t.Helper()
				if err := os.WriteFile(name, []byte(text), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(bundle, "arm"), launcher)
			write(filepath.Join(bundle, "arm-"+p[0]+"-"+p[1]), "#!/bin/sh\nprintf '%s\\000' \"$PWD\" \"$@\" > \"$ARM_TEST_CAPTURE\"\nexit 29\n")
			kernel := "Linux"
			if p[0] == "darwin" {
				kernel = "Darwin"
			}
			arch := p[1]
			if arch == "amd64" {
				arch = "x86_64"
			}
			write(filepath.Join(fakepath, "uname"), "#!/bin/sh\ncase \"$1\" in -s) printf '%s\\n' '"+kernel+"';; -m) printf '%s\\n' '"+arch+"';; esac\n")
			args := []string{"space value", "literal$(not-executed)", "'quotes'", "--home", "./relative state"}
			cmd := exec.Command(filepath.Join(bundle, "arm"), args...)
			cmd.Dir = caller
			cmd.Env = append(os.Environ(), "PATH="+fakepath+":/usr/bin:/bin", "ARM_TEST_CAPTURE="+target)
			output, err := cmd.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 29 {
				t.Fatal(err, string(output))
			}
			actual, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			physicalCaller, err := filepath.EvalSymlinks(caller)
			if err != nil {
				t.Fatal(err)
			}
			expected := strings.Join(append([]string{physicalCaller}, args...), "\x00") + "\x00"
			if string(actual) != expected {
				t.Fatal("launcher changed cwd/argv", string(actual))
			}
		})
	}
}
func TestLauncherUnsupportedPlatformFailsExplicitly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "arm"), []byte(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "uname"), []byte("#!/bin/sh\necho unsupported\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(root, "arm"), "version")
	cmd.Env = append(os.Environ(), "PATH="+root+":/usr/bin:/bin")
	raw, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(raw), "unsupported operating system") {
		t.Fatal(err, string(raw))
	}
}
func TestBundleArchiveRejectsUnsafePathsLinksAndExtraMembers(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", "root/../../outside", "root//duplicate", "root/\\windows", "root/symlink"} {
		t.Run(name, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "unsafe.tar.gz")
			file, err := os.Create(destination)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(file)
			writer := tar.NewWriter(gz)
			h := &tar.Header{Name: name, Typeflag: tar.TypeReg, Size: 1, Mode: 0755, ModTime: epochTime()}
			if strings.HasSuffix(name, "symlink") {
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "/elsewhere"
				h.Size = 0
			}
			if err = writer.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if h.Size > 0 {
				writer.Write([]byte("x"))
			}
			writer.Close()
			gz.Close()
			file.Close()
			if VerifyArchive(context.Background(), destination) == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestVersionCannotSelectArchivePathOrShellSyntax(t *testing.T) {
	for _, version := range []string{"../escape", "/absolute", "", "a/b", "a;command", "a\ncommand", "a$(command)", ".", ".."} {
		root := filepath.Join(t.TempDir(), "uncreated")
		if _, _, err := Build(context.Background(), Options{Root: root, Version: version, Protocol: 1}); err == nil {
			t.Fatal("unsafe version accepted", version)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("invalid version created output")
		}
	}
}

func TestCarrierSourceSnapshotExcludesGeneratedAssetsAndPreservesCaller(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "snapshot")
	for _, d := range []string{"cmd", "internal/companions/generated", "knowledge", "third_party/rsyncgo", "third_party/unreviewed", ".cache"} {
		if err := os.MkdirAll(filepath.Join(source, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range map[string]string{"go.mod": "module fixture\n", "go.sum": "", "cmd/main.go": "package main\n", "internal/companions/assets_enabled.go": "package companions\n", "internal/companions/generated/operator-file": "must remain untouched", "knowledge/index.json": "[]", "third_party/rsyncgo/UPSTREAM-go.mod.txt": "module github.com/gokrazy/rsync\n", "third_party/rsyncgo/LICENSE": "reviewed fork license", "third_party/rsyncgo/engine.go": "package rsync\n", "third_party/unreviewed/secret": "must not copy unrelated third parties", ".cache/generated": "must not copy caches"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := copySource(context.Background(), source, target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(target, "internal/companions/generated")); !os.IsNotExist(err) {
		t.Fatal("generated payloads copied recursively", err)
	}
	for _, name := range []string{"third_party/rsyncgo/UPSTREAM-go.mod.txt", "third_party/rsyncgo/LICENSE", "third_party/rsyncgo/engine.go"} {
		original, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		copied, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || string(copied) != string(original) {
			t.Fatal("reviewed fork input missing or changed", name, err)
		}
	}
	for _, name := range []string{"third_party/unreviewed", ".cache"} {
		if _, err := os.Lstat(filepath.Join(target, name)); !os.IsNotExist(err) {
			t.Fatal("unreviewed input copied", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(source, "internal/companions/generated/operator-file"))
	if err != nil || string(raw) != "must remain untouched" {
		t.Fatal("caller assets changed", err)
	}
	if err = os.Symlink("/elsewhere", filepath.Join(source, "internal", "link")); err != nil {
		t.Fatal(err)
	}
	if err = copySource(context.Background(), source, filepath.Join(t.TempDir(), "reject")); err == nil {
		t.Fatal("snapshot adopted symlinked source")
	}
}
