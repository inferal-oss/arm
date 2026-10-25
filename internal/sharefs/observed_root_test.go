package sharefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestObservedRootMatchesCompletePublishedVersion(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	m := built(t, e, p, a, b)
	f, snapshot, err := p.OpenArtifactReadOnlyObserved(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	independent, err := version(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if Version(snapshot) != independent || independent != m.Entries[0].Version {
		t.Fatal("storage/sharefs native observations differ", snapshot, independent, m.Entries[0].Version)
	}
}

func TestObservedRootRefusesEveryChangedPublishedField(t *testing.T) {
	mutations := map[string]func(*Version){
		"device": func(v *Version) { v.Identity.Device++ }, "inode": func(v *Version) { v.Identity.Inode++ }, "owner": func(v *Version) { v.Identity.Owner++ },
		"birth-seconds": func(v *Version) { v.Identity.BornSeconds++ }, "birth-nanos": func(v *Version) { v.Identity.BornNanoseconds++ }, "birth-known": func(v *Version) { v.Identity.BirthKnown = false },
		"mount": func(v *Version) { v.Mount += "-changed" }, "size": func(v *Version) { v.Size++ }, "mode": func(v *Version) { v.Mode ^= 0100 }, "links": func(v *Version) { v.Links++ },
		"mtime-seconds": func(v *Version) { v.ModifiedSeconds++ }, "mtime-nanos": func(v *Version) { v.ModifiedNanos++ }, "ctime-seconds": func(v *Version) { v.ChangedSeconds++ }, "ctime-nanos": func(v *Version) { v.ChangedNanos++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			e, p, a, b := fixture(t)
			m := built(t, e, p, a, b)
			o := opened(t, e, p, a, m, permit)
			// Change one expected field after valid publication/open. This isolates
			// the full native root comparison instead of failing manifest syntax.
			entry := o.entries[""]
			mutate(&entry.Version)
			o.entries[""] = entry
			if got, err := o.Stat(context.Background(), ""); err == nil || !errors.Is(err, ErrChanged) || got != (Info{}) {
				t.Fatal("incomplete root comparison", got, err)
			}
		})
	}
}

func TestObservedRootRetainsColdAndWarmReadGuardCuts(t *testing.T) {
	e, p, a, b := fixture(t)
	write(t, filepath.Join(a.Path, "file"), []byte("approved"))
	m := built(t, e, p, a, b)
	calls := 0
	o := opened(t, e, p, a, m, func(_ context.Context, x Access) error {
		if x.Operation == "read" {
			calls++
		}
		return nil
	})
	h := handle(t, o, "file")
	for _, wanted := range []int{6, 4} {
		calls = 0
		if got, err := o.Read(context.Background(), h, 0, 8); err != nil || string(got) != "approved" || calls != wanted {
			t.Fatal("read guard cut changed", string(got), err, calls, wanted)
		}
	}
}

func TestObservedRootReplacementBeforeFinalWalkDisclosesNoBytes(t *testing.T) {
	for _, kind := range []string{"provider", "allocation", "provider-close"} {
		for _, warm := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "-cold", true: "-warm"}[warm], func(t *testing.T) {
				e, p, a, b := fixture(t)
				write(t, filepath.Join(a.Path, "file"), []byte("approved"))
				m := built(t, e, p, a, b)
				armed := false
				calls := 0
				cut := 5
				if warm {
					cut = 3
				}
				o := opened(t, e, p, a, m, func(_ context.Context, x Access) error {
					if x.Operation != "read" || !armed {
						return nil
					}
					calls++
					if calls != cut {
						return nil
					}
					if kind == "provider-close" {
						return p.Close()
					}
					path := a.Path
					if kind == "provider" {
						path = filepath.Dir(a.Path)
					}
					if err := os.Rename(path, path+"-original"); err != nil {
						return err
					}
					return os.Mkdir(path, 0700)
				})
				h := handle(t, o, "file")
				if warm {
					if _, err := o.Read(context.Background(), h, 0, 8); err != nil {
						t.Fatal(err)
					}
				}
				armed = true
				if got, err := o.Read(context.Background(), h, 0, 8); got != nil || err == nil || calls < cut {
					t.Fatal("replacement/Close escaped final current path check", string(got), err, calls, cut)
				}
			})
		}
	}
}
