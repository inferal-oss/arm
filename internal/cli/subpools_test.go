package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/cli"
)

func TestSubpoolCLIHelpAndInvalidSelectorsAreOffline(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, command := range []string{"pool roots", "pool children ID", "pool consumers ID", "pool lookup", "pool create name"} {
		var out, stderr bytes.Buffer
		args := append([]string{"--home", home}, strings.Fields(command)...)
		args = append(args, "--help")
		if code := cli.Main(args, strings.NewReader(""), &out, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "Usage: arm") {
			t.Fatal(command, code, &out, &stderr)
		}
	}
	for _, command := range []string{"pool create child --parent root --capacity 1 --reason test", "pool create child --parent root --parent-revision 0 --capacity 1 --reason test", "pool create root --scope host --parent-revision 0 --capacity 1 --reason test", "pool children", "pool consumers", "pool roots unexpected", "pool lookup", "pool lookup --key key unexpected"} {
		var out, stderr bytes.Buffer
		args := append([]string{"--home", home}, strings.Fields(command)...)
		if code := cli.Main(args, strings.NewReader(""), &out, &stderr); code != 2 {
			t.Fatal(command, code, &out, &stderr)
		}
	}
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatal("offline validation created state", err)
	}
}
