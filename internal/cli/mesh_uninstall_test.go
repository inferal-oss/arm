package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeshUninstallRejectsAmbiguousEffectsBeforeBackend(t *testing.T) {
	for _, flags := range [][]string{{"--lookup", "--key", "original", "--yes=false"}, {"--finish", "--key", "original", "--dry-run"}, {"--finish", "--key", "original", "--reason", "new"}, {"--lookup", "--finish", "--key", "original"}, {"--force", "--wait"}, {"--wait", "--timeout", "0"}, {"--timeout", "1"}, {"--lookup"}, {"--finish"}} {
		t.Run(strings.Join(flags, "-"), func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "absent")
			var out, stderr bytes.Buffer
			args := append([]string{"--home", home, "mesh", "uninstall", strings.Repeat("1", 32)}, flags...)
			if code := Main(args, strings.NewReader(""), &out, &stderr); code != 2 {
				t.Fatal(code, out.String(), stderr.String())
			}
			if _, e := os.Stat(home); !os.IsNotExist(e) {
				t.Fatal("invalid CLI created state", e)
			}
		})
	}
}
