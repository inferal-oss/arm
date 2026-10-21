package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yrashk/arm/internal/model"
)

func fixture(t *testing.T) (*Provider, model.Artifact) {
	t.Helper()
	p, err := Open(filepath.Join(t.TempDir(), "storage"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	name := "0123456789abcdef0123456789abcdef"
	id, err := p.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	return p, model.Artifact{Name: name, Path: p.Path(name), Identity: &id, Phase: "owned"}
}
func deadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestCleanupNeverFollowsSymlinksOrExternalHardLinks(t *testing.T) {
	p, a := fixture(t)
	outside := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(outside, []byte("preserve this outside file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(a.Path, "symlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(a.Path, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(a.Path, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.Path, "nested", "payload"), []byte("artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx := deadline(t)
	survey, err := p.Survey(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(survey.Active) > 0 {
		t.Fatalf("unexpected active users: %v", survey.Active)
	}
	if err := p.Quarantine(a); err != nil {
		t.Fatal(err)
	}
	finished := false
	for i := 0; i < 10; i++ {
		count, done, err := p.Step(ctx, a, 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		if count > 1 {
			t.Fatalf("cleanup exceeded step budget: %d", count)
		}
		if done {
			finished = true
			break
		}
	}
	if !finished {
		t.Fatal("bounded cleanup never completed")
	}
	if _, err := os.Lstat(p.Path(".reclaim-" + a.Name)); !os.IsNotExist(err) {
		t.Fatal("quarantine remains")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "preserve this outside file" {
		t.Fatal("outside sentinel was modified")
	}
}

func TestOpenFileBlocksCleanupAndCanBeRetried(t *testing.T) {
	p, a := fixture(t)
	path := filepath.Join(a.Path, "held-open")
	if err := os.WriteFile(path, []byte("active"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx := deadline(t)
	survey, err := p.Survey(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(survey.Active) == 0 {
		t.Fatal("active file descriptor was not detected")
	}
	if err := p.Quarantine(a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.Step(ctx, a, 128, nil); err == nil {
		t.Fatal("removed an active allocation")
	}
	if _, err := os.Stat(p.Path(".reclaim-"+a.Name) + "/held-open"); err != nil {
		t.Fatal("active file was removed")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, done, err := p.Step(ctx, a, 128, nil); err != nil || !done {
		t.Fatalf("cleanup failed after use stopped: %v %v", done, err)
	}
}

func TestReplacedRootAndArtifactArePreserved(t *testing.T) {
	for _, replaceRoot := range []bool{false, true} {
		t.Run(map[bool]string{false: "artifact", true: "root"}[replaceRoot], func(t *testing.T) {
			p, a := fixture(t)
			target := a.Path
			if replaceRoot {
				target = p.path
			}
			if err := os.Rename(target, target+"-original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(target, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(target, "sentinel")
			if err := os.WriteFile(marker, []byte("unrelated"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := p.Quarantine(a); err == nil {
				t.Fatal("accepted a replaced directory")
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("replacement sentinel was touched")
			}
		})
	}
}

func TestQuarantineCrashWindowAndMissingIdentity(t *testing.T) {
	p, a := fixture(t)
	if err := p.Quarantine(a); err != nil {
		t.Fatal(err)
	}
	// The durable record can still say owned after a crash at this point.
	if err := p.Check(a); err != nil {
		t.Fatal(err)
	}
	if err := p.Quarantine(a); err != nil {
		t.Fatal(err)
	}
	if _, done, err := p.Step(deadline(t), a, 128, nil); err != nil || !done {
		t.Fatalf("crash-window reconciliation: %v %v", done, err)
	}
	a.Identity = nil
	if err := p.Check(a); err == nil {
		t.Fatal("adopted a directory without recorded identity")
	}
}

func TestRecycledInodeCannotMatchOldCreationIdentity(t *testing.T) {
	p, a := fixture(t)
	if !p.Identity().BirthKnown || !a.Identity.BirthKnown {
		t.Fatal("ownership lacks creation-time identity")
	}
	old := *a.Identity
	old.BornSeconds--
	a.Identity = &old
	if err := p.Quarantine(a); err == nil {
		t.Fatal("an old creation identity authorized cleanup of a different directory generation")
	}
	if _, err := os.Stat(a.Path); err != nil {
		t.Fatal("identity mismatch changed the artifact")
	}
}
