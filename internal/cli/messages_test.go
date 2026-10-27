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
	"getarm.org/internal/manager"
	"getarm.org/internal/model"
	"getarm.org/internal/server"
)

func TestCLIMessageKeysDeduplicateReviewedRecipients(t *testing.T) {
	home := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, home, "127.0.0.1:0", model.Resources{CPU: 1}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
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
		var out, stderr bytes.Buffer
		code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
		return code, out.String(), stderr.String()
	}
	if code, _, stderr := call("agent", "register", "--id", "recipient", "--adapter", "generic", "--delivery", "poll", "--cwd", t.TempDir()); code != 0 {
		t.Fatal(stderr)
	}
	for _, action := range []string{"send", "broadcast"} {
		args := []string{"message", action, "--text", "Operator retry fixture", "--key", action + "-stable-key"}
		if action == "send" {
			args = append(args, "--to", "recipient")
		}
		var first manager.Message
		for attempt := range 2 {
			code, out, stderr := call(args...)
			var msg manager.Message
			if code != 0 || json.Unmarshal([]byte(out), &msg) != nil || msg.Key != action+"-stable-key" {
				t.Fatalf("send output: %d %s %s", code, out, stderr)
			}
			if attempt == 0 {
				first = msg
			} else if first.ID != msg.ID {
				t.Fatal("CLI retry duplicated message")
			}
		}
		args = append(args, "--text", "Changed payload")
		if code, _, stderr := call(args...); code == 0 || !strings.Contains(stderr, "idempotency_conflict") || !strings.Contains(stderr, action+"-stable-key") {
			t.Fatal("changed payload reused key", stderr)
		}
	}
	code, out, stderr := call("messages")
	var messages []manager.Message
	if code != 0 || json.Unmarshal([]byte(out), &messages) != nil || len(messages) != 2 {
		t.Fatalf("duplicate messages: %s %s", out, stderr)
	}
}
