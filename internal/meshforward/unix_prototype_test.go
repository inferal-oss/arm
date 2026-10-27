//go:build arm_forward_review && arm_forward_unix_prototype && (darwin || linux)

package meshforward

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func unixPrototypeHome(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(root, "fup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(home, "mesh"), 0700); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestUnixPrototypeAdmissionBeforeApplicationBytesAndCancellationJoin(t *testing.T) {
	home := unixPrototypeHome(t)
	key := strings.Repeat("a", 32)
	entered := make(chan struct{})
	admit := make(chan struct{})
	remoteReady := make(chan net.Conn, 1)
	ctx, cancel := context.WithCancel(t.Context())
	p, err := StartUnixPrototype(ctx, home, key, time.Now().Add(time.Minute), func(context.Context) (net.Conn, error) {
		close(entered)
		<-admit // models the existing pinned TLS, token and READY exchange
		client, server := net.Pipe()
		remoteReady <- server
		return client, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.DialTimeout("unix", p.Path(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err = client.Write([]byte("effect")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("admission did not start")
	}
	select {
	case <-remoteReady:
		t.Fatal("application reached remote before admission")
	default:
	}
	close(admit)
	remote := <-remoteReady
	defer remote.Close()
	if err = remote.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	bytes := make([]byte, 6)
	if _, err = io.ReadFull(remote, bytes); err != nil || string(bytes) != "effect" {
		t.Fatal(string(bytes), err)
	}
	cancel()
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(p.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("joined prototype left a live socket path", err)
	}
}

func TestUnixPrototypeLostAckAndUnjoinedRestartRefuseAdoption(t *testing.T) {
	home := unixPrototypeHome(t)
	key := strings.Repeat("b", 32)
	ctx, cancel := context.WithCancel(t.Context())
	open := func(context.Context) (net.Conn, error) { return nil, errors.New("unused") }
	p, err := StartUnixPrototype(ctx, home, key, time.Now().Add(time.Minute), open)
	if err != nil {
		t.Fatal(err)
	}
	if replacement, err := StartUnixPrototype(ctx, home, key, time.Now().Add(time.Minute), open); err == nil {
		replacement.Close()
		t.Fatal("lost start reply created or adopted a second socket")
	}
	cancel()
	<-p.done // original process lost before publishing cleanup evidence
	if replacement, err := StartUnixPrototype(t.Context(), home, key, time.Now().Add(time.Minute), open); err == nil {
		replacement.Close()
		t.Fatal("unjoined restart adopted an old Unix path")
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnixPrototypeChangedInodeAndCloseFailureRetainPath(t *testing.T) {
	for _, mode := range []string{"replaced_socket", "close_error"} {
		t.Run(mode, func(t *testing.T) {
			home := unixPrototypeHome(t)
			p, err := StartUnixPrototype(t.Context(), home, strings.Repeat("c", 32), time.Now().Add(time.Minute),
				func(context.Context) (net.Conn, error) { return nil, errors.New("unused") })
			if err != nil {
				t.Fatal(err)
			}
			if mode == "replaced_socket" {
				if err = os.Remove(p.Path()); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(p.Path(), []byte("foreign path"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				p.record(errors.New("injected original close failure"))
			}
			if err = p.Close(); err == nil {
				t.Fatal("uncertain original identity or close silently removed path")
			}
			if _, err = os.Lstat(p.Path()); err != nil {
				t.Fatal("uncertain original path was removed", err)
			}
		})
	}
}
