package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/cli"
	"getarm.org/internal/client"
	"getarm.org/internal/model"
	"getarm.org/internal/server"
)

func TestRunTransparencyCLI(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1, Memory: 1 << 30, Disk: 1 << 30})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("backend did not join")
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(home, "endpoint.json")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backend not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	call := func(args ...string) (int, string, string) {
		var out, errout bytes.Buffer
		code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &errout)
		return code, out.String(), errout.String()
	}
	code, out, stderr := call("proxy", "--quiet", "--reason", "transparent fixture", "--", "/bin/sh", "-c", "printf hello; printf error >&2; exit 7")
	var run model.Run
	if code != 7 || json.Unmarshal([]byte(out), &run) != nil || stderr != "" || run.LeaseID != "" || run.ExitCode == nil || *run.ExitCode != 7 || run.LogBytes != 10 {
		t.Fatalf("quiet proxy result %d %s %s", code, out, stderr)
	}
	for _, selector := range []string{run.ID, run.Name} {
		code, out, stderr = call("log", "-f", selector)
		if code != 0 || out != "helloerror" || stderr != "" {
			t.Fatalf("log %s: %d %q %q", selector, code, out, stderr)
		}
	}
	code, out, stderr = call("runs", "--json")
	var runs []model.Run
	if code != 0 || json.Unmarshal([]byte(out), &runs) != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("runs: %d %s %s", code, out, stderr)
	}
	var status model.Status
	if err := client.Call(ctx, home, "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Leases) != 0 {
		t.Fatalf("proxy acquired leases: %+v", status.Leases)
	}
	code, out, stderr = call("proxy", "--reason", "multi page fixture", "--", "/bin/sh", "-c", "head -c 200000 /dev/zero | tr '\\000' x")
	if code != 0 || json.Unmarshal([]byte(out), &run) != nil || len(stderr) != 200000 || run.LogBytes != 200000 {
		t.Fatalf("streaming proxy: code=%d bytes=%d result=%s err=%q", code, len(stderr), out, stderr[:min(len(stderr), 100)])
	}
	code, out, stderr = call("log", run.ID, "--offset", "65535")
	if code != 0 || out != strings.Repeat("x", 200000-65535) || stderr != "" {
		t.Fatalf("paged log: %d bytes=%d err=%s", code, len(out), stderr)
	}

	proxyDone := make(chan struct {
		code        int
		out, stderr string
	}, 1)
	go func() {
		code, out, stderr := call("proxy", "--quiet", "--reason", "live follow fixture", "--", "/bin/sh", "-c", "printf first; sleep 2; printf last")
		proxyDone <- struct {
			code        int
			out, stderr string
		}{code, out, stderr}
	}()
	var live model.Run
	deadline = time.Now().Add(5 * time.Second)
	for live.ID == "" {
		if err := client.Call(ctx, home, "runs", nil, &runs); err != nil {
			t.Fatal(err)
		}
		for _, candidate := range runs {
			if candidate.Reason == "live follow fixture" {
				live = candidate
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("live run not discoverable")
		}
		if live.ID == "" {
			time.Sleep(10 * time.Millisecond)
		}
	}
	stream := &runStreamWriter{ready: make(chan struct{}, 1)}
	followDone := make(chan int, 1)
	var followErr bytes.Buffer
	go func() {
		followDone <- cli.Main([]string{"--home", home, "log", "-f", live.ID}, strings.NewReader(""), stream, &followErr)
	}()
	select {
	case <-stream.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("live log did not stream")
	}
	select {
	case result := <-proxyDone:
		t.Fatalf("first output arrived only after proxy completed: %+v", result)
	default:
	}
	select {
	case result := <-proxyDone:
		if result.code != 0 {
			t.Fatalf("live proxy failed %+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("live proxy did not finish")
	}
	select {
	case code := <-followDone:
		if code != 0 || stream.String() != "firstlast" || followErr.Len() != 0 {
			t.Fatalf("follow final drain %d %q %s", code, stream.String(), &followErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("follow did not finish")
	}
	code, out, stderr = call("run", "--quiet", "--cpu", "0.01", "--ttl", "5s", "--reason", "quiet leased fixture", "--", "/bin/sh", "-c", "printf leased-output; exit 3")
	var lease model.Lease
	if code != 3 || json.Unmarshal([]byte(out), &lease) != nil || stderr != "" {
		t.Fatalf("quiet leased run: %d %s %s", code, out, stderr)
	}
	code, out, stderr = call("log", lease.ID)
	if code != 0 || out != "leased-output" || stderr != "" {
		t.Fatalf("leased log: %d %q %q", code, out, stderr)
	}
}

func TestRunCommandsHelpAndValidationAreOffline(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"proxy", "--help"}, {"log", "--help"}, {"runs", "--help"}, {"run", "--help"}} {
		var out, errout bytes.Buffer
		if code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &errout); code != 0 {
			t.Fatalf("%v: %d %s %s", args, code, out.String(), errout.String())
		}
	}
	for _, args := range [][]string{{"proxy", "--", "/bin/echo", "bad"}, {"proxy", "--reason", "empty"}, {"log"}, {"log", "id", "--offset", "-1"}, {"runs", "unexpected"}} {
		var out, errout bytes.Buffer
		if code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &errout); code != 2 && code != 1 {
			t.Fatalf("bad args accepted %v: %d %s %s", args, code, out.String(), errout.String())
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("offline commands created state: %v", err)
	}
}

// The channel proves bytes reach the follower before the producing command exits.
type runStreamWriter struct {
	bytes.Buffer
	ready chan struct{}
}

func (w *runStreamWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	select {
	case w.ready <- struct{}{}:
	default:
	}
	return n, err
}
