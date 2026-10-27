package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/model"
)

func TestExactPreviewAndBackupBeforeInstallAndUninstall(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "state")
	root := filepath.Join(base, "codex")
	os.Mkdir(root, 0700)
	path := filepath.Join(root, "AGENTS.md")
	original := []byte("My instructions without final newline")
	if err := os.WriteFile(path, original, 0640); err != nil {
		t.Fatal(err)
	}
	targets := []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}, {Adapter: "claude", Root: filepath.Join(base, "claude"), Name: "CLAUDE.md"}}
	plan, err := PreviewInstall(home, targets)
	if err != nil {
		t.Fatal(err)
	}
	change := plan.Report.Actions[0].Change
	if change == nil || change.BeforeHash != digest(original) || change.BeforeExists != true || change.Added == 0 || !strings.Contains(change.Diff, `\ No newline at end of file`) {
		t.Fatal("imprecise existing-file preview", change)
	}
	if _, err = os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("preview created backup/state")
	}
	report, err := plan.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if !report.BackupComplete || report.BackupDirectory != plan.Report.BackupDirectory || len(report.Backups) != 3 {
		t.Fatal(report)
	}
	installed, _ := os.ReadFile(path)
	if digest(installed) != change.AfterHash {
		t.Fatal("applied bytes differed from approved diff")
	}
	for _, entry := range report.Backups {
		if !entry.Exists && (entry.Mode != nil || entry.UID != nil || entry.GID != nil) {
			t.Fatal("absent original was assigned invented permissions/ownership", entry)
		}
		meta, err := os.ReadFile(entry.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		var stored BackupEntry
		if json.Unmarshal(meta, &stored) != nil || stored.Source != entry.Source {
			t.Fatal("invalid backup metadata")
		}
		if entry.Source == path {
			saved, err := os.ReadFile(entry.Saved)
			if err != nil || !bytes.Equal(saved, original) || entry.SHA256 != digest(original) || entry.Mode == nil || *entry.Mode != 0640 {
				t.Fatal("original backup lost bytes/mode", entry, err)
			}
			info, _ := os.Stat(entry.Saved)
			if info.Mode().Perm() != 0600 {
				t.Fatal("backup was not private")
			}
		}
		if entry.Source == filepath.Join(base, "claude", "CLAUDE.md") && (entry.Exists || entry.Saved != "") {
			t.Fatal("absent file was invented in backup")
		}
	}
	before, _ := os.ReadDir(filepath.Join(home, "install-backups"))
	if _, err = Install(home, targets, false); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadDir(filepath.Join(home, "install-backups"))
	if len(after) != len(before) {
		t.Fatal("no-op reinstall created redundant backups")
	}
	manifest, _ := os.ReadFile(filepath.Join(home, "install.json"))
	p, err := PreviewUninstall(home)
	if err != nil {
		t.Fatal(err)
	}
	if p.Report.Actions[0].Change.AfterHash != digest(original) || p.Report.Actions[1].Change.AfterExists {
		t.Fatal("uninstall preview did not describe exact restoration/deletion", p.Report)
	}
	r, err := p.Apply()
	if err != nil || !r.BackupComplete {
		t.Fatal(r, err)
	}
	for _, entry := range r.Backups {
		if entry.Exists {
			b, err := os.ReadFile(entry.Saved)
			if err != nil {
				t.Fatal(err)
			}
			if entry.Source == path && !bytes.Equal(b, installed) {
				t.Fatal("uninstall did not save installed content")
			}
			if entry.Source == filepath.Join(home, "install.json") && !bytes.Equal(b, manifest) {
				t.Fatal("installation manifest was not backed up")
			}
		}
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatal("uninstall changed unrelated user bytes")
	}
	if _, err = os.Stat(report.BackupDirectory); err != nil {
		t.Fatal("uninstall removed previous backup")
	}
}

func TestBackupFailureAndUnsafeRootsPreventEveryInstructionWrite(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "public-directory"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "state")
			root := filepath.Join(base, "agent")
			os.Mkdir(home, 0700)
			os.Mkdir(root, 0700)
			path := filepath.Join(root, "AGENTS.md")
			original := []byte("keep me\n")
			os.WriteFile(path, original, 0644)
			p, err := PreviewInstall(home, []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}})
			if err != nil {
				t.Fatal(err)
			}
			backupRoot := filepath.Join(home, "install-backups")
			outside := filepath.Join(base, "outside")
			os.Mkdir(outside, 0700)
			sentinel := filepath.Join(outside, "sentinel")
			os.WriteFile(sentinel, []byte("untouched"), 0600)
			switch kind {
			case "file":
				os.WriteFile(backupRoot, []byte("blocked"), 0600)
			case "symlink":
				os.Symlink(outside, backupRoot)
			case "public-directory":
				os.Mkdir(backupRoot, 0755)
			}
			r, err := p.Apply()
			var api *model.Error
			if err == nil || !errors.As(err, &api) || api.Code != "backup_failed" || r.BackupComplete {
				t.Fatal("unsafe backup was accepted", r, err)
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, original) {
				t.Fatal("backup failure modified instructions")
			}
			if _, err = os.Stat(filepath.Join(home, "install.json")); !os.IsNotExist(err) {
				t.Fatal("backup failure modified manifest")
			}
			got, _ = os.ReadFile(sentinel)
			if string(got) != "untouched" {
				t.Fatal("backup escaped its private root")
			}
		})
	}
}

func TestBinaryUserBytesRemainExactAndDiffIsExplicit(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "agent")
	os.Mkdir(root, 0700)
	home := filepath.Join(base, "state")
	path := filepath.Join(root, "AGENTS.md")
	original := []byte{'x', 0xff, '\n'}
	os.WriteFile(path, original, 0600)
	p, err := PreviewInstall(home, []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Report.Actions[0].Change.Binary || !strings.Contains(p.Report.Actions[0].Change.Diff, "Non-UTF-8") {
		t.Fatal("invalid UTF-8 was silently shown as a textual patch")
	}
	r, err := p.Apply()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.Backups {
		if entry.Source == path {
			b, _ := os.ReadFile(entry.Saved)
			if !bytes.Equal(b, original) {
				t.Fatal("backup corrupted binary bytes")
			}
		}
	}
	if _, err = Uninstall(home, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatal("binary user bytes were not preserved")
	}
}
func TestApplyRejectsAComputedTransformationOutsideApprovedHashes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "agent")
	home := filepath.Join(base, "state")
	os.Mkdir(root, 0700)
	path := filepath.Join(root, "AGENTS.md")
	original := []byte("user instructions\n")
	os.WriteFile(path, original, 0600)
	p, err := PreviewInstall(home, []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}})
	if err != nil {
		t.Fatal(err)
	}
	change := p.snapshot.changes[path]
	change.AfterHash = digest([]byte("different approved bytes"))
	p.snapshot.changes[path] = change
	r, err := p.Apply()
	var api *model.Error
	if err == nil || !errors.As(err, &api) || api.Code != "plan_changed" || !r.BackupComplete {
		t.Fatal("unreviewed computed edit was applied", r, err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, original) {
		t.Fatal("unreviewed edit modified source")
	}
	if _, err = os.Stat(filepath.Join(home, "install.json")); !os.IsNotExist(err) {
		t.Fatal("unreviewed edit modified installation record")
	}
}
func TestPreservingEveryEditedBlockDoesNotRewriteManifestOrCreateBackup(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "agent")
	home := filepath.Join(base, "state")
	target := Target{Adapter: "codex", Root: root, Name: "AGENTS.md"}
	if _, err := Install(home, []Target{target}, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, target.Name)
	b, _ := os.ReadFile(path)
	b = bytes.Replace(b, []byte("Start with"), []byte("Edited: start with"), 1)
	os.WriteFile(path, b, 0600)
	before, err := fileState(home, "install.json")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Uninstall(home, false)
	if err != nil || r.Complete || r.BackupDirectory != "" {
		t.Fatal(r, err)
	}
	after, err := fileState(home, "install.json")
	if err != nil || before != after {
		t.Fatal("preservation-only uninstall rewrote the installation manifest")
	}
}
