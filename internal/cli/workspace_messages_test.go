package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inferal-oss/arm/internal/cli"
)

func TestWorkspaceMessagesOfflineHelpAndExactSelectors(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, action := range []string{"send", "reply", "inbox", "outbox", "message-lookup", "message-cancel", "messages-retire"} {
		for _, prefix := range []bool{false, true} {
			args := []string{"workspaces", action, "--help"}
			if prefix {
				args = []string{"help", "workspaces", action}
			}
			var out, stderr bytes.Buffer
			status := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
			if status != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "Usage: arm workspaces "+action) || !strings.Contains(out.String(), "--workspace") {
				t.Fatal(args, status, out.String(), stderr.String())
			}
			if strings.Contains(out.String(), "\n  --agent ") || strings.Contains(out.String(), "\n  --session ") || strings.Contains(out.String(), "\n  --host ") {
				t.Fatal("host send exposed impersonation or unrelated selector", out.String())
			}
		}
	}
	for _, args := range [][]string{{"send"}, {"send", "--workspace", "missing", "--key", "exact", "--to", "other", "--text", "body", "--ttl", "25h"}, {"reply", "--workspace", "missing", "--key", "exact", "--text", "body"}, {"inbox", "--workspace", "missing", "--limit", "21"}, {"message-lookup", "--workspace", "missing"}, {"message-cancel", "--workspace", "missing", "--key", "key", "--revision", "0"}} {
		var out, stderr bytes.Buffer
		code := cli.Main(append([]string{"--home", home, "workspaces"}, args...), strings.NewReader(""), &out, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), "invalid_request") {
			t.Fatal(args, code, out.String(), stderr.String())
		}
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("offline help/validation initialized state", e)
	}
}
