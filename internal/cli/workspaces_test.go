package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/cli"
)

func TestWorkspacesHelpOfflineClosedScope(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	topics := []string{"", "list", "inspect", "lookup", "create", "join", "decide", "invitation", "consent", "admit", "refresh", "finalize", "resume-join", "resume-admission", "recovery-decide", "recovery-run", "deny", "revoke", "retire", "cancel-join", "cancel-admission", "cancel-recovery"}
	for _, topic := range topics {
		for _, prefix := range []bool{false, true} {
			t.Run(topic, func(t *testing.T) {
				args := []string{"workspaces"}
				if topic != "" {
					args = append(args, topic)
				}
				if prefix {
					args = append([]string{"help"}, args...)
				} else {
					args = append(args, "--help")
				}
				var out, stderr bytes.Buffer
				status := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
				if status != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "Usage: arm workspaces") {
					t.Fatal(args, status, out.String(), stderr.String())
				}
			})
		}
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("help initialized state", e)
	}
}
func TestWorkspacesHelpKeepsSelectorsScoped(t *testing.T) {
	for _, q := range []struct {
		topic        string
		want, absent []string
	}{{"admit", []string{"--consent", "--review", "--yes", "Known-key"}, []string{"--permit-proof", "--original", "--source"}}, {"resume-admission", []string{"--original", "--permit-proof", "--consent"}, []string{"--approve", "--review"}}, {"refresh", []string{"--workspace"}, []string{"--key", "--reason", "--approve"}}} {
		var out, stderr bytes.Buffer
		if status := cli.Main([]string{"workspaces", q.topic, "--help"}, strings.NewReader(""), &out, &stderr); status != 0 {
			t.Fatal(status, stderr.String())
		}
		for _, w := range q.want {
			if !strings.Contains(out.String(), w) {
				t.Fatal("missing", w, out.String())
			}
		}
		for _, w := range q.absent {
			if strings.Contains(out.String(), "\n  "+w) {
				t.Fatal("irrelevant", w, out.String())
			}
		}
	}
}
