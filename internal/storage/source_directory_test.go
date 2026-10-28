package storage

import (
	"getarm.org/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceDirectoryFixture(t *testing.T) (*Provider, model.Artifact) {
	t.Helper()
	state := t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := Open(filepath.Join(state, "storage"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	name := strings.Repeat("a", 32)
	id, err := p.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	return p, model.Artifact{Name: name, Path: p.Path(name), Identity: &id, Phase: "owned"}
}
func TestSourceDirectoryOriginalFDAndUnlink(t *testing.T) {
	p, a := sourceDirectoryFixture(t)
	held, err := p.HoldSourceDirectory(a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { held.Close() })
	nonce := strings.Repeat("b", 32)
	name, _ := SourceFileName("payload", nonce)
	file, err := held.OpenExclusive("payload", nonce)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = held.CheckFile(file, name, true); err != nil {
		t.Fatal(err)
	}
	if err = held.UnlinkOriginal(file, name); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("original bytes")); err != nil {
		t.Fatal(err)
	}
	if err = held.CheckFile(file, name, false); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(a.Path); err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	if _, err = held.OpenExclusive("../../foreign", nonce); err == nil {
		t.Fatal("unbounded purpose accepted")
	}
}
func TestSourceDirectoryRejectsReplacementWithoutAdoption(t *testing.T) {
	p, a := sourceDirectoryFixture(t)
	held, err := p.HoldSourceDirectory(a)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err = os.Rename(a.Path, a.Path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(a.Path, 0700); err != nil {
		t.Fatal(err)
	}
	if held.Check() == nil {
		t.Fatal("replacement path revived original custody")
	}
	if _, err = held.OpenExclusive("payload", strings.Repeat("b", 32)); err == nil {
		t.Fatal("file created through replacement")
	}
	if _, err = os.Stat(a.Path + "-original"); err != nil {
		t.Fatal("original directory was deleted")
	}
}
