package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadyReaderCannotObservePausedPublisherStage(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "ready.json")
	raw := []byte("{\"ready\":true}\n")
	staged := make(chan string, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	writerJoined := false
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	go func() {
		done <- publishReady(final, raw, func(path string) error {
			staged <- path
			<-release
			return nil
		})
	}()
	defer func() {
		unblock()
		if writerJoined {
			return
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("owned publisher did not join")
		}
	}()
	var stage string
	select {
	case stage = <-staged:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher did not reach closed stage")
	}
	info, err := os.Stat(stage)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
	if got, err := os.ReadFile(stage); err != nil || !bytes.Equal(got, raw) {
		t.Fatal(string(got), err)
	}
	// Actual reader runs while the writer is paused, after native Sync+Close.
	// It may only wait for absence. Once final exists, one strict JSON read must
	// succeed; there is no retry of malformed/empty readiness.
	if _, err = os.ReadFile(final); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial final visible", err)
	}
	unblock()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, e := os.ReadFile(final)
		if errors.Is(e, os.ErrNotExist) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(got, raw) {
			t.Fatalf("published partial readiness %q", got)
		}
		var value struct {
			Ready bool `json:"ready"`
		}
		decoder := json.NewDecoder(bytes.NewReader(got))
		decoder.DisallowUnknownFields()
		if e = decoder.Decode(&value); e != nil || !value.Ready {
			t.Fatal(value, e)
		}
		break
	}
	select {
	case err = <-done:
		writerJoined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publisher did not finish")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "ready.json" {
		t.Fatal(entries, err)
	}
}

func TestReadyNeverReplacesExistingFinalOrStagingSentinels(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			final := filepath.Join(root, "ready.json")
			sentinel := filepath.Join(root, "sentinel")
			if err := os.WriteFile(sentinel, []byte("keep exact sentinel"), 0400); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				if err := os.WriteFile(final, []byte("unrelated final"), 0400); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(sentinel, final); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(final, 0700); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(final)
			if err != nil {
				t.Fatal(err)
			}
			occupied := filepath.Join(root, ".arm-ready-preexisting")
			if err = os.WriteFile(occupied, []byte("unrelated stage prefix"), 0400); err != nil {
				t.Fatal(err)
			}
			stageBorn, err := os.Stat(occupied)
			if err != nil {
				t.Fatal(err)
			}
			if err = publishReady(final, []byte(`{"ready":true}`), nil); err == nil {
				t.Fatal("preexisting final adopted/replaced")
			}
			after, e := os.Lstat(final)
			if e != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("final identity changed", e)
			}
			if kind == "file" {
				if raw, e := os.ReadFile(final); e != nil || string(raw) != "unrelated final" {
					t.Fatal(string(raw), e)
				}
			}
			stageAfter, e := os.Stat(occupied)
			if e != nil || !os.SameFile(stageBorn, stageAfter) {
				t.Fatal("preexisting stage touched", e)
			}
			if raw, e := os.ReadFile(occupied); e != nil || string(raw) != "unrelated stage prefix" {
				t.Fatal(string(raw), e)
			}
			if raw, e := os.ReadFile(sentinel); e != nil || string(raw) != "keep exact sentinel" {
				t.Fatal(string(raw), e)
			}
			entries, e := os.ReadDir(root)
			if e != nil || len(entries) != 3 {
				t.Fatal("owned failed stage leaked", entries, e)
			}
		})
	}
}

func TestReadyPreservesChangedStagingIdentityAndRefusesPublication(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "ready.json")
	retained := filepath.Join(root, "retained-owned-stage")
	var replaced string
	err := publishReady(final, []byte(`{"ready":true}`), func(stage string) error {
		replaced = stage
		if e := os.Rename(stage, retained); e != nil {
			return e
		}
		return os.WriteFile(stage, []byte("replacement sentinel"), 0600)
	})
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatal(err)
	}
	if _, e := os.Lstat(final); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("unexpected final", e)
	}
	if raw, e := os.ReadFile(replaced); e != nil || string(raw) != "replacement sentinel" {
		t.Fatal(string(raw), e)
	}
	if raw, e := os.ReadFile(retained); e != nil || string(raw) != `{"ready":true}` {
		t.Fatal(string(raw), e)
	}
}

func TestReadyErrorBeforePublicationOnlyCleansOwnedStage(t *testing.T) {
	root := t.TempDir()
	final := filepath.Join(root, "ready.json")
	injected := fmt.Errorf("fixture staged callback failure")
	if err := publishReady(final, []byte(`{"ready":true}`), func(string) error { return injected }); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("owned failed stage retained", entries, err)
	}
	for _, raw := range [][]byte{nil, []byte("{"), bytes.Repeat([]byte(" "), 16385)} {
		if err = publishReady(final, raw, nil); err == nil {
			t.Fatal("invalid readiness accepted")
		}
	}
	entries, err = os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}
