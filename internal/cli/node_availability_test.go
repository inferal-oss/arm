package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"getarm.org/internal/customnames"
	"getarm.org/internal/model"
	"getarm.org/internal/readablename"
)

func TestNodeAvailabilityPreparedCLIRejectsRemoteMutationBeforeState(t *testing.T) {
	for _, args := range [][]string{
		{"stray"},
		{"stray", "--host", "localhost"},
		{"set", "stray", "--profile", "stable", "--expected-revision", "0", "--key", "original", "--reason", "owner declaration"},
		{"set", "--host", "remote-peer", "--profile", "intermittent", "--expected-revision", "0", "--key", "original", "--reason", "owner declaration"},
		{"set", "--profile", "intermittent", "--key", "original", "--reason", "owner declaration"},
		{"set", "--profile", "guaranteed", "--expected-revision", "0", "--key", "original", "--reason", "owner declaration"},
	} {
		var out, stderr bytes.Buffer
		home := filepath.Join(t.TempDir(), "absent")
		a := app{home: home, ctx: context.Background(), out: &out, err: &stderr}
		if code, err := a.nodeAvailability(args); code != 2 || err == nil {
			t.Fatal("invalid owner update accepted", args, code, err)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("rejected owner update wrote state", err)
		}
	}
}

func TestNodeAvailabilityActivatedHelpIsSideEffectFree(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, command := range []string{"node availability", "node availability set"} {
		for _, args := range [][]string{append(strings.Fields(command), "--help"), append([]string{"help"}, strings.Fields(command)...)} {
			var out, stderr bytes.Buffer
			code := Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
			if code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "Usage: arm "+command) {
				t.Fatalf("help unavailable: %v %d %s %s", args, code, &out, &stderr)
			}
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("help created state", err)
	}
}

func TestNodeAvailabilityCompletionUsesOnlyFixedOfflineChoices(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"__complete", "node", "availability", "set", "--profile", ""}, "unspecified\nstable\nintermittent\n:4"},
		{[]string{"__complete", "node", "availability", "set", "--profile", "st"}, "stable\n:4"},
		{[]string{"__complete", "node", "availability", "set", "--host", ""}, "localhost\n:4"},
	} {
		out, diagnostic, code := completionCLI(t, home, tt.args...)
		got, want := strings.Split(strings.TrimSpace(out), "\n"), strings.Split(tt.want, "\n")
		sort.Strings(got)
		sort.Strings(want)
		if code != 0 || diagnostic != "Completion ended with directive: ShellCompDirectiveNoFileComp\n" || strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("offline availability completion %v: %d %q %q", tt.args, code, out, diagnostic)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("completion created state", err)
	}
}

func TestNodeAvailabilityReadResolvesAliasesToExactHost(t *testing.T) {
	for _, mode := range []string{"custom", "generated", "ambiguous", "exact"} {
		t.Run(mode, func(t *testing.T) {
			localName, target := "Local node", "MobilePeer"
			entry := customnames.Entry{Kind: "host", AuthorityHost: namePeer, ID: namePeer, Name: "MobilePeer", Active: true}
			entries := []customnames.Entry{entry}
			if mode == "generated" {
				target = readablename.FromID(readablename.Host, namePeer)
				entries = nil
			}
			if mode == "ambiguous" {
				localName = target
			}
			if mode == "exact" {
				target = namePeer
			}
			var mu sync.Mutex
			var calls, reads []string
			home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, q.Operation)
				switch q.Operation {
				case "node_list":
					return model.NodeRegistry{Nodes: []model.NodeEntry{{Host: nameHost, Name: localName, Local: true}, {Host: namePeer, Name: "Enrolled peer", Trust: model.NodeTrust{State: "active"}}}}, nil
				case "name_list":
					return nameSnapshot{Document: nameDocument(entries...)}, nil
				case "status":
					return model.Status{Host: nameHost}, nil
				case "node_availability_read_v1":
					var params struct {
						Host string `json:"host"`
					}
					if err := json.Unmarshal(q.Params, &params); err != nil {
						t.Error(err)
					}
					reads = append(reads, params.Host)
					return map[string]any{"schema": 1, "host": params.Host}, nil
				default:
					t.Error("unexpected availability alias operation", q.Operation)
					return nil, &model.Error{Code: "forbidden"}
				}
			})
			a := customNameApp(home)
			code, err := a.nodeAvailability([]string{"--host", target})
			mu.Lock()
			defer mu.Unlock()
			if mode == "ambiguous" {
				if code != 1 || customNameCode(err) != "host_unavailable" || len(reads) != 0 {
					t.Fatal("ambiguous alias reached availability read", code, err, reads)
				}
				return
			}
			if code != 0 || err != nil || len(reads) != 1 || reads[0] != namePeer {
				t.Fatal("availability alias was not canonicalized", mode, code, err, reads)
			}
			if mode == "exact" && (len(calls) != 1 || calls[0] != "node_availability_read_v1") {
				t.Fatal("exact host ID performed alias inventory reads", calls)
			}
		})
	}
}

func TestNodeAvailabilityReadHostCompletionUsesAuthorizedInventory(t *testing.T) {
	t.Run("authorized", func(t *testing.T) {
		var mu sync.Mutex
		var operations []string
		home := completionEndpoint(t, func(_ context.Context, q model.WireRequest) (any, *model.Error) {
			mu.Lock()
			defer mu.Unlock()
			operations = append(operations, q.Operation)
			switch q.Operation {
			case "node_list":
				return model.NodeRegistry{Nodes: []model.NodeEntry{{Host: nameHost, Local: true}, {Host: namePeer, Trust: model.NodeTrust{State: "active"}}}}, nil
			case "name_list":
				entry := customnames.Entry{Kind: "host", AuthorityHost: namePeer, ID: namePeer, Name: "MobilePeer", Active: true}
				return nameSnapshot{Document: nameDocument(entry)}, nil
			default:
				t.Error("host completion attempted another operation", q.Operation)
				return nil, &model.Error{Code: "forbidden"}
			}
		})
		out, diagnostic, code := completionCLI(t, home, "__complete", "node", "availability", "--host", "")
		if code != 0 || diagnostic != "Completion ended with directive: ShellCompDirectiveNoFileComp\n" {
			t.Fatal(code, out, diagnostic)
		}
		for _, want := range []string{namePeer + "\thost ID", readablename.FromID(readablename.Host, namePeer) + "\thost name", "MobilePeer\thost name"} {
			if !strings.Contains(out, want+"\n") {
				t.Fatal("authorized host suggestion missing", want, out)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if len(operations) != 2 || operations[0] != "node_list" || operations[1] != "name_list" {
			t.Fatal("host completion changed read-only inventory path", operations)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		home := filepath.Join(t.TempDir(), "absent")
		out, diagnostic, code := completionCLI(t, home, "__complete", "node", "availability", "--host", "")
		if code != 0 || out != ":4\n" || diagnostic != "Completion ended with directive: ShellCompDirectiveNoFileComp\n" {
			t.Fatal("unavailable backend supplied host choices", code, out, diagnostic)
		}
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Fatal("unavailable host completion created state", err)
		}
	})
}
