package install

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRepairChangedInstructionRoot(t *testing.T) {
	for _, change := range []string{"intact", "edited", "changed-after-review"} {
		t.Run(change, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "state")
			root := filepath.Join(base, "agent")
			previousRoot := filepath.Join(base, "previous-agent-root")
			targets := []Target{{Adapter: "claude", Root: root, Name: "CLAUDE.md"}}
			path := filepath.Join(root, "CLAUDE.md")
			manifestPath := filepath.Join(home, "install.json")
			write := func(path string, content []byte) {
				t.Helper()
				if err := os.WriteFile(path, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			read := func(path string) []byte {
				t.Helper()
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			if err := os.Mkdir(previousRoot, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(previousRoot, root); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(home, targets, false); err != nil {
				t.Fatal(err)
			}
			original := read(manifestPath)
			instructions := append(read(path), []byte("\nUser instructions.\n")...)
			if err := os.Remove(root); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			if change == "edited" {
				instructions = bytes.Replace(instructions, []byte("Installation alone"), []byte("Installation"), 1)
			}
			write(path, instructions)
			if _, err := PreviewInstall(home, targets); err == nil {
				t.Fatal("ordinary installation accepted changed root")
			}
			plan, err := PreviewRepair(home)
			if change == "edited" {
				if err == nil {
					t.Fatal("repair accepted changed managed block")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Report.Actions) != 1 || !bytes.Equal(read(manifestPath), original) {
				t.Fatal("unexpected repair preview", plan.Report)
			}
			if change == "changed-after-review" {
				write(path, append(instructions, []byte("Another edit.\n")...))
				if _, err := plan.Apply(); err == nil || !bytes.Equal(read(manifestPath), original) {
					t.Fatal("repair applied after the reviewed file changed", err)
				}
				return
			}
			report, err := plan.Apply()
			if err != nil {
				t.Fatal(err)
			}
			if !report.BackupComplete || len(report.Backups) != 1 || !bytes.Equal(read(report.Backups[0].Saved), original) {
				t.Fatal("repair did not back up the original manifest", report)
			}
			if !bytes.Equal(read(path), instructions) || !InspectInstructions(home).Ready {
				t.Fatal("repair failed to restore readiness or changed the instructions")
			}
			plan, err = PreviewRepair(home)
			if err != nil || len(plan.Report.Actions) != 0 || plan.Report.BackupDirectory != "" {
				t.Fatal("repeat repair is not a no-op", err)
			}
		})
	}
}
