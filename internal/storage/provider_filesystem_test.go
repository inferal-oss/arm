package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"getarm.org/internal/model"
)

func providerParentFixture(t *testing.T) (string, *os.File, string) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(parent)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { held.Close() })
	fs, err := providerFilesystemIdentity(int(held.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return parent, held, fs
}
func TestProviderFilesystemMismatchBeforeMkdir(t *testing.T) {
	parent, _, _ := providerParentFixture(t)
	path := filepath.Join(parent, "provider")
	if p, err := OpenOnFilesystem(path, nil, "fsid:old-controlled-observation"); err == nil {
		p.Close()
		t.Fatal("old filesystem accepted")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("mismatch changed parent namespace", entries, err)
	}
}
func TestProviderFilesystemHeldParentMatchingAndReopen(t *testing.T) {
	parent, held, fs := providerParentFixture(t)
	path := filepath.Join(parent, "provider")
	p, err := openProviderAtHeldParent(held, path, nil, fs)
	if err != nil {
		t.Fatal(err)
	}
	original := p.Identity()
	if !original.BirthKnown {
		t.Fatal("root identity lacks birth")
	}
	artifact, err := p.Create("0123456789abcdef0123456789abcdef")
	if err != nil || !artifact.BirthKnown {
		t.Fatal(artifact, err)
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenOnFilesystem(path, &original, fs)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if next.Identity() != original {
		t.Fatal("reopen changed original identity")
	}
	wrong := original
	wrong.Inode++
	if p, err = OpenOnFilesystem(path, &wrong, fs); err == nil {
		p.Close()
		t.Fatal("existing-root mismatch adopted")
	}
	if p, err = OpenOnFilesystem(path, nil, fs); err == nil {
		p.Close()
		t.Fatal("existing root adopted without original identity")
	}
}
func TestProviderFilesystemHeldParentRenameCannotRedirectCreation(t *testing.T) {
	base, _, _ := providerParentFixture(t)
	original := filepath.Join(base, "selected")
	moved := filepath.Join(base, "moved")
	outside := filepath.Join(base, "replacement")
	for _, p := range []string{original, outside} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(original, "provider")
	held, err := openProviderParent(target)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	fs, err := providerFilesystemIdentity(int(held.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, original); err != nil {
		t.Fatal(err)
	}
	if p, err := openProviderAtHeldParent(held, target, nil, fs); err == nil {
		p.Close()
		t.Fatal("replaced named parent published")
	}
	if _, err = os.Lstat(filepath.Join(outside, "provider")); !os.IsNotExist(err) {
		t.Fatal("mkdir followed replacement parent", err)
	}
	// It was created only on the checked original FD. Failure leaves that owned
	// uncertain namespace untouched; no automatic deletion or credit is inferred.
	f, err := os.Open(filepath.Join(moved, "provider"))
	if err != nil {
		t.Fatal("original held parent did not receive root", err)
	}
	defer f.Close()
	got, err := providerFilesystemIdentity(int(f.Fd()))
	if err != nil || got != fs {
		t.Fatal("created root changed filesystem", got, err)
	}
}
func TestProviderFilesystemExistingRootMismatchPreservesSentinel(t *testing.T) {
	parent, _, fs := providerParentFixture(t)
	path := filepath.Join(parent, "provider")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(path, "unrelated")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	wrong := model.FileIdentity{Inode: 123, Owner: uint32(os.Geteuid()), BirthKnown: true}
	for _, expected := range []*model.FileIdentity{nil, &wrong} {
		if p, err := OpenOnFilesystem(path, expected, fs); err == nil {
			p.Close()
			t.Fatal("unrelated existing root adopted")
		}
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "preserve" {
		t.Fatal("unrelated namespace changed", err)
	}
}
func TestProviderFilesystemRejectsSymlinkParentsAndBounds(t *testing.T) {
	base, _, fs := providerParentFixture(t)
	real := filepath.Join(base, "real")
	alias := filepath.Join(base, "alias")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(alias, "provider"), filepath.Join(alias, "child", "provider"), base + "/../provider", base + "/" + strings.Repeat("x", 256), base + strings.Repeat("/x", 129) + "/provider"} {
		if p, err := OpenOnFilesystem(path, nil, fs); err == nil {
			p.Close()
			t.Fatal("unbounded/noncanonical/symlink parent accepted", path)
		}
	}
	if p, err := OpenOnFilesystem(filepath.Join(real, "provider"), nil, ""); err == nil {
		p.Close()
		t.Fatal("missing expected filesystem accepted")
	}
	entries, err := os.ReadDir(real)
	if err != nil || len(entries) != 0 {
		t.Fatal("refused paths changed namespace", entries, err)
	}
}

func TestProviderFilesystemReopenRefusesChangedPrivateMode(t *testing.T) {
	parent, _, fs := providerParentFixture(t)
	path := filepath.Join(parent, "provider")
	p, err := OpenOnFilesystem(path, nil, fs)
	if err != nil {
		t.Fatal(err)
	}
	id := p.Identity()
	p.Close()
	if err = os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if p, err = OpenOnFilesystem(path, &id, fs); err == nil {
		p.Close()
		t.Fatal("nonprivate root reopened")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("refusal changed namespace/mode", info, err)
	}
}

func TestProviderFilesystemReopensOriginalOpenIdentity(t *testing.T) {
	parent, _, fs := providerParentFixture(t)
	path := filepath.Join(parent, "provider")
	legacy, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	original := legacy.Identity()
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}
	checked, err := OpenOnFilesystem(path, &original, fs)
	if err != nil {
		t.Fatal("original Open identity no longer compatible", err)
	}
	defer checked.Close()
	if checked.Identity() != original {
		t.Fatal("legacy identity changed on checked reopen")
	}
}
