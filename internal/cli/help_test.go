package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/cli"
)

func TestAllCommandHelpIsAvailableWithoutBackendOrSideEffects(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	commands := []string{"", "acquire", "run", "storage create", "storage list", "storage inspect", "storage cleanup", "storage handoff", "ports reserve", "ports list", "ports discover", "pool create", "pool list", "pool inspect", "pool history", "pool set", "pool adjust", "pool rename", "pool describe", "pool drain", "pool resume", "pool delete", "pool", "usage", "inspect", "release", "cancel", "reconcile", "renew", "status", "resources", "messages", "events", "daemon start", "daemon stop", "daemon status", "serve", "agent probe", "agent register", "agent list", "agent inbox", "agent watch", "agent receive", "agent ack", "message operator", "message send", "message broadcast", "install", "uninstall", "version", "storage", "ports", "daemon", "agent", "message"}
	for _, command := range append(commands, "incidents", "start", "stop", "agents", "monitor") {
		t.Run(command, func(t *testing.T) {
			for _, spelling := range []string{"--help", "-h", "topic"} {
				args := strings.Fields(command)
				if spelling == "topic" {
					args = append([]string{"help"}, args...)
				} else {
					args = append(args, spelling)
				}
				var out, stderr bytes.Buffer
				code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
				if code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "Usage: arm") {
					t.Fatalf("%v: code=%d stdout=%s stderr=%s", args, code, &out, &stderr)
				}
			}
		})
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("help created state: %v", err)
	}
}

func TestHelpOnlyListsOptionsThatApplyToTheCommand(t *testing.T) {
	tests := []struct {
		command         string
		present, absent []string
	}{
		{"ports reserve", []string{"--port ", "--reason ", "OS-selected"}, []string{"--cpu", "--memory", "--disk", "--storage", "--port-lease", "--cwd", "--profile"}},
		{"storage create", []string{"SIZE", "--disk", "--reason"}, []string{"--cpu", "--memory", "--port", "--cwd", "--profile", "--storage"}},
		{"daemon stop", []string{"--json"}, []string{"--cpu", "--memory", "--disk", "--listen"}},
		{"release", []string{"LEASE_ID"}, []string{"--ttl"}},
		{"storage handoff", []string{"Preserves remaining data"}, []string{"--dry-run"}},
		{"agent inbox", []string{"--after", "--pending"}, []string{"--once", "--message", "--text"}},
		{"agent receive", []string{"--message"}, []string{"--text", "--wait", "--after", "--once"}},
		{"agent ack", []string{"--message", "--text"}, []string{"--wait", "--after", "--once"}},
		{"agent list", []string{"--json"}, []string{"--session", "--idle", "--adapter"}},
		{"agents", []string{"offline", "--json", "arm agent list"}, []string{"--session", "--idle", "--adapter"}},
		{"message operator", []string{"--text"}, []string{"--to"}},
		{"uninstall", []string{"manifest", "--dry-run"}, []string{"--codex-home", "--claude-home"}},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			var out, stderr bytes.Buffer
			args := append(strings.Fields(tt.command), "--help")
			if code := cli.Main(args, strings.NewReader(""), &out, &stderr); code != 0 {
				t.Fatalf("help failed: %s", &stderr)
			}
			for _, want := range tt.present {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q in %s", want, &out)
				}
			}
			for _, unwanted := range tt.absent {
				if strings.Contains(out.String(), unwanted) {
					t.Errorf("irrelevant %q in %s", unwanted, &out)
				}
			}
		})
	}
}

func TestInvalidCLIArgumentsFailBeforeBackendOrFilesystemWork(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	tests := []string{
		"ports reserve --storage fixture --reason test", "ports reserve --cpu 1 --reason test",
		"storage create 10GiB --disk 1GiB --reason test", "storage create 10GiB extra --reason test",
		"storage create 10GiB --reason --ttl 1m", "storage create 10GiB --reason", "storage create 10GiB",
		"ports reserve 0 --port tcp4://127.0.0.1:0 --reason test", "ports reserve --reason --",
		"release fixture extra", "release fixture --ttl 1m", "renew fixture", "renew fixture --ttl -1m",
		"storage handoff fixture --dry-run", "storage cleanup fixture extra", "daemon stop --cpu 1",
		"install extra", "uninstall --codex-home ignored", "agent list --session ignored",
		"message operator --to ignored --text test", "status extra", "events extra", "version extra",
		"agent inbox --once", "agent receive --text ignored", "agent ack --after ignored", "agent watch unexpected",
		"pool create slots --capacity 1 --reason test", "pool create slots --scope global --reason test",
		"pool create slots --scope global --host localhost --capacity 1 --reason test", "pool set ID 2 --reason test",
		"pool adjust ID 0 --reason test", "pool list --reason ignored", "pool inspect", "pool history ID extra", "pool adjust ID 1 --revision 0 --reason test",
		"usage", "usage ID extra", "usage ID --cpu 1", "incidents extra", "incidents --cpu 1", "run --reason test --usage-grace 1s -- echo test", "acquire --reason test --usage-policy revoke", "status --json=invalid", "--home", "--no-such-option", "agent register --id",
		"start extra", "stop extra", "stop --cpu 1", "start --reason ignored",
		"agents extra", "agents --adapter codex",
		"monitor extra", "monitor --cpu 1", "monitor --after 1", "monitor --after -1 --epoch fixture",
	}
	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			var out, stderr bytes.Buffer
			code := cli.Main(append([]string{"--home", home}, strings.Fields(command)...), strings.NewReader(""), &out, &stderr)
			if code != 2 || out.Len() != 0 || stderr.Len() == 0 || strings.Contains(stderr.String(), "endpoint.json") {
				t.Fatalf("invalid arguments reached backend or wrong status: %d %s %s", code, &out, &stderr)
			}
		})
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("invalid arguments created state: %v", err)
	}
}
