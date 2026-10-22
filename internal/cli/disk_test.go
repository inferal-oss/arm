package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveDiskCLIHelpDefaultsAutoAndRejectsAmbiguousZeroBeforeState(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	for _, command := range []string{"start", "serve", "daemon start"} {
		var out, stderr bytes.Buffer
		args := append([]string{"--home", home}, strings.Fields(command)...)
		args = append(args, "--help")
		if code := Main(args, strings.NewReader(""), &out, &stderr); code != 0 || !strings.Contains(out.String(), "live state-filesystem pool") || !strings.Contains(out.String(), `(default auto)`) {
			t.Fatal(command, code, &out, &stderr)
		}
	}
	for _, line := range []string{"start --disk 0", "serve --disk -1", "start --disk garbage"} {
		var out, stderr bytes.Buffer
		if code := Main(append([]string{"--home", home}, strings.Fields(line)...), strings.NewReader(""), &out, &stderr); code != 2 {
			t.Fatal(line, code, &out, &stderr)
		}
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("argument/help work created state", err)
	}
}
