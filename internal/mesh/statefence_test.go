package mesh

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yrashk/arm/internal/local"
)

func TestResetFenceBlocksReadOnlyLifecycleAndMeshInitialization(t *testing.T) {
	home := t.TempDir()
	os.Chmod(home, 0700)
	if err := os.WriteFile(filepath.Join(home, local.ResetFenceName), []byte("private interrupted reset"), 0600); err != nil {
		t.Fatal(err)
	}
	var fenced *local.StateFenceError
	if _, err := InspectLifecycle(home, "test"); !errors.As(err, &fenced) {
		t.Fatalf("inspection bypassed reset: %v", err)
	}
	if store, err := Init(home, "0123456789abcdef0123456789abcdef", "fixture", "127.0.0.1:23456", "127.0.0.1:23456"); err == nil {
		store.Close()
		t.Fatal("mesh init bypassed reset")
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 1 || entries[0].Name() != local.ResetFenceName {
		t.Fatal("fenced mesh changed namespace")
	}
}
