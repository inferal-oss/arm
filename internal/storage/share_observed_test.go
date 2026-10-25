package storage

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func TestObservedReadOnlyOpenMatchesActualOpenedRoot(t *testing.T) {
	p, a := fixture(t)
	f, observed, err := p.OpenArtifactReadOnlyObserved(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	id, err := DirectoryIdentity(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	mount, err := mountIdentity(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	var s unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &s); err != nil {
		t.Fatal(err)
	}
	if observed.Identity != id || id != *a.Identity || observed.Mount != mount || mount != p.mount || observed.Mode != uint32(s.Mode) || observed.Size != s.Size || observed.Links != uint64(s.Nlink) || observed.ModifiedSeconds != s.Mtim.Sec || observed.ModifiedNanos != s.Mtim.Nsec || observed.ChangedSeconds != s.Ctim.Sec || observed.ChangedNanos != s.Ctim.Nsec {
		t.Fatal("observation differs from actual opened descriptor", observed, s)
	}
	flags, err := unix.FcntlInt(f.Fd(), unix.F_GETFL, 0)
	if err != nil || flags&unix.O_ACCMODE != unix.O_RDONLY {
		t.Fatal("not a read-only descriptor", flags, err)
	}
	wrong := id
	wrong.Inode++
	if got, err := readOnlyOpenedObservation(int(f.Fd()), wrong); err == nil || got != (ReadOnlyObservation{}) {
		t.Fatal("wrong opened identity disclosed observation", got, err)
	}
}

func TestObservedReadOnlyOpenPreservesOrdinaryRefusals(t *testing.T) {
	for _, kind := range []string{"phase", "path", "name", "identity", "missing-identity", "missing", "quarantine", "both-names", "provider-replaced", "artifact-replaced", "artifact-symlink", "provider-mode", "mount", "closed", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			p, a := fixture(t)
			ctx := context.Background()
			switch kind {
			case "phase":
				a.Phase = "quarantined"
			case "path":
				a.Path = filepath.Dir(a.Path)
			case "name":
				a.Name = ".."
				a.Path = p.Path(a.Name)
			case "identity":
				v := *a.Identity
				v.Inode++
				a.Identity = &v
			case "missing-identity":
				a.Identity = nil
			case "missing":
				if err := os.Remove(a.Path); err != nil {
					t.Fatal(err)
				}
			case "quarantine":
				if err := os.Rename(a.Path, p.Path(".reclaim-"+a.Name)); err != nil {
					t.Fatal(err)
				}
			case "both-names":
				if err := os.Mkdir(p.Path(".reclaim-"+a.Name), 0700); err != nil {
					t.Fatal(err)
				}
			case "provider-replaced":
				if err := os.Rename(p.path, p.path+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(p.path, 0700); err != nil {
					t.Fatal(err)
				}
			case "artifact-replaced":
				if err := os.Rename(a.Path, a.Path+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(a.Path, 0700); err != nil {
					t.Fatal(err)
				}
			case "artifact-symlink":
				if err := os.Rename(a.Path, a.Path+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(a.Path+"-old", a.Path); err != nil {
					t.Fatal(err)
				}
			case "provider-mode":
				if err := os.Chmod(p.path, 0777); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(p.path, 0700)
			case "mount":
				p.mount += "-not-current"
			case "closed":
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			ordinary, err := p.OpenArtifactReadOnly(ctx, a)
			if ordinary != nil {
				ordinary.Close()
			}
			if err == nil {
				t.Fatal("ordinary refusal fixture did not refuse", kind)
			}
			observed, snapshot, err := p.OpenArtifactReadOnlyObserved(ctx, a)
			if observed != nil {
				observed.Close()
			}
			if err == nil || observed != nil || snapshot != (ReadOnlyObservation{}) {
				t.Fatal("observed open weakened refusal", kind, snapshot, err)
			}
		})
	}
	var absent *Provider
	if f, s, err := absent.OpenArtifactReadOnlyObserved(context.Background(), model.Artifact{}); err == nil || f != nil || s != (ReadOnlyObservation{}) {
		t.Fatal("absent provider accepted", f, s, err)
	}
}

func TestObservedReadOnlyOpenConcurrentProviderClose(t *testing.T) {
	p, a := fixture(t)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, s, err := p.OpenArtifactReadOnlyObserved(context.Background(), a)
			if err != nil {
				if f != nil || s != (ReadOnlyObservation{}) {
					t.Error("failed open disclosed descriptor/observation")
				}
				return
			}
			if f == nil || s.Identity != *a.Identity {
				t.Error("unbound opened descriptor")
			}
			if f != nil {
				f.Close()
			}
		}()
	}
	_ = p.Close()
	wg.Wait()
	if f, s, err := p.OpenArtifactReadOnlyObserved(context.Background(), a); err == nil || f != nil || s != (ReadOnlyObservation{}) {
		if f != nil {
			f.Close()
		}
		t.Fatal("closed provider regained authority", s, err)
	}
}
