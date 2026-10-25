package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeshStopRejectsExplicitInvalidTimeoutAndLookupEditsBeforeBackend(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"zero", []string{"--timeout", "0"}},
		{"no-wait", []string{"--timeout", "1"}},
		{"negative", []string{"--wait", "--timeout", "-1"}},
		{"force-wait", []string{"--force", "--wait"}},
		{"lookup-force", []string{"--lookup", "--key", "old", "--force"}},
		{"lookup-timeout", []string{"--lookup", "--key", "old", "--wait", "--timeout", "1"}},
		{"lookup-reason", []string{"--lookup", "--key", "old", "--reason", "new purpose"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "absent")
			var out, stderr bytes.Buffer
			args := append([]string{"--home", home, "mesh", "stop", strings.Repeat("1", 32)}, test.args...)
			if code := Main(args, strings.NewReader(""), &out, &stderr); code != 2 {
				t.Fatal(code, out.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "endpoint") {
				t.Fatal("invalid flags reached backend", stderr.String())
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatal("invalid flags created state", err)
			}
		})
	}
}
