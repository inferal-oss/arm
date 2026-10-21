package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructionUpgradeAndInterruptedBoundaries(t *testing.T) {
	for _, phase := range []string{"normal", "before-write", "after-write", "uninstall-before-write", "uninstall-after-write"} {
		t.Run(phase, func(t *testing.T) {
			base := t.TempDir()
			home := filepath.Join(base, "arm")
			root := filepath.Join(base, "agent")
			target := Target{Adapter: "claude", Root: root, Name: "CLAUDE.md"}
			os.Mkdir(root, 0700)
			path := filepath.Join(root, target.Name)
			original := "original user instructions\n"
			os.WriteFile(path, []byte(original), 0640)
			if _, err := Install(home, []Target{target}, false); err != nil {
				t.Fatal(err)
			}
			m, err := readManifest(home)
			if err != nil {
				t.Fatal(err)
			}
			current := m.Entries[0].Block
			old := "\n\n" + begin + "\nLegacy ARM instructions\n" + end + "\n"
			m.Entries[0].Block = old
			if phase != "normal" {
				m.Entries[0].PendingBlock = current
			}
			if err = save(home, m); err != nil {
				t.Fatal(err)
			}
			block := old
			if strings.HasSuffix(phase, "after-write") {
				block = current
			}
			userEdit := "\nLater user edit\n"
			os.WriteFile(path, []byte(original+block+userEdit), 0640)
			if strings.HasPrefix(phase, "uninstall") {
				r, err := Uninstall(home, false)
				if err != nil || !r.Complete {
					t.Fatalf("interrupted upgrade uninstall: %+v %v", r, err)
				}
				b, _ := os.ReadFile(path)
				if string(b) != original+userEdit {
					t.Fatal("uninstall damaged user instructions")
				}
				return
			}
			before, _ := os.ReadFile(path)
			manifestBefore, _ := os.ReadFile(filepath.Join(home, "install.json"))
			if _, err = Install(home, []Target{target}, true); err != nil {
				t.Fatal(err)
			}
			afterPreview, _ := os.ReadFile(path)
			manifestAfter, _ := os.ReadFile(filepath.Join(home, "install.json"))
			if string(afterPreview) != string(before) || string(manifestBefore) != string(manifestAfter) {
				t.Fatal("upgrade preview modified files")
			}
			if _, err = Install(home, []Target{target}, false); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(path)
			if string(b) != original+current+userEdit || !strings.Contains(string(b), "arm agent watch") {
				t.Fatalf("upgrade lost instructions or user edits: %s", b)
			}
			m, err = readManifest(home)
			if err != nil || m.Entries[0].PendingBlock != "" || m.Entries[0].Block != current {
				t.Fatal("upgrade intention did not finalize", err)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0640 {
				t.Fatal("upgrade changed file permissions")
			}
			if _, err = Uninstall(home, false); err != nil {
				t.Fatal(err)
			}
			b, _ = os.ReadFile(path)
			if string(b) != original+userEdit {
				t.Fatal("upgraded uninstall damaged user instructions")
			}
		})
	}
}

func TestAmbiguousInterruptedUpgradePreservesBothBlocks(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "arm")
	root := filepath.Join(base, "agent")
	target := Target{Adapter: "claude", Root: root, Name: "CLAUDE.md"}
	if _, err := Install(home, []Target{target}, false); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(home)
	if err != nil {
		t.Fatal(err)
	}
	old := m.Entries[0].Block
	next := strings.Replace(old, "Register your exact session", "New policy: register your exact session", 1)
	m.Entries[0].PendingBlock = next
	if err = save(home, m); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, target.Name)
	both := old + next
	if err = os.WriteFile(path, []byte(both), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(home, []Target{target}, false); err == nil {
		t.Fatal("ambiguous upgrade was accepted")
	}
	r, err := Uninstall(home, false)
	if err != nil || r.Complete {
		t.Fatalf("ambiguous uninstall claimed complete: %+v %v", r, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != both {
		t.Fatal("ambiguous blocks were modified", err)
	}
}
