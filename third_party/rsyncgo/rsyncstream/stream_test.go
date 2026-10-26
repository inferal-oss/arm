package rsyncstream

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type processConn struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (c processConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c processConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c processConn) Close() error                { _ = c.w.Close(); return c.r.Close() }

type stdioConn struct{}

func (stdioConn) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdioConn) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdioConn) Close() error                { return nil }

func TestHelperGoServer(t *testing.T) {
	if os.Getenv("ARM_RSYNC_HELPER") != "1" {
		return
	}
	var direction Direction
	switch os.Getenv("ARM_RSYNC_DIRECTION") {
	case "push":
		direction = Push
	case "pull":
		direction = Pull
	default:
		t.Fatal("bad direction")
	}
	_, err := RunServer(context.Background(), stdioConn{}, direction, Options{PreserveTimes: true, PreservePerms: true}, os.Getenv("ARM_RSYNC_ROOT"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestPushPullAndDelta(t *testing.T) {
	for _, direction := range []Direction{Push, Pull} {
		t.Run(map[Direction]string{Push: "push", Pull: "pull"}[direction], func(t *testing.T) {
			src, dst := filepath.Join(t.TempDir(), "source"), filepath.Join(t.TempDir(), "stage")
			for _, dir := range []string{src, dst} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			data := bytes.Repeat([]byte("a rolling checksum file\n"), 1<<15)
			if err := os.WriteFile(filepath.Join(src, "large"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("large", filepath.Join(src, "link")); err != nil {
				t.Fatal(err)
			}
			o := Options{PreserveLinks: true, PreservePerms: true, PreserveTimes: true}
			transfer := func() int64 {
				left, right := net.Pipe()
				defer left.Close()
				defer right.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				type result struct{ err error }
				serverResult := make(chan result, 1)
				serverRoot, clientRoot := dst, src
				if direction == Pull {
					serverRoot, clientRoot = src, dst
				}
				go func() { _, err := RunServer(ctx, right, direction, o, serverRoot); serverResult <- result{err} }()
				stats, err := RunClient(ctx, left, direction, o, clientRoot)
				if err != nil {
					t.Fatalf("client: %v", err)
				}
				if got := <-serverResult; got.err != nil {
					t.Fatalf("server: %v", got.err)
				}
				return stats.Written + stats.Read
			}
			first := transfer()
			got, err := os.ReadFile(filepath.Join(dst, "large"))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("first copy: %v", err)
			}
			link, err := os.Readlink(filepath.Join(dst, "link"))
			if err != nil || link != "large" {
				t.Fatalf("link %q: %v", link, err)
			}
			copy(data[32000:32010], []byte("new bytes!"))
			if err := os.WriteFile(filepath.Join(src, "large"), data, 0600); err != nil {
				t.Fatal(err)
			}
			delta := transfer()
			t.Logf("first wire bytes=%d, edited-file wire bytes=%d", first, delta)
			got, err = os.ReadFile(filepath.Join(dst, "large"))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("delta copy: %v", err)
			}
			if delta >= first/2 {
				t.Fatalf("delta used %d bytes, first copy %d", delta, first)
			}
		})
	}
}

func TestCancellationJoins(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := RunClient(ctx, left, Pull, Options{}, root); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not join")
	}
}

func TestStagingSymlinkCannotRedirectWrites(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source")
	stage := filepath.Join(t.TempDir(), "stage")
	outside := t.TempDir()
	for _, dir := range []string{src, stage} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(src, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "data"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(stage, "sub")); err != nil {
		t.Fatal(err)
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() { _, err := RunServer(ctx, right, Push, Options{}, stage); serverDone <- err }()
	_, clientErr := RunClient(ctx, left, Push, Options{}, src)
	serverErr := <-serverDone
	if clientErr != nil || serverErr != nil {
		t.Fatalf("client %v, server %v", clientErr, serverErr)
	}
	if _, err := os.Stat(filepath.Join(outside, "data")); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(stage, "sub", "data"))
	if err != nil || string(got) != "inside" {
		t.Fatalf("staged data %q: %v", got, err)
	}
}

func TestRootSymlinkRejected(t *testing.T) {
	actual := t.TempDir()
	link := filepath.Join(t.TempDir(), "root")
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ServerArgs(Push, Options{}, link); err == nil {
		t.Fatal("symlink root accepted")
	}
}

func TestInstalledRsyncServerInterop(t *testing.T) {
	bin, err := exec.LookPath("rsync")
	if err != nil {
		t.Skip("rsync executable unavailable")
	}
	for _, direction := range []Direction{Push, Pull} {
		t.Run(map[Direction]string{Push: "push", Pull: "pull"}[direction], func(t *testing.T) {
			src, dst := filepath.Join(t.TempDir(), "source"), filepath.Join(t.TempDir(), "stage")
			for _, dir := range []string{src, dst} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			data := bytes.Repeat([]byte("0123456789abcdefghij"), 1<<15)
			if err := os.WriteFile(filepath.Join(src, "data"), data, 0600); err != nil {
				t.Fatal(err)
			}
			o := Options{PreserveTimes: true, PreservePerms: true}
			transfer := func() int64 {
				serverRoot, clientRoot := dst, src
				if direction == Pull {
					serverRoot, clientRoot = src, dst
				}
				args, err := ServerArgs(direction, o, serverRoot)
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(bin, args...)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				stdin, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				conn := processConn{r: stdout, w: stdin}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				stats, runErr := RunClient(ctx, conn, direction, o, clientRoot)
				waitErr := cmd.Wait()
				stdin.Close()
				stdout.Close()
				if runErr != nil || waitErr != nil {
					t.Fatalf("client %v, installed server %v: %s", runErr, waitErr, stderr.String())
				}
				return stats.Read + stats.Written
			}
			first := transfer()
			got, err := os.ReadFile(filepath.Join(dst, "data"))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("first: %v", err)
			}
			copy(data[12345:12355], []byte("new bytes!"))
			if err := os.WriteFile(filepath.Join(src, "data"), data, 0600); err != nil {
				t.Fatal(err)
			}
			delta := transfer()
			t.Logf("installed server first wire bytes=%d, edited-file wire bytes=%d", first, delta)
			got, err = os.ReadFile(filepath.Join(dst, "data"))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("delta: %v", err)
			}
			if delta >= first/2 {
				t.Fatalf("delta %d bytes, first %d", delta, first)
			}
		})
	}
}

func TestInstalledRsyncClientInterop(t *testing.T) {
	bin, err := exec.LookPath("rsync")
	if err != nil {
		t.Skip("rsync executable unavailable")
	}
	for _, direction := range []Direction{Push, Pull} {
		t.Run(map[Direction]string{Push: "push", Pull: "pull"}[direction], func(t *testing.T) {
			src, dst := filepath.Join(t.TempDir(), "source"), filepath.Join(t.TempDir(), "stage")
			for _, dir := range []string{src, dst} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			data := bytes.Repeat([]byte("abcdefghij0123456789"), 1<<15)
			if err := os.WriteFile(filepath.Join(src, "data"), data, 0600); err != nil {
				t.Fatal(err)
			}
			shell := filepath.Join(t.TempDir(), "remote-shell")
			if err := os.WriteFile(shell, []byte("#!/bin/sh\nexec \""+os.Args[0]+"\" -test.run=TestHelperGoServer\n"), 0700); err != nil {
				t.Fatal(err)
			}
			transfer := func() {
				args := []string{"-rpt", "--ignore-times", "-e", shell}
				serverRoot := dst
				if direction == Push {
					args = append(args, src+"/", "example:"+dst+"/")
				} else {
					serverRoot = src
					args = append(args, "example:"+src+"/", dst+"/")
				}
				cmd := exec.Command(bin, args...)
				cmd.Env = append(os.Environ(), "ARM_RSYNC_HELPER=1", "ARM_RSYNC_ROOT="+serverRoot, "ARM_RSYNC_DIRECTION="+map[Direction]string{Push: "push", Pull: "pull"}[direction])
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("installed client %v: %s", err, out)
				}
			}
			transfer()
			got, err := os.ReadFile(filepath.Join(dst, "data"))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("first: %v", err)
			}
			copy(data[12345:12355], []byte("new bytes!"))
			if err := os.WriteFile(filepath.Join(src, "data"), data, 0600); err != nil {
				t.Fatal(err)
			}
			transfer()
			got, err = os.ReadFile(filepath.Join(dst, "data"))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("delta: %v", err)
			}
		})
	}
}

func BenchmarkFreshBulkStaging(b *testing.B) {
	const size = 16 << 20
	src := filepath.Join(b.TempDir(), "source")
	if err := os.Mkdir(src, 0700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "bulk"), bytes.Repeat([]byte("0123456789abcdef"), size/16), 0600); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		stage := filepath.Join(b.TempDir(), "stage")
		if err := os.Mkdir(stage, 0700); err != nil {
			b.Fatal(err)
		}
		left, right := net.Pipe()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		serverDone := make(chan error, 1)
		go func() { _, err := RunServer(ctx, right, Push, Options{}, stage); serverDone <- err }()
		b.StartTimer()
		_, clientErr := RunClient(ctx, left, Push, Options{}, src)
		serverErr := <-serverDone
		b.StopTimer()
		left.Close()
		right.Close()
		cancel()
		if clientErr != nil || serverErr != nil {
			b.Fatalf("client %v server %v", clientErr, serverErr)
		}
	}
}
