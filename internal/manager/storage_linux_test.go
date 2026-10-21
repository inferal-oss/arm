package manager

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStorageDeniedNativeObservationRetainsFilesAndBudget(t *testing.T) {
	m := newManager(t)
	m.Start()
	l, err := m.Acquire(storageRequest("restricted-native"), nil)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(l.ManagedDir, "sentinel")
	if err = os.WriteFile(file, []byte("must survive denied observation"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = m.storage.Survey(t.Context(), *l.Artifact)
	if err == nil {
		t.Skip("native activity observation is available; restricted-path fixture not applicable")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unexpected observation failure: %v", err)
	}
	if _, err = m.End(l.ID, "released"); err != nil {
		t.Fatal(err)
	}
	storageEventually(t, func() bool { l, _ = m.Inspect(l.ID); return l.State == "needs_attention" })
	s, _ := m.Status()
	if s.Reserved.Disk != 1024 || l.Artifact.NamespaceRemoved || l.Artifact.LastError == "" {
		t.Fatalf("denied observation returned capacity or removed files: %+v", l)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "must survive denied observation" {
		t.Fatal("denied observation changed data", err)
	}
	storageEventually(t, func() bool { _, err := m.StorageHandoff(l.ID); return err == nil })
	if _, err = os.Stat(file); err != nil {
		t.Fatal("handoff removed retained data", err)
	}
}
