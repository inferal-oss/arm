package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareNeverChangesSharedDirectoryPermissions(t *testing.T) {
	base := t.TempDir()
	public := filepath.Join(base, "public")
	if e := os.Mkdir(public, 0755); e != nil {
		t.Fatal(e)
	}
	if e := Prepare(public); e == nil {
		t.Fatal("accepted shared state directory")
	}
	info, _ := os.Stat(public)
	if info.Mode().Perm() != 0755 {
		t.Fatal("changed existing directory permissions")
	}
	link := filepath.Join(base, "link")
	os.Symlink(base, link)
	if e := Prepare(link); e == nil {
		t.Fatal("accepted symlink as state root")
	}
}

func TestIntegrationLockIsExclusive(t *testing.T) {
	home := filepath.Join(t.TempDir(), "arm")
	lock, e := Lock(home, "integration.lock")
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if second, e := Lock(home, "integration.lock"); e == nil {
		second.Close()
		t.Fatal("second integration operation acquired the same lock")
	}
}
