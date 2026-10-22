package mesh

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeExecutableChild(t *testing.T) {
	if os.Getenv("ARM_RUNTIME_TEST_CHILD") != "1" {
		return
	}
	r, e := CaptureRuntimeExecutable()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	defer r.Close()
	fmt.Println("READY")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	if _, e = r.Proof(); e == nil {
		fmt.Println("UNSAFE")
	} else {
		fmt.Println("CHANGED")
	}
	os.Exit(0)
}
func TestRuntimeExecutableReplacementCannotInheritOldProcessProof(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "arm")
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	copyFile := func(source, target string) {
		in, e := os.Open(source)
		if e != nil {
			t.Fatal(e)
		}
		defer in.Close()
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = io.Copy(out, in); e != nil {
			t.Fatal(e)
		}
		if e = out.Close(); e != nil {
			t.Fatal(e)
		}
	}
	copyFile(self, path)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-test.run=^TestRuntimeExecutableChild$")
	cmd.Env = append(os.Environ(), "ARM_RUNTIME_TEST_CHILD=1")
	in, e := cmd.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = os.Stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { in.Close(); cmd.Process.Kill(); cmd.Wait() }()
	lines := bufio.NewScanner(out)
	if !lines.Scan() || lines.Text() != "READY" {
		t.Fatal("runtime snapshot fixture failed")
	}
	old := filepath.Join(root, "old-running-image")
	if e = os.Rename(path, old); e != nil {
		t.Fatal(e)
	}
	copyFile(self, path) // Same bytes/version, different installed inode.
	if _, e = io.WriteString(in, "proof\n"); e != nil {
		t.Fatal(e)
	}
	in.Close()
	if !lines.Scan() || lines.Text() != "CHANGED" {
		t.Fatal("new installed path inherited old running image proof")
	}
	if e = cmd.Wait(); e != nil {
		t.Fatal(e)
	}
}
