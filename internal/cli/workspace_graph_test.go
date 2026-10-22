package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/cli"
)

func TestWorkspaceGraphOfflineHelpAndSelectorValidation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{{"workspaces", "graph", "--help"}, {"help", "workspaces", "graph"}} {
		var out, stderr bytes.Buffer
		if code := cli.Main(append([]string{"--home", home}, args...), strings.NewReader(""), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatal(code, out.String(), stderr.String())
		}
		for _, want := range []string{"--workspace", "--section", "--to", "--hash", "reachability"} {
			if !strings.Contains(out.String(), want) {
				t.Fatal("missing bounded graph contract", want, out.String())
			}
		}
		for _, absent := range []string{"--reason", "--approve", "--yes", "--key"} {
			if strings.Contains(out.String(), "\n  "+absent) {
				t.Fatal("graph read exposed mutation selector", absent)
			}
		}
	}
	for _, args := range [][]string{{}, {"--workspace", "x", "--section", "paths"}, {"--workspace", "x", "--section", "nodes", "--to", "y"}, {"--workspace", "x", "--limit", "41"}, {"--workspace", "x", "--section", "edges", "--offset", "-1"}} {
		var out, stderr bytes.Buffer
		code := cli.Main(append([]string{"--home", home, "workspaces", "graph"}, args...), strings.NewReader(""), &out, &stderr)
		if code == 0 || !strings.Contains(stderr.String(), "invalid_request") {
			t.Fatal("invalid read selectors reached backend", code, args, stderr.String())
		}
	}
	if _, e := os.Stat(home); !os.IsNotExist(e) {
		t.Fatal("help/invalid graph command initialized local authority", e)
	}
}
