package agentstatus

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStoreAtomicLimitsAndPrivacy(t *testing.T) {
	home := t.TempDir()
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(s *State) error { s.Day = "2026-01-01"; s.DailyCalls++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Update(func(s *State) error { s.Entries = make([]Entry, MaxIdentities+1); return nil })
	if err == nil {
		t.Fatal("capacity accepted")
	}
	s, err := store.Update(nil)
	if err != nil || s.DailyCalls != 1 || len(s.Entries) != 0 {
		t.Fatal("failed change altered durable state", err)
	}
	info, err := os.Stat(filepath.Join(home, "status-v1", "state.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("nonprivate state", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, e := OpenStore(home)
			if e == nil {
				_, e = other.Update(func(s *State) error { s.Day = "2026-01-01"; s.DailyCalls++; return nil })
			}
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	s, err = store.Update(nil)
	if err != nil || s.DailyCalls != 9 {
		t.Fatal("lost concurrent update", err)
	}
}
func TestStoreRefusesAliasesAndUnknownState(t *testing.T) {
	home := t.TempDir()
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "status-v1", "state.json")
	if err = os.Symlink(filepath.Join(home, "outside"), path); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(nil); err == nil {
		t.Fatal("symlink followed")
	}
	os.Remove(path)
	if err = os.WriteFile(path, []byte(`{"version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(func(s *State) error { return nil }); err == nil {
		t.Fatal("unknown state overwritten")
	}
}

func TestStorePreservesInterruptedPublicationEvidence(t *testing.T) {
	home := t.TempDir()
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "status-v1", ".state-interrupted")
	if err = os.WriteFile(path, []byte("synthetic interrupted evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(nil); err == nil {
		t.Fatal("interrupted publication was ignored")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "synthetic interrupted evidence" {
		t.Fatal("evidence changed")
	}
}

func TestStoreRefusesNonregularLeafWithoutBlocking(t *testing.T) {
	home := t.TempDir()
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err = unix.Mkfifo(filepath.Join(home, "status-v1", "state.json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Update(nil); err == nil {
		t.Fatal("nonregular status leaf accepted")
	}
}
