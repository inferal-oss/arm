package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"getarm.org/internal/client"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
	"getarm.org/internal/server"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/spf13/pflag"
)

func completionCLI(t *testing.T, home string, args ...string) (string, string, int) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &diagnostic)
	return out.String(), diagnostic.String(), code
}

func TestCompletionPublicTreeAndAuthoritativeFlags(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	a := app{home: home, ctx: context.Background(), in: strings.NewReader(""), out: io.Discard, err: io.Discard}
	root := a.commandTree(true, func() error { return nil })
	count, flags := 0, 0
	for name := range commandHelps {
		if name == "arm" {
			continue
		}
		cmd, remaining, err := root.Find(strings.Fields(name))
		if err != nil || len(remaining) != 0 || cmd.CommandPath() != "arm "+name {
			t.Fatalf("missing public command %s: %v %v", name, remaining, err)
		}
		probe := a
		captured := false
		probe.catalogFlags = func(f *flag.FlagSet, _ bool) {
			captured = true
			f.VisitAll(func(v *flag.Flag) {
				flags++
				p := cmd.Flags().Lookup(v.Name)
				if p == nil || p.Usage != v.Usage || p.DefValue != v.DefValue {
					t.Errorf("%s --%s differs from the execution parser", name, v.Name)
				}
			})
		}
		_, _ = probe.command(append(strings.Fields(name), "--help"))
		if _, group := groupHelps[name]; !captured && !group && name != "mesh forward show" && name != "mesh ssh-relay show" {
			t.Errorf("public leaf %s did not reach its flag registration boundary", name)
		}
		count++
	}
	if count < 150 || flags < 900 {
		t.Fatalf("unexpectedly incomplete public tree: %d commands %d flags", count, flags)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("catalog wrote state", err)
	}
}

func TestCompletionRootFlagsMatchExecutionWithoutEffects(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	a := app{home: home, forceJSON: true, ctx: context.Background(), in: strings.NewReader(""), out: io.Discard, err: io.Discard}
	// Exercise metadata construction concurrently from the same application.
	// The root registration must bind private copies rather than resetting its
	// already selected execution defaults (especially forceJSON) or doing I/O.
	differences := make(chan []string, 2)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			root := a.commandTree(true, func() error { return nil })
			probe := a
			actual := probe.rootFlags()
			var missing []string
			actual.VisitAll(func(v *flag.Flag) {
				p := root.Flags().Lookup(v.Name)
				if p == nil || p.Usage != v.Usage || p.DefValue != v.DefValue || p.Value.String() != v.Value.String() {
					missing = append(missing, "root --"+v.Name+" differs from the execution FlagSet")
					return
				}
				noOption := ""
				if value, ok := v.Value.(interface{ IsBoolFlag() bool }); ok && value.IsBoolFlag() {
					noOption = "true"
				}
				if p.NoOptDefVal != noOption {
					missing = append(missing, "root --"+v.Name+" has different boolean parsing")
				}
			})
			root.Flags().VisitAll(func(v *pflag.Flag) {
				if actual.Lookup(v.Name) == nil {
					missing = append(missing, "root --"+v.Name+" is absent from the execution FlagSet")
				}
			})
			differences <- missing
		}()
	}
	workers.Wait()
	close(differences)
	for missing := range differences {
		for _, message := range missing {
			t.Error(message)
		}
	}
	if a.home != home || !a.forceJSON {
		t.Fatal("metadata registration changed execution defaults")
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("root catalog created state", err)
	}
}

func TestCompletionProtocolNestedFlagsAndOffline(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, tt := range []struct {
		args            []string
		present, absent string
	}{
		{[]string{"__complete", ""}, "completion\t", "_ssh-proxy"},
		{[]string{"__complete", "pool", ""}, "inspect\t", "_cargo-helper"},
		{[]string{"__complete", "mesh", ""}, "connect\t", "bootstrap"},
		{[]string{"__complete", "pool", "create", "--"}, "--parent-revision", "--listen"},
		{[]string{"__complete", "mesh", "kubernetes", "ship", "--"}, "--", "--not-a-real-flag"},
		{[]string{"__complete", "run", "--"}, "--cpu", "--listen"},
		{[]string{"__complete", "node", "--host", ""}, ":4", "backend unavailable"},
	} {
		out, diagnostic, code := completionCLI(t, home, tt.args...)
		if code != 0 || !strings.Contains(out, tt.present) || strings.Contains(out+diagnostic, tt.absent) {
			t.Errorf("%v code=%d stdout=%s stderr=%s", tt.args, code, out, diagnostic)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("completion initialized state", err)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out, diagnostic, code := completionCLI(t, home, "completion", shell)
		if code != 0 || diagnostic != "" || !strings.Contains(out, "__complete") || len(out) < 1000 {
			t.Fatalf("%s: %d %s %s", shell, code, out, diagnostic)
		}
	}
}

func completionEndpoint(t *testing.T, responder func(context.Context, model.WireRequest) (any, *model.Error)) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		var q model.WireRequest
		if wsjson.Read(r.Context(), c, &q) != nil {
			return
		}
		result, problem := responder(r.Context(), q)
		_ = wsjson.Write(r.Context(), c, model.WireResponse{Version: 1, ID: q.ID, Result: result, Error: problem})
	}))
	t.Cleanup(server.Close)
	home := t.TempDir()
	if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{URL: "ws" + strings.TrimPrefix(server.URL, "http"), Token: "test-secret"}); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCompletionAuthorizedIdentifiersNamesAndRefusals(t *testing.T) {
	var mu sync.Mutex
	var ops []string
	home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
		mu.Lock()
		ops = append(ops, q.Operation)
		mu.Unlock()
		switch q.Operation {
		case "node_list":
			return model.NodeRegistry{Nodes: []model.NodeEntry{{Host: "abc123", Name: "quiet-otter", Local: true}, {Host: "def456", Name: "bad$(touch /tmp/no)", Trust: model.NodeTrust{State: "active"}}, {Host: "remote", Name: "remote-node", Trust: model.NodeTrust{State: "active"}}, {Host: "revoked", Name: "hidden", Trust: model.NodeTrust{State: "revoked"}}, {Host: "active", Name: "revoked", Trust: model.NodeTrust{State: "active"}}}}, nil
		case "pool_list":
			return []model.PoolView{{Pool: model.Pool{ID: "pool_1", Name: "build-slots"}}, {Pool: model.Pool{ID: "pool_2", Name: "duplicate"}}, {Pool: model.Pool{ID: "pool_3", Name: "duplicate"}}}, nil
		case "status":
			return map[string]any{"leases": []map[string]string{{"id": "123456", "Name": "calm-cedar", "reason": "private reason"}}}, nil
		case "name_list":
			return nameSnapshot{Document: nameDocument()}, nil
		default:
			return nil, &model.Error{Code: "forbidden", Message: "private error credential"}
		}
	})
	for _, tt := range []struct {
		args         []string
		want, absent string
	}{
		{[]string{"__complete", "node", "--host", ""}, "quiet-otter\thost name", "hidden"},
		{[]string{"__complete", "pool", "inspect", ""}, "build-slots\tpool name", "duplicate"},
		{[]string{"__complete", "inspect", ""}, readablename.FromID(readablename.Lease, "123456") + "\tlease name", "private reason"},
		{[]string{"__complete", "inspect", "--host", "remote", ""}, ":4", "private error"},
		{[]string{"__complete", "inspect", "--host", "abc123", ""}, "123456\tlease ID", "private reason"},
		{[]string{"__complete", "inspect", "--host", readablename.FromID(readablename.Host, "abc123"), ""}, "123456\tlease ID", "private reason"},
		{[]string{"__complete", "node", "--host", "revoked"}, ":4", "revoked\thost name"},
		{[]string{"__complete", "run", "--pool", "build"}, "build-slots=\tpool name", "private reason"},
		{[]string{"__complete", "run", "--service", ""}, "123456\tlease ID", "private reason"},
		{[]string{"__complete", "mesh", "send", "quiet"}, "quiet-otter\thost name", "private reason"},
		{[]string{"__complete", "mesh", "forward", "grant", "--service", ""}, ":4", "123456"},
	} {
		out, diagnostic, code := completionCLI(t, home, tt.args...)
		if code != 0 || !strings.Contains(out, tt.want) || strings.Contains(out+diagnostic, tt.absent) || strings.Contains(out, "test-secret") || strings.Contains(out, "$(") {
			t.Fatalf("%v: %d %s %s", tt.args, code, out, diagnostic)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, op := range ops {
		if op != "node_list" && op != "pool_list" && op != "status" && op != "mesh_dispatch" && op != "name_list" {
			t.Fatal("completion attempted an effect", op)
		}
	}
	if len(ops) != 22 {
		data, _ := json.Marshal(ops)
		t.Fatalf("unexpected completion fanout: %s", data)
	}
}

func TestCompletionRootOptionsAndCommandBoundaries(t *testing.T) {
	home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
		if q.Operation == "name_list" {
			return nameSnapshot{Document: nameDocument()}, nil
		}
		if q.Operation != "node_list" {
			t.Error("unexpected read", q.Operation)
		}
		return model.NodeRegistry{Nodes: []model.NodeEntry{{Host: "local-id", Name: "local-name", Local: true}}}, nil
	})
	for _, prefix := range [][]string{{"--home", home}, {"--home=" + home}, {"-home", home}, {"--json", "--home", home}} {
		args := append([]string{"__complete"}, prefix...)
		args = append(args, "node", "--host", "")
		out, diagnostic, code := completionCLI(t, filepath.Join(t.TempDir(), "absent"), args...)
		if code != 0 || !strings.Contains(out, "local-name\thost name") {
			t.Fatalf("%v: %d %s %s", args, code, out, diagnostic)
		}
	}
	absent := filepath.Join(t.TempDir(), "absent")
	for _, tt := range []struct {
		args         []string
		want, absent string
	}{
		{[]string{"__complete", "run", "printf", "--"}, ":4\n", "--cpu"},
		{[]string{"__complete", "run", "--reason", "purpose", "--"}, "--cpu", "--listen"},
		{[]string{"__complete", "run", "-cpu", "1", "--p"}, "--pool", "--listen"},
		{[]string{"__complete", "ssh", "-key", "disable", "NODE", "--m"}, "--memory", "--listen"},
		{[]string{"__complete", "run", "--pool", "pool_1=1", "--p"}, "--pool", "--listen"},
		{[]string{"__complete", "run", "--port", "web=tcp4://127.0.0.1:0", "--p"}, "--port", "--listen"},
		{[]string{"__complete", "storage", "export", "--file", "output.bin", "--f"}, "--file", "--cpu"},
		{[]string{"__complete", "node", "--"}, "--host", "--home"},
		{[]string{"__complete", "pool", "adjust", "pool_1", "-1", "--r"}, "--reason", "unknown shorthand"},
		{[]string{"__complete", "pool", "adjust", "--revision", "3", "pool_1", "-01", "--r"}, "--reason", "unknown shorthand"},
		{[]string{"__complete", "pool", "adjust", "pool_1", "-9223372036854775808", "--j"}, "--json", "unknown shorthand"},
		{[]string{"__complete", "pool", "adjust", "pool_1", "-1", "--h"}, "--help", "unknown shorthand"},
		{[]string{"__complete", "pool", "adjust", "pool_1", "-1", "--", "--r"}, ":4", "--reason"},
	} {
		out, diagnostic, code := completionCLI(t, absent, tt.args...)
		if code != 0 || !strings.Contains(out, tt.want) || strings.Contains(out, tt.absent) {
			t.Fatalf("%v: %d %s %s", tt.args, code, out, diagnostic)
		}
	}
	out, _, _ := completionCLI(t, absent, "__complete", "")
	count := 0
	for _, line := range strings.Split(out, "\n") {
		name, _, _ := strings.Cut(line, "\t")
		if name == "help" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("root completion contains %d help commands: %s", count, out)
	}
}

func TestCompletionGeneratedShellArgumentsRemainLiteral(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			interpreter, err := exec.LookPath(shell)
			if err != nil {
				t.Skip("shell is unavailable")
			}
			dir := t.TempDir()
			record := filepath.Join(dir, "arguments")
			sentinel := filepath.Join(dir, "executed")
			fake := filepath.Join(dir, "arm")
			if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARM_COMPLETION_ARGS\"\nprintf 'create\\tCreate pool\\nrack:one\\tHost name\\n:4\\n'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			a := app{home: dir, ctx: context.Background(), in: strings.NewReader(""), out: io.Discard, err: io.Discard}
			var generated bytes.Buffer
			if err := generateCompletionScript(a.commandTree(true, func() error { return nil }), shell, &generated); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "completion")
			if err := os.WriteFile(path, generated.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			literal := "state with spaces; touch " + sentinel
			script := ""
			options := []string{}
			switch shell {
			case "bash":
				options = []string{"--noprofile", "--norc"}
				script = `source "$ARM_COMPLETION_SCRIPT"
words=(arm --home "$ARM_COMPLETION_LITERAL" pool cr)
cur=cr
__arm_get_completion_results
[[ $out == *rack:one* ]] || exit 92
__arm_process_completion_results
printf '%s\n' "${COMPREPLY[@]}"
words=(arm --home "$ARM_COMPLETION_LITERAL" pool rack)
cur=rack
__arm_get_completion_results
__arm_process_completion_results
found=0
for candidate in "${COMPREPLY[@]}"; do
    [[ $candidate == rack:one* ]] && found=1
done
[[ $found == 1 ]] || exit 93
printf '%s\n' "${COMPREPLY[@]}"`
			case "zsh":
				options = []string{"-f"}
				script = `autoload -Uz compinit
compinit -D
source "$ARM_COMPLETION_SCRIPT"
_describe() { print -rl -- "${completions[@]}"; return 0 }
words=(arm --home "$ARM_COMPLETION_LITERAL" pool cr)
CURRENT=${#words[@]}
_arm`
			case "fish":
				options = []string{"--no-config"}
				script = `source "$ARM_COMPLETION_SCRIPT"
set -l quoted (string escape -- "$ARM_COMPLETION_LITERAL")
complete -C "arm --home $quoted pool cr"`
			}
			command := exec.Command(interpreter, append(options, "-c", script)...)
			command.Dir = dir
			command.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "ARM_COMPLETION_SCRIPT="+path, "ARM_COMPLETION_ARGS="+record, "ARM_COMPLETION_LITERAL="+literal)
			output, err := command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "create") {
				t.Fatalf("shell completion failed: %v %s", err, output)
			}
			raw, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			last := "cr"
			if shell == "bash" {
				last = "rack"
			}
			if string(raw) != "__complete\n--home\n"+literal+"\npool\n"+last+"\n" {
				t.Fatalf("argument boundaries changed: %q", raw)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatal("literal completion argument was executed", err)
			}
		})
	}
}

func TestCompletionEndpointRejectsUnboundedMetadata(t *testing.T) {
	for _, kind := range []string{"fifo", "oversized", "symlink", "public"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, "endpoint.json")
			switch kind {
			case "fifo":
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(path, []byte(strings.Repeat("x", (16<<10)+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("missing", path); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.WriteFile(path, []byte(`{"token":"secret"}`), 0644); err != nil {
					t.Fatal(err)
				}
			}
			start := time.Now()
			out, diagnostic, code := completionCLI(t, home, "__complete", "node", "--host", "")
			if code != 0 || !strings.Contains(out, ":4") || strings.Contains(out+diagnostic, "secret") || time.Since(start) > 800*time.Millisecond {
				t.Fatalf("%s: %d %s %s (%s)", kind, code, out, diagnostic, time.Since(start))
			}
		})
	}
}

func TestCompletionDeadlineAndExplicitHome(t *testing.T) {
	home := completionEndpoint(t, func(ctx context.Context, q model.WireRequest) (any, *model.Error) {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
		return nil, &model.Error{Code: "forbidden", Message: "secret"}
	})
	start := time.Now()
	out, diagnostic, code := completionCLI(t, home, "__complete", "node", "--host", "")
	if code != 0 || !strings.Contains(out, ":4") || strings.Contains(out+diagnostic, "secret") || time.Since(start) > 800*time.Millisecond {
		t.Fatalf("completion exceeded deadline: %s %d %s %s", time.Since(start), code, out, diagnostic)
	}
}

func TestCompletionDisposableBackendIntegration(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("disposable backend did not join")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := local.ReadEndpoint(home); err == nil {
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatal("disposable backend failed", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("disposable backend was not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	callCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	capacity := int64(1)
	var pool model.PoolChange
	if err := client.Call(callCtx, home, "pool_mutate", model.PoolMutation{Operation: "create", Name: "completion-slots", Scope: "global", Capacity: &capacity, Reason: "Verify isolated completion", Key: "completion-pool"}, &pool); err != nil {
		t.Fatal(err)
	}
	var lease model.Lease
	if err := client.Call(callCtx, home, "acquire", model.Request{Host: "localhost", TTL: time.Minute, Reason: "Verify isolated completion", Key: "completion-lease", Pools: []model.PoolClaim{{Pool: pool.Pool.ID, Quantity: 1}}}, &lease); err != nil {
		t.Fatal(err)
	}
	var status model.Status
	if err := client.Call(callCtx, home, "status", nil, &status); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"__complete", "pool", "inspect", ""}, pool.Pool.ID + "\tpool ID"},
		{[]string{"__complete", "pool", "inspect", ""}, readablename.FromID(readablename.Pool, pool.Pool.ID) + "\tpool name"},
		{[]string{"__complete", "inspect", ""}, lease.ID + "\tlease ID"},
		{[]string{"__complete", "inspect", ""}, readablename.FromID(readablename.Lease, lease.ID) + "\tlease name"},
		{[]string{"__complete", "node", "--host", ""}, readablename.FromID(readablename.Host, status.Host) + "\thost name"},
		{[]string{"__complete", "inspect", "--host", readablename.FromID(readablename.Host, status.Host), ""}, lease.ID + "\tlease ID"},
	} {
		out, diagnostic, code := completionCLI(t, home, tt.args...)
		if code != 0 || !strings.Contains(out, tt.want) {
			t.Fatalf("%v: %d %s %s", tt.args, code, out, diagnostic)
		}
	}
	if err := client.Call(callCtx, home, "release", map[string]string{"id": lease.ID}, nil); err != nil {
		t.Fatal(err)
	}
}
