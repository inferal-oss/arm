package cargoprofile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yrashk/arm/internal/model"
	"github.com/yrashk/arm/internal/storage"
	"golang.org/x/sys/unix"
)

// OwnedDirectory is evidence from an exclusive birth, never an instruction to
// adopt a similarly named existing directory or independently erase its tree.
type OwnedDirectory struct {
	Name     string             `json:"name"`
	Path     string             `json:"path"`
	Identity model.FileIdentity `json:"identity"`
}
type OwnedPaths struct {
	AllocationPath     string             `json:"allocation_path"`
	AllocationIdentity model.FileIdentity `json:"allocation_identity"`
	Root               OwnedDirectory     `json:"root"`
	Target             OwnedDirectory     `json:"target"`
	Build              OwnedDirectory     `json:"build"`
	Control            OwnedDirectory     `json:"control"`
}

// BirthStep is acknowledged durably before each mutation and again after its
// fsynced identity exists. Failure never erases or adopts a partial birth.
type BirthStep struct {
	Step int                 `json:"step"`
	Path string              `json:"path"`
	Born *model.FileIdentity `json:"born,omitempty"`
}

var cargoLeaseID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func CreateOwnedPaths(ctx context.Context, allocation string, expected model.FileIdentity, lease string, record func(BirthStep) error) (out OwnedPaths, err error) {
	if !cargoLeaseID.MatchString(lease) || record == nil {
		return out, fmt.Errorf("exclusive Cargo lease identity and durable birth recorder required")
	}
	parent, e := openOwnedDirectory(allocation, expected)
	if e != nil {
		return out, e
	}
	defer parent.Close()
	out.AllocationPath, out.AllocationIdentity = allocation, expected
	rootName := "cargo-" + lease
	if len(filepath.Join(allocation, rootName, "control")) > 4096 {
		return out, fmt.Errorf("Cargo owned directory path exceeds bound")
	}
	makeOne := func(parent *os.File, name, path string, step int) (OwnedDirectory, *os.File, error) {
		if e := ctx.Err(); e != nil {
			return OwnedDirectory{}, nil, e
		}
		if e := record(BirthStep{Step: step, Path: path}); e != nil {
			return OwnedDirectory{}, nil, e
		}
		// A callback can wait for a durable parent acknowledgement. Recheck after
		// it returns, before touching the directory namespace.
		if e := ctx.Err(); e != nil {
			return OwnedDirectory{}, nil, e
		}
		if e := verifyOwnedDirectory(parent, expected.Owner, nil); e != nil {
			return OwnedDirectory{}, nil, e
		}
		if e := unix.Mkdirat(int(parent.Fd()), name, 0700); e != nil {
			return OwnedDirectory{}, nil, fmt.Errorf("Cargo directory birth refused; existing names are never adopted: %w", e)
		}
		fd, e := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return OwnedDirectory{}, nil, e
		}
		f := os.NewFile(uintptr(fd), path)
		fail := func(e error) (OwnedDirectory, *os.File, error) { f.Close(); return OwnedDirectory{}, nil, e }
		if e = verifyOwnedDirectory(f, expected.Owner, nil); e != nil {
			return fail(e)
		}
		id, e := storage.DirectoryIdentity(fd)
		if e != nil {
			return fail(e)
		}
		if id.Device != expected.Device {
			return fail(fmt.Errorf("Cargo directory crosses allocation filesystem"))
		}
		if e = f.Sync(); e != nil {
			return fail(e)
		}
		if e = parent.Sync(); e != nil {
			return fail(e)
		}
		born := OwnedDirectory{Name: name, Path: path, Identity: id}
		if e = record(BirthStep{Step: step, Path: path, Born: &id}); e != nil {
			return born, f, e
		}
		return born, f, nil
	}
	rootPath := filepath.Join(allocation, rootName)
	root, rootFD, e := makeOne(parent, rootName, rootPath, 1)
	out.Root = root
	if rootFD != nil {
		defer rootFD.Close()
	}
	if e != nil {
		return out, e
	}
	for i, item := range []struct {
		name string
		dest *OwnedDirectory
	}{{"target", &out.Target}, {"build", &out.Build}, {"control", &out.Control}} {
		child, f, e := makeOne(rootFD, item.name, filepath.Join(rootPath, item.name), i+2)
		*item.dest = child
		if f != nil {
			f.Close()
		}
		if e != nil {
			return out, e
		}
	}
	if e = out.Revalidate(ctx); e != nil {
		return out, e
	}
	return out, nil
}
func verifyOwnedDirectory(f *os.File, owner uint32, expected *model.FileIdentity) error {
	var st unix.Stat_t
	if e := unix.Fstat(int(f.Fd()), &st); e != nil {
		return e
	}
	if st.Mode != unix.S_IFDIR|0700 || st.Uid != owner {
		return fmt.Errorf("Cargo directory is not an exact private owned directory")
	}
	if expected != nil {
		actual, e := storage.DirectoryIdentity(int(f.Fd()))
		if e != nil || actual != *expected {
			return fmt.Errorf("Cargo directory native birth identity changed")
		}
	}
	return nil
}
func openOwnedDirectory(path string, expected model.FileIdentity) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || !validText(path) || len(path) > 4096 || expected.Inode == 0 || !expected.BirthKnown || expected.Owner != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("verified physical managed allocation identity required")
	}
	f, e := openConfigNoFollow(path)
	if e != nil {
		return nil, fmt.Errorf("Cargo managed directory unavailable")
	}
	if e = verifyOwnedDirectory(f, expected.Owner, &expected); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func (o OwnedPaths) Roots() Roots {
	return Roots{Target: o.Target.Path, Build: o.Build.Path, Control: o.Control.Path}
}
func (o OwnedPaths) Revalidate(ctx context.Context) error {
	parent, e := openOwnedDirectory(o.AllocationPath, o.AllocationIdentity)
	if e != nil {
		return e
	}
	defer parent.Close()
	if len(o.Root.Name) != len("cargo-")+32 || !strings.HasPrefix(o.Root.Name, "cargo-") || !cargoLeaseID.MatchString(o.Root.Name[len("cargo-"):]) || o.Root.Path != filepath.Join(o.AllocationPath, o.Root.Name) {
		return fmt.Errorf("Cargo root evidence is malformed")
	}
	for _, item := range []struct {
		d      OwnedDirectory
		name   string
		parent string
	}{{o.Root, o.Root.Name, o.AllocationPath}, {o.Target, "target", o.Root.Path}, {o.Build, "build", o.Root.Path}, {o.Control, "control", o.Root.Path}} {
		if e = ctx.Err(); e != nil {
			return e
		}
		if item.d.Name != item.name || item.d.Path != filepath.Join(item.parent, item.name) || item.d.Identity.Device != o.AllocationIdentity.Device {
			return fmt.Errorf("Cargo born directory evidence is inconsistent")
		}
		f, e := openOwnedDirectory(item.d.Path, item.d.Identity)
		if e != nil {
			return e
		}
		f.Close()
	}
	// Resolve the canonical allocation again after checking children, so a
	// renamed/replaced ancestor cannot turn old descriptor checks into authority.
	final, e := openOwnedDirectory(o.AllocationPath, o.AllocationIdentity)
	if e != nil {
		return e
	}
	return final.Close()
}

// CanonicalAllocation resolves permitted path aliases only while matching the
// already-recorded native birth identity. It never grants ownership by path.
// Call outside Manager.mu; later operations use the physical no-follow path.
func CanonicalAllocation(path string, expected model.FileIdentity) (string, error) {
	if !filepath.IsAbs(path) || !validText(path) || len(path) > 4096 {
		return "", fmt.Errorf("managed allocation path is invalid")
	}
	physical, e := filepath.EvalSymlinks(path)
	if e != nil {
		return "", fmt.Errorf("managed allocation path is unavailable")
	}
	f, e := openOwnedDirectory(physical, expected)
	if e != nil {
		return "", e
	}
	f.Close()
	return physical, nil
}
