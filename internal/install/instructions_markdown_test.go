package install

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestInstructionCommandsAreMarkdownCode(t *testing.T) {
	// Check commands as complete spans/blocks, not merely whether a backtick
	// appears somewhere nearby. Long examples retain executable line continuations.
	for _, want := range []string{
		"`arm status --json`", "`arm agents --json`", "`arm agents card --list --json`",
		"`arm agents card NAME --json`", "`arm agents card rust-build --json`", "`arm agents card gradle-build --json`",
		"`arm run --cpu N --memory SIZE --ttl DURATION --reason \"purpose\" -- COMMAND`",
		"`arm release LEASE_ID`", "`arm renew LEASE_ID --ttl DURATION`", "`arm agent register`",
		"```sh\narm agent send --id CONTACT --session SESSION --registration REGISTRATION \\\n  --to RECIPIENT --text \"message\" --key SEND_KEY\n```",
		"```sh\narm agent reply --id CONTACT --session SESSION --registration REGISTRATION \\\n  --message MESSAGE_ID --text \"reply\" --key REPLY_KEY\n```",
		"`arm agent list --json`", "`--to-registration`", "`--key SEND_KEY`", "`sender_contact`", "`after`",
		"`arm messages --json`", "`arm pool create NAME --scope host|global --capacity COUNT --reason \"purpose\"`",
		"`arm acquire --pool POOL_ID=COUNT --reason \"purpose\"`", "`arm pool history POOL_ID`", "`--revision N`",
		"`arm storage export`", "`arm storage exports`", "`arm storage retain`", "`--host HOST_ID_OR_UNIQUE_NAME`",
		"`arm mesh message-lookup HOST_ID --key KEY`", "`--delivery poll`", "`--idle`", "`--after CURSOR`", "`--once`",
		"`arm agent inbox --id CONTACT --session SESSION --pending`",
		"`arm agent watch --id CONTACT --session SESSION`",
		"`arm agent ack --id CONTACT --session SESSION --message MESSAGE_ID --text \"response\"`",
	} {
		if !strings.Contains(instructionsMarkdown, want) {
			t.Errorf("missing complete Markdown command/field: %s", want)
		}
	}
	if strings.Count(instructions, begin) != 1 || strings.Count(instructions, end) != 1 || !strings.HasPrefix(instructions, begin+"\n## ") || !strings.HasSuffix(instructions, "\n"+end+"\n") {
		t.Fatal("embedding altered managed markers or newline boundaries")
	}
	var prose strings.Builder
	fenced := false
	for _, line := range strings.Split(instructionsMarkdown, "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		parts := strings.Split(line, "`")
		if len(parts)%2 == 0 {
			t.Fatalf("unclosed inline code span: %s", line)
		}
		for i := 0; i < len(parts); i += 2 {
			prose.WriteString(parts[i])
			prose.WriteByte('\n')
		}
	}
	if fenced {
		t.Fatal("unclosed command fence")
	}
	if bare := regexp.MustCompile(`\barm(?:\s+[a-z]|\b)|--[a-z][a-z-]*|\b(?:SEND_KEY|REPLY_KEY|sender_contact)\b`).FindString(prose.String()); bare != "" {
		t.Fatalf("command/flag/field escaped code formatting: %q", bare)
	}
}

func TestMarkdownUpgradePreviewBackupAndUninstallBothAdapters(t *testing.T) {
	for _, adapter := range []struct{ name, file string }{{"codex", "AGENTS.md"}, {"claude", "CLAUDE.md"}} {
		t.Run(adapter.name, func(t *testing.T) {
			base := t.TempDir()
			home, root := filepath.Join(base, "bound state with ' quote"), filepath.Join(base, adapter.name)
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, adapter.file)
			original := "## User policy\nKeep this user text unchanged.\n"
			if err := os.WriteFile(path, []byte(original), 0640); err != nil {
				t.Fatal(err)
			}
			targets := []Target{{Adapter: adapter.name, Root: root, Name: adapter.file}}
			if _, err := Install(home, targets, false); err != nil {
				t.Fatal(err)
			}
			manifest, err := readManifest(home)
			if err != nil {
				t.Fatal(err)
			}
			// Reproduce an exactly recorded older unformatted block; existing
			// interrupted-upgrade tests separately cover either write boundary.
			legacy := "\n\n" + begin + "\n## Agent Resource Manager\n\nUse arm for resource-intensive commands. Start with arm status --json.\n" + end + "\n"
			manifest.Entries[0].Block = legacy
			if err := save(home, manifest); err != nil {
				t.Fatal(err)
			}
			later := "\nUser addition after installation.\n"
			before := []byte(original + legacy + later)
			if err := os.WriteFile(path, before, 0640); err != nil {
				t.Fatal(err)
			}
			manifestBefore, err := os.ReadFile(filepath.Join(home, "install.json"))
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PreviewInstall(home, targets)
			if err != nil || len(plan.Report.Actions) != 1 {
				t.Fatal(plan, err)
			}
			action := plan.Report.Actions[0]
			if action.Action != "upgrade-managed-instructions" || action.Change == nil || !strings.Contains(action.Change.Diff, "-Use arm for") || !strings.Contains(action.Change.Diff, "+Installation alone does not authorize adoption.") || !strings.Contains(action.Change.Diff, "+Once enrolled, use `arm` for") {
				t.Fatal("preview omitted exact Markdown upgrade diff", action)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, before) {
				t.Fatal("preview wrote instruction file", err)
			}
			manifestAfter, err := os.ReadFile(filepath.Join(home, "install.json"))
			if err != nil || !bytes.Equal(manifestAfter, manifestBefore) {
				t.Fatal("preview rewrote manifest", err)
			}
			if _, err := os.Stat(plan.Report.BackupDirectory); !os.IsNotExist(err) {
				t.Fatal("preview created backup", err)
			}
			report, err := plan.Apply()
			if err != nil || !report.Complete || !report.BackupComplete {
				t.Fatal(report, err)
			}
			foundBackup := false
			for _, backup := range report.Backups {
				if backup.Source == path {
					saved, err := os.ReadFile(backup.Saved)
					if err != nil || !bytes.Equal(saved, before) {
						t.Fatal("upgrade backup did not retain exact unformatted bytes", err)
					}
					foundBackup = true
				}
			}
			if !foundBackup {
				t.Fatal("upgrade skipped instruction backup")
			}
			upgraded, err := os.ReadFile(path)
			if err != nil || digest(upgraded) != action.Change.AfterHash || !strings.HasPrefix(string(upgraded), original) || !strings.HasSuffix(string(upgraded), later) {
				t.Fatal("upgrade differed from exact preview or damaged user text", err)
			}
			if _, err := Install(home, targets, false); err != nil {
				t.Fatal("repeat install", err)
			}
			repeated, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(upgraded, repeated) {
				t.Fatal("Markdown reinstall was not idempotent", err)
			}
			uninstall, err := PreviewUninstall(home)
			if err != nil {
				t.Fatal(err)
			}
			if report, err := uninstall.Apply(); err != nil || !report.Complete || !report.BackupComplete {
				t.Fatal(report, err)
			}
			remaining, err := os.ReadFile(path)
			if err != nil || string(remaining) != original+later {
				t.Fatal("uninstall changed surrounding user text", string(remaining), err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatal("upgrade/uninstall changed instruction permissions", err)
			}
		})
	}
}
