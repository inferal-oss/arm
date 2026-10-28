package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

func runCompletionTokens(t *testing.T, out string) []string {
	t.Helper()
	if !strings.HasSuffix(out, ":4\n") {
		t.Fatalf("missing no-file-completion directive: %q", out)
	}
	var tokens []string
	for _, line := range strings.Split(strings.TrimSuffix(out, ":4\n"), "\n") {
		if line == "" {
			continue
		}
		token, description, _ := strings.Cut(line, "\t")
		if !completionToken(token) || description != "" && description != "run ID" && description != "run name" {
			t.Fatalf("unsafe run suggestion: %q", line)
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func TestCompletionRunsSelectors(t *testing.T) {
	leased, proxy := strings.Repeat("1", 32), strings.Repeat("2", 32)
	name := func(id string) string { return readablename.FromID("run", id) }
	runs := []model.Run{
		{ID: leased, Name: name(leased), LeaseID: leased, Command: []string{"PRIVATE_COMMAND"}, CWD: "/PRIVATE_CWD", Reason: "PRIVATE_REASON", Agent: "PRIVATE_AGENT", State: "terminal"},
		{ID: proxy, Name: name(proxy), Command: []string{"PRIVATE_PROXY_COMMAND"}, Reason: "PRIVATE_PROXY_REASON", State: "active"},
	}
	var mu sync.Mutex
	var ops []string
	home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
		mu.Lock()
		ops = append(ops, q.Operation)
		mu.Unlock()
		if q.Operation != "runs" || string(q.Params) != "null" {
			return nil, &model.Error{Code: "forbidden", Message: "PRIVATE_ERROR"}
		}
		return runs, nil
	})
	all := []string{leased, proxy, name(leased), name(proxy)}
	slices.Sort(all)
	for _, tt := range []struct {
		name string
		args []string
		want []string
	}{
		{"all", []string{"__complete", "log", ""}, all},
		{"short-follow", []string{"__complete", "log", "-f", ""}, all},
		{"long-follow-offset", []string{"__complete", "log", "--follow", "--offset", "7", ""}, all},
		{"inline-offset", []string{"__complete", "log", "--offset=7", ""}, all},
		{"id-prefix", []string{"__complete", "log", leased[:6]}, []string{leased}},
		{"readable-prefix", []string{"__complete", "log", name(proxy)}, []string{name(proxy)}},
		{"no-descriptions", []string{"__completeNoDesc", "log", ""}, all},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, diagnostic, code := completionCLI(t, home, tt.args...)
			if code != 0 || strings.Contains(out+diagnostic, "PRIVATE_") || strings.Contains(out+diagnostic, "test-secret") {
				t.Fatalf("completion leaked metadata or failed: %d %q %q", code, out, diagnostic)
			}
			if got := runCompletionTokens(t, out); !slices.Equal(got, tt.want) {
				t.Fatalf("suggestions %q, want %q", got, tt.want)
			}
			if strings.Contains(out, readablename.FromID(readablename.Lease, leased)) {
				t.Fatal("offered a lease-domain alias that run lookup does not resolve")
			}
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ops) != 7 {
		t.Fatalf("want one inventory read per completion, got %q", ops)
	}
	for _, op := range ops {
		if op != "runs" {
			t.Fatal("completion read logs or attempted an effect", op)
		}
	}
}

func TestCompletionRunsSanitizesNames(t *testing.T) {
	id := strings.Repeat("3", 32)
	for i, unsafe := range []string{"bad name", "bad\nname", "bad\tname", "$(command)", "'quoted'", "-option", "non-ASCII-雪", strings.Repeat("x", 129), strings.Repeat("f", 32)} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
				return []model.Run{{ID: id, Name: unsafe}}, nil
			})
			out, _, code := completionCLI(t, home, "__complete", "log", "")
			want := []string{id, readablename.FromID("run", id)}
			slices.Sort(want)
			if code != 0 || !slices.Equal(runCompletionTokens(t, out), want) {
				t.Fatalf("unsafe name entered protocol or hid exact ID: %d %q", code, out)
			}
		})
	}
}

func TestCompletionRunsAmbiguity(t *testing.T) {
	ids := []string{strings.Repeat("4", 32), strings.Repeat("5", 32), strings.Repeat("6", 32), strings.Repeat("7", 32)}
	alias := readablename.FromID("run", ids[0])
	runs := []model.Run{{ID: ids[0], Name: "duplicate"}, {ID: ids[1], Name: "duplicate"}, {ID: ids[2], Name: alias}, {ID: ids[3], Name: ids[0]}, {ID: ids[3], Name: "unambiguous"}, {ID: ids[3], Name: "unambiguous"}}
	home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) { return runs, nil })
	out, _, code := completionCLI(t, home, "__complete", "log", "")
	tokens := runCompletionTokens(t, out)
	if code != 0 || slices.Contains(tokens, "duplicate") || slices.Contains(tokens, alias) || !slices.Contains(tokens, "unambiguous") {
		t.Fatalf("wrong ambiguity handling: %q", out)
	}
	for _, id := range ids {
		if !slices.Contains(tokens, id) {
			t.Fatalf("ambiguous names hid canonical ID %s", id)
		}
	}
}

func TestCompletionRunsInventoryBounds(t *testing.T) {
	for _, tt := range []struct {
		name   string
		count  int
		prefix string
		want   int
	}{
		{"sorted-output-cap", 300, "", 256},
		{"collision-beyond-output-cap", 300, "duplicate_run_alias", 0},
		{"oversized-inventory", 4097, "", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runs := make([]model.Run, tt.count)
			for i := range runs {
				runs[i].ID = fmt.Sprintf("%032x", i+1)
			}
			runs[0].Name, runs[len(runs)-1].Name = "duplicate_run_alias", "duplicate_run_alias"
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) { return runs, nil })
			out, _, code := completionCLI(t, home, "__complete", "log", tt.prefix)
			got := runCompletionTokens(t, out)
			if code != 0 || len(got) != tt.want || !slices.IsSorted(got) {
				t.Fatalf("wrong bounded suggestions: code=%d count=%d", code, len(got))
			}
		})
	}
}

func TestCompletionRunsUnavailable(t *testing.T) {
	for _, name := range []string{"missing", "empty", "stopped"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if name == "missing" {
				home = filepath.Join(home, "absent")
			}
			if name == "stopped" {
				s := httptest.NewServer(http.NotFoundHandler())
				s.Close()
				if err := local.AtomicJSON(filepath.Join(home, "endpoint.json"), local.Endpoint{URL: "ws" + strings.TrimPrefix(s.URL, "http"), Token: "PRIVATE_TOKEN"}); err != nil {
					t.Fatal(err)
				}
			}
			out, diagnostic, code := completionCLI(t, home, "__complete", "log", "")
			if code != 0 || len(runCompletionTokens(t, out)) != 0 || strings.Contains(out+diagnostic, "PRIVATE_TOKEN") {
				t.Fatalf("unavailable inventory: %d %q %q", code, out, diagnostic)
			}
			files, err := os.ReadDir(home)
			if name == "missing" {
				if !os.IsNotExist(err) {
					t.Fatal("completion created a state directory", err)
				}
			} else if err != nil || name == "empty" && len(files) != 0 || name == "stopped" && (len(files) != 1 || files[0].Name() != "endpoint.json") {
				t.Fatal("completion initialized backend state", files, err)
			}
		})
	}
	for _, name := range []string{"denied", "malformed", "invalid-id"} {
		t.Run(name, func(t *testing.T) {
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
				switch name {
				case "denied":
					return nil, &model.Error{Code: "forbidden", Message: "PRIVATE_ERROR"}
				case "malformed":
					return "PRIVATE_BAD_RESPONSE", nil
				default:
					return []model.Run{{ID: "invalid\nidentifier", Name: "safe-looking"}}, nil
				}
			})
			out, diagnostic, code := completionCLI(t, home, "__complete", "log", "")
			if code != 0 || len(runCompletionTokens(t, out)) != 0 || strings.Contains(out+diagnostic, "PRIVATE_") {
				t.Fatalf("failed inventory leaked suggestions: %d %q %q", code, out, diagnostic)
			}
		})
	}
}

func TestCompletionRunsDeadline(t *testing.T) {
	home := completionEndpoint(t, func(ctx context.Context, q model.WireRequest) (any, *model.Error) {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
		return []model.Run{{ID: strings.Repeat("8", 32), Name: "too-late"}}, nil
	})
	start := time.Now()
	out, diagnostic, code := completionCLI(t, home, "__complete", "log", "")
	if elapsed := time.Since(start); code != 0 || len(runCompletionTokens(t, out)) != 0 || strings.Contains(out+diagnostic, "too-late") || elapsed > 800*time.Millisecond {
		t.Fatalf("run inventory escaped completion deadline: %s %d %q %q", elapsed, code, out, diagnostic)
	}
}

func TestCompletionRunFlagsAndBoundariesOffline(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, tt := range []struct{ command, flag string }{{"proxy", "--quiet"}, {"run", "--quiet"}, {"log", "--follow"}, {"log", "--offset"}} {
		out, _, code := completionCLI(t, home, "__complete", tt.command, "--")
		if code != 0 || !strings.Contains(out, tt.flag+"\t") {
			t.Fatalf("existing flag missing: %s %s %d %q", tt.command, tt.flag, code, out)
		}
	}
	for _, args := range [][]string{
		{"__complete", "log", strings.Repeat("1", 32), ""},
		{"__complete", "log", "--offset", ""},
		{"__complete", "proxy", "--quiet", "--", "printf", ""},
		{"__complete", "run", "--quiet", "--", "printf", ""},
	} {
		out, _, code := completionCLI(t, home, args...)
		if code != 0 || len(runCompletionTokens(t, out)) != 0 {
			t.Fatalf("completion crossed argument boundary: %v %d %q", args, code, out)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("offline flags/boundaries created state", err)
	}
}
