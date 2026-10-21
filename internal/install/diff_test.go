package install

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnifiedChangesApplyToExactExpectedBytes(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is unavailable for independent unified-diff validation")
	}
	for _, test := range []struct {
		name, before, after string
		exists, remains     bool
	}{{"append", "old\n", "old\nnew\n", true, true}, {"new-file", "", "new\n", false, true}, {"delete", "old\n", "", true, false}, {"no-newline", "old", "old\nnew", true, true}, {"middle", "a\nb\nc\nd\ne\nf\ng\nh\ni\n", "a\nb\nc\nchanged\ne\nf\ng\nh\ni\n", true, true}} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "AGENTS.md")
			if test.exists {
				os.WriteFile(path, []byte(test.before), 0600)
			}
			change := fileChange("AGENTS.md", []byte(test.before), []byte(test.after), test.exists, test.remains)
			patch := filepath.Join(root, "change.patch")
			os.WriteFile(patch, []byte(change.Diff), 0600)
			cmd := exec.Command(git, "apply", "--unsafe-paths", "-p0", patch)
			cmd.Dir = root
			if data, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("unified diff rejected: %v %s\n%s", err, data, change.Diff)
			}
			got, err := os.ReadFile(path)
			if !test.remains {
				if !os.IsNotExist(err) {
					t.Fatal("deletion patch retained file")
				}
				return
			}
			if err != nil || !bytes.Equal(got, []byte(test.after)) {
				t.Fatalf("diff applied incorrect bytes: %q %v", got, err)
			}
		})
	}
}
func TestDiffQuotesCallerControlledPaths(t *testing.T) {
	change := fileChange("/tmp/evil\n+++ forged\x1b[2J", nil, []byte("new\n"), false, true)
	if strings.Contains(change.Diff, "\n+++ forged") || strings.Contains(change.Diff, "\x1b") {
		t.Fatal("path injected diff lines or terminal codes")
	}
}
