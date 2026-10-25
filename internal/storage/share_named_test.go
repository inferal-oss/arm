package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReadOnlyNamedObservationMatchesRealNativePaths(t *testing.T) {
	p, a := fixture(t)
	for _, item := range []struct {
		name string
		fd   int
		path string
	}{{"provider", unix.AT_FDCWD, p.path}, {"artifact", int(p.root.Fd()), a.Name}} {
		t.Run(item.name, func(t *testing.T) {
			got, err := readOnlyNamedAt(item.fd, item.path)
			if err != nil {
				t.Fatal(err)
			}
			id, err := durableIdentity(item.fd, item.path)
			if err != nil {
				t.Fatal(err)
			}
			var s unix.Stat_t
			if err = unix.Fstatat(item.fd, item.path, &s, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				t.Fatal(err)
			}
			if got.Identity != id || got.Mode != uint32(s.Mode) || got.Mode&unix.S_IFMT != unix.S_IFDIR {
				t.Fatal("named observation differs from independent native reads", got, id, s.Mode)
			}
		})
	}
	link := filepath.Join(p.path, "link")
	if err := os.Symlink(a.Path, link); err != nil {
		t.Fatal(err)
	}
	got, err := readOnlyNamedAt(int(p.root.Fd()), "link")
	if err != nil || got.Mode&unix.S_IFMT != unix.S_IFLNK || got.Identity == *a.Identity {
		t.Fatal("named metadata followed a symlink", got, err)
	}
}

func TestReadOnlyNamedOpenRefusesChangedRootAndArtifactWithoutMutation(t *testing.T) {
	for _, kind := range []string{"provider-symlink", "provider-file", "artifact-file", "provider-inode", "provider-owner", "provider-birth", "provider-birth-unknown", "artifact-owner", "artifact-birth", "artifact-birth-unknown"} {
		t.Run(kind, func(t *testing.T) {
			p, a := fixture(t)
			marker := filepath.Join(a.Path, "sentinel")
			if err := os.WriteFile(marker, []byte("retain original"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "provider-symlink", "provider-file", "artifact-file":
				target := p.path
				if kind == "artifact-file" {
					target = a.Path
				}
				if err := os.Rename(target, target+"-old"); err != nil {
					t.Fatal(err)
				}
				if kind == "provider-symlink" {
					if err := os.Symlink(target+"-old", target); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(target, []byte("retain replacement"), 0600); err != nil {
					t.Fatal(err)
				}
				marker = filepath.Join(target+"-old", "sentinel")
				if kind != "artifact-file" {
					marker = filepath.Join(target+"-old", a.Name, "sentinel")
				}
			case "provider-inode":
				p.identity.Inode++
			case "provider-owner":
				p.identity.Owner++
			case "provider-birth":
				p.identity.BornNanoseconds++
			case "provider-birth-unknown":
				p.identity.BirthKnown = false
			case "artifact-owner", "artifact-birth", "artifact-birth-unknown":
				id := *a.Identity
				switch kind {
				case "artifact-owner":
					id.Owner++
				case "artifact-birth":
					id.BornNanoseconds++
				case "artifact-birth-unknown":
					id.BirthKnown = false
				}
				a.Identity = &id
			}
			ordinary, ordinaryErr := p.OpenArtifactReadOnly(context.Background(), a)
			if ordinary != nil {
				ordinary.Close()
			}
			if ordinaryErr == nil {
				t.Fatal("ordinary negative control unexpectedly admitted", kind)
			}
			f, got, err := p.OpenArtifactReadOnlyObserved(context.Background(), a)
			if f != nil {
				f.Close()
			}
			if err == nil || f != nil || got != (ReadOnlyObservation{}) {
				t.Fatal("named read-only resolution disclosed refused allocation", got, err)
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "retain original" {
				t.Fatal("refusal changed original data", string(data), err)
			}
		})
	}
}
