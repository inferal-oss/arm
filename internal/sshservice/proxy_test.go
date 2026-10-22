package sshservice

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// A private test-binary subprocess acts as the future fixed CLI proxy entry;
	// it cannot launch commands or open a network destination.
	if len(os.Args) == 4 && os.Args[1] == "_ssh-proxy" && os.Args[2] == "--socket" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if Proxy(ctx, os.Args[3], os.Stdin, os.Stdout) != nil {
			os.Exit(97)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func socketFixture(t *testing.T) (string, *net.UnixListener) {
	t.Helper()
	dir, err := os.MkdirTemp("/private/tmp", "arm-ssh-%n ' ")
	if os.IsNotExist(err) {
		dir, err = os.MkdirTemp("/tmp", "arm-ssh-%n ' ")
	}
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) }) // own disposable private fixture only
	path := filepath.Join(dir, "proxy.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return path, l
}
func TestProxyPrivateSocketAndBoundedCancellation(t *testing.T) {
	path, listener := socketFixture(t)
	accepted := make(chan struct{})
	go func() {
		c, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer c.Close()
		close(accepted)
		_, _ = io.Copy(c, c)
	}()
	input, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Proxy(ctx, path, input, io.Discard) }()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("private proxy did not connect")
	}
	cancel()
	writer.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancel lost")
		}
	case <-time.After(time.Second):
		t.Fatal("proxy did not stop")
	}
}
func TestProxyRejectsPublicOrSymlinkSocket(t *testing.T) {
	for _, kind := range []string{"public", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path, _ := socketFixture(t)
			if kind == "public" {
				os.Chmod(path, 0666)
			} else {
				os.Rename(path, path+".old")
				os.Symlink(path+".old", path)
			}
			in, out := io.Pipe()
			defer in.Close()
			defer out.Close()
			if Proxy(context.Background(), path, in, io.Discard) == nil {
				t.Fatal("unsafe proxy socket accepted")
			}
		})
	}
}
