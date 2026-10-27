package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/cli"
)

func TestLifecycleConflictingFlagsFailBeforeBackendAccess(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"release"}, {"cancel"}, {"release", "ID", "--all"}, {"cancel", "--all", "ID"}, {"release", "ID", "--key", "key"}, {"stop", "--force", "--wait"}, {"stop", "--timeout", "1"}, {"stop", "--wait", "--timeout", "0"}, {"stop", "--wait", "--timeout", "-1"}, {"stop", "--wait", "--timeout", "9223372036854775807"}} {
		var out, stderr bytes.Buffer
		code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr)
		if code != 2 || !json.Valid(stderr.Bytes()) || strings.Contains(stderr.String(), "backend unavailable") {
			t.Fatalf("bad lifecycle flags reached backend: %v code=%d out=%s error=%s", args, code, &out, &stderr)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("invalid flags created state: %v", err)
	}
}

func TestLifecycleHelpExplainsBulkAndDrainModes(t *testing.T) {
	for _, command := range []string{"release", "cancel", "stop", "daemon stop"} {
		var out, stderr bytes.Buffer
		args := append(strings.Fields(command), "--help")
		if code := cli.Main(args, strings.NewReader(""), &out, &stderr); code != 0 {
			t.Fatal(stderr.String())
		}
		options := []string{"--all", "--key"}
		if strings.Contains(command, "stop") {
			options = []string{"--wait", "--force", "--timeout"}
		}
		for _, option := range options {
			if !strings.Contains(out.String(), option) {
				t.Fatalf("help omitted %s: %s", option, out.String())
			}
		}
	}
}
