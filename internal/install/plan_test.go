package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApprovedPlanRejectsEditedReplacedAndChangedRoots(t *testing.T) {
	for _, change := range []string{"contents", "inode", "root", "manifest", "mode", "missing-parent"} {
		t.Run(change, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "state")
			root := filepath.Join(base, "agent")
			if change == "missing-parent" {
				root = filepath.Join(base, "new", "agent")
			} else {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("original"), 0640); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := PreviewInstall(home, []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(home); !os.IsNotExist(err) {
				t.Fatal("preview created state")
			}
			path := filepath.Join(root, "AGENTS.md")
			switch change {
			case "contents":
				err = os.WriteFile(path, []byte("edited"), 0640)
			case "inode":
				err = os.Rename(path, path+".old")
				if err == nil {
					err = os.WriteFile(path, []byte("original"), 0640)
				}
			case "root":
				err = os.Rename(root, root+".old")
				if err == nil {
					err = os.Mkdir(root, 0700)
				}
				if err == nil {
					err = os.WriteFile(path, []byte("original"), 0640)
				}
			case "mode":
				err = os.Chmod(path, 0600)
			case "manifest":
				err = os.Mkdir(home, 0700)
				if err == nil {
					err = os.WriteFile(filepath.Join(home, "install.json"), []byte(`{"version":1,"entries":[]}`), 0600)
				}
			case "missing-parent":
				err = os.Mkdir(filepath.Dir(root), 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = plan.Apply(); err == nil {
				t.Fatal("changed preview was applied")
			}
			if change != "manifest" {
				if _, err = os.Stat(home); !os.IsNotExist(err) {
					t.Fatal("rejection created state")
				}
			}
		})
	}
}

func TestApprovedPlanCreatesNestedStateAndRevalidatesUninstall(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "nested", "state")
	root := filepath.Join(base, "agent")
	plan, err := PreviewInstall(home, []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.Apply(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	uninstall, err := PreviewUninstall(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "AGENTS.md"), append(before, []byte("\nnew user content")...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = uninstall.Apply(); err == nil {
		t.Fatal("stale uninstall approval removed instructions")
	}
	current, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil || len(current) <= len(before) {
		t.Fatal("edited file lost")
	}
	uninstall, err = PreviewUninstall(home)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := uninstall.Apply(); err != nil || !report.Complete {
		t.Fatal(report, err)
	}
	remaining, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if string(remaining) != "\nnew user content" {
		t.Fatal("unrelated content changed", string(remaining))
	}
}

func TestApprovedPlanRejectsSymlinkReplacement(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "state")
	root := filepath.Join(base, "agent")
	os.Mkdir(root, 0700)
	path := filepath.Join(root, "AGENTS.md")
	os.WriteFile(path, []byte("original"), 0600)
	plan, err := PreviewInstall(home, []Target{{Adapter: "codex", Root: root, Name: "AGENTS.md"}})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	os.WriteFile(outside, []byte("sentinel"), 0600)
	os.Remove(path)
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err = plan.Apply(); err == nil {
		t.Fatal("followed a changed target")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "sentinel" {
		t.Fatal("outside data changed")
	}
}

func TestApprovedPlanCanCreateStateAndTargetInSameDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "new", "state")
	plan, err := PreviewInstall(home, []Target{{Adapter: "codex", Root: home, Name: "AGENTS.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = plan.Apply(); err != nil {
		t.Fatal(err)
	}
	plan, err = PreviewUninstall(home)
	if err != nil {
		t.Fatal(err)
	}
	if report, err := plan.Apply(); err != nil || !report.Complete {
		t.Fatal(report, err)
	}
}
