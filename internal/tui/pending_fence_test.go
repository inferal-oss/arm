package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yrashk/arm/internal/local"
)

func TestResetFencePreventsNewPendingIntentWithoutErasingUnknown(t *testing.T) {
	home := pendingHome(t)
	record := pendingFixture(t, "message")
	if err := persistPending(home, record, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, pendingDirectory, record.name())
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, local.ResetFenceName), []byte(`{"phase":"completed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []func() error{func() error { return persistPending(home, record, true) }, func() error { return settlePending(home, record) }, func() error { _, e := loadPending(home); return e }} {
		if err = action(); err == nil {
			t.Fatal("pending operation bypassed incomplete reset fence")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("fenced pending operation erased or changed unknown intent")
	}
	home = pendingHome(t)
	os.WriteFile(filepath.Join(home, local.ResetFenceName), nil, 0600)
	if err = persistPending(home, record, false); err == nil {
		t.Fatal("new intent persisted behind fence")
	}
	if _, err = os.Lstat(filepath.Join(home, pendingDirectory)); !os.IsNotExist(err) {
		t.Fatal("fenced attempt created pending namespace")
	}
}
