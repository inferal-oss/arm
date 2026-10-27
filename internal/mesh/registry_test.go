package mesh

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/inferal-oss/arm/internal/local"
)

func TestNodeRegistryReadDoesNotPrepareStateOrLoadKeys(t *testing.T) {
	home := filepath.Join(t.TempDir(), "absent")
	if _, exists, err := ReadRegistry(home); err != nil || exists {
		t.Fatal(exists, err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("read created directory", err)
	}
	home = filepath.Join(t.TempDir(), "state")
	store, err := Init(home, local.ID(), "read-only", "127.0.0.1:12345", "127.0.0.1:12345")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err = os.Remove(filepath.Join(home, "mesh", "node-key.pem")); err != nil {
		t.Fatal(err)
	}
	if config, exists, err := ReadRegistry(home); err != nil || !exists || config.Identity.Name != "read-only" {
		t.Fatal(config, exists, err)
	}
	if err = os.Chmod(filepath.Join(home, "mesh", "config.json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = ReadRegistry(home); err == nil {
		t.Fatal("unsafe registry permissions accepted")
	}
}
