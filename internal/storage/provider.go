// Package storage operates only on newly created ARM-owned directories. It does
// not adopt a path because of its name and does not use recursive RemoveAll.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

var validName = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Provider struct {
	mu            sync.Mutex
	path          string
	root          *os.File
	identity      model.FileIdentity
	mount         string
	snapshotSlots chan struct{} // runtime-only owned S copy slots; never reconstructed from rows
}

func Identity(s *unix.Stat_t) model.FileIdentity {
	return model.FileIdentity{Device: uint64(s.Dev), Inode: s.Ino, Owner: s.Uid}
}
func Open(path string, expected *model.FileIdentity) (*Provider, error) {
	if expected == nil {
		if err := os.Mkdir(path, 0700); err != nil {
			return nil, err
		}
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	p := &Provider{path: path, root: f}
	var s unix.Stat_t
	if err = unix.Fstat(fd, &s); err == nil {
		p.identity, err = durableIdentity(fd, "")
		if s.Uid != uint32(os.Geteuid()) || s.Mode&0077 != 0 {
			err = fmt.Errorf("managed root must be private and owned by ARM's user")
		}
		if expected != nil && p.identity != *expected {
			err = fmt.Errorf("managed root identity changed")
		}
	}
	if err == nil {
		p.mount, err = mountIdentity(fd)
	}
	if err == nil && expected == nil {
		parent, e := os.Open(filepath.Dir(path))
		if e == nil {
			e = parent.Sync()
			parent.Close()
		}
		err = e
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return p, nil
}
func (p *Provider) Close() error                 { return p.root.Close() }
func (p *Provider) Identity() model.FileIdentity { return p.identity }
func (p *Provider) Check(a model.Artifact) error {
	// Read-only checks of an active allocation must not wait behind an
	// unrelated directory's potentially slow activity inspection.
	_, f, err := p.locate(a)
	if f != nil {
		f.Close()
	}
	return err
}
func (p *Provider) Path(name string) string { return filepath.Join(p.path, name) }
func (p *Provider) validate() error {
	var s unix.Stat_t
	if err := unix.Lstat(p.path, &s); err != nil {
		return err
	}
	id, err := durableIdentity(unix.AT_FDCWD, p.path)
	if err != nil {
		return err
	}
	if s.Mode&unix.S_IFMT != unix.S_IFDIR || id != p.identity || s.Mode&0077 != 0 {
		return fmt.Errorf("managed root was replaced or its permissions changed")
	}
	return nil
}
func (p *Provider) Create(name string) (model.FileIdentity, error) {
	// The authority serializes creation; names are unique and exclusive mkdir
	// is safe alongside cleanup of a different artifact.
	if !validName.MatchString(name) {
		return model.FileIdentity{}, fmt.Errorf("invalid artifact name")
	}
	if err := p.validate(); err != nil {
		return model.FileIdentity{}, err
	}
	fd := int(p.root.Fd())
	if err := unix.Mkdirat(fd, name, 0700); err != nil {
		return model.FileIdentity{}, err
	}
	var s unix.Stat_t
	if err := unix.Fstatat(fd, name, &s, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return model.FileIdentity{}, err
	}
	if s.Mode&unix.S_IFMT != unix.S_IFDIR || s.Uid != p.identity.Owner {
		return model.FileIdentity{}, fmt.Errorf("created artifact identity changed")
	}
	if err := p.root.Sync(); err != nil {
		return model.FileIdentity{}, err
	}
	return durableIdentity(fd, name)
}

// locate never infers ownership from a directory name. A crash after an
// exclusive quarantine rename can be reconciled using the original identity.
func (p *Provider) locate(a model.Artifact) (string, *os.File, error) {
	if err := p.validate(); err != nil {
		return "", nil, err
	}
	if !validName.MatchString(a.Name) || a.Identity == nil {
		return "", nil, fmt.Errorf("artifact has no verified identity")
	}
	var found string
	for _, name := range []string{a.Name, ".reclaim-" + a.Name} {
		var s unix.Stat_t
		err := unix.Fstatat(int(p.root.Fd()), name, &s, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		if found != "" {
			return "", nil, fmt.Errorf("both artifact and quarantine names exist")
		}
		id, err := durableIdentity(int(p.root.Fd()), name)
		if err != nil {
			return "", nil, err
		}
		if s.Mode&unix.S_IFMT != unix.S_IFDIR || id != *a.Identity {
			return "", nil, fmt.Errorf("artifact directory was replaced")
		}
		found = name
	}
	if found == "" {
		return "", nil, os.ErrNotExist
	}
	fd, err := unix.Openat(int(p.root.Fd()), found, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", nil, err
	}
	f := os.NewFile(uintptr(fd), found)
	id, err := durableIdentity(fd, "")
	if err == nil && id != *a.Identity {
		err = fmt.Errorf("artifact changed while opening")
	}
	if err == nil {
		var mount string
		mount, err = mountIdentity(fd)
		if err == nil && mount != p.mount {
			err = fmt.Errorf("artifact crosses a mount boundary")
		}
	}
	if err != nil {
		f.Close()
		return "", nil, err
	}
	return found, f, nil
}

type Survey struct {
	Entries      int
	Allocated    int64
	Active       []int
	identities   map[model.FileIdentity]bool
	beforeRemove func() error
}

// survey validates the entire candidate before any deletion. The subsequent
// deletion walk rechecks every opened directory, including its mount identity.
func (p *Provider) survey(ctx context.Context, f *os.File) (Survey, error) {
	s := Survey{identities: map[model.FileIdentity]bool{}}
	err := p.walk(ctx, f, 0, &s, false, 0)
	return s, err
}
func (p *Provider) Survey(ctx context.Context, a model.Artifact) (Survey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	name, f, err := p.locate(a)
	if err != nil {
		return Survey{}, err
	}
	defer f.Close()
	s, err := p.survey(ctx, f)
	if err == nil {
		s.Active, err = activeUsers(ctx, p.Path(name), s.identities)
	}
	return s, err
}

func (p *Provider) Quarantine(a model.Artifact) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	name, f, err := p.locate(a)
	if err != nil {
		return err
	}
	f.Close()
	if name == ".reclaim-"+a.Name {
		return nil
	}
	if err := renameExclusive(int(p.root.Fd()), name, ".reclaim-"+a.Name); err != nil {
		return err
	}
	return p.root.Sync()
}

// Step removes at most limit entries. It checks activity again after
// quarantine; an open file or working directory prevents all deletion.
func (p *Provider) Step(ctx context.Context, a model.Artifact, limit int, beforeRemove func() error) (int, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	name, f, err := p.locate(a)
	if errors.Is(err, os.ErrNotExist) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer f.Close()
	if name != ".reclaim-"+a.Name {
		return 0, false, fmt.Errorf("artifact must be quarantined before cleanup")
	}
	s, err := p.survey(ctx, f)
	if err != nil {
		return 0, false, err
	}
	active, err := activeUsers(ctx, p.Path(name), s.identities)
	if err != nil {
		return 0, false, err
	}
	if len(active) > 0 {
		return 0, false, fmt.Errorf("artifact is still open by process(es) %v", active)
	}
	if err := p.validate(); err != nil {
		return 0, false, err
	}
	// ReadDir uses an independent descriptor to avoid a stale directory offset
	// from the survey. No traversal follows a symlink or changes mount.
	fd, err := unix.Openat(int(p.root.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, false, err
	}
	dir := os.NewFile(uintptr(fd), name)
	defer dir.Close()
	opened, err := durableIdentity(fd, "")
	if err != nil {
		return 0, false, err
	}
	if opened != *a.Identity {
		return 0, false, fmt.Errorf("quarantine changed before cleanup")
	}
	progress := Survey{identities: map[model.FileIdentity]bool{}, beforeRemove: beforeRemove}
	err = p.walk(ctx, dir, 0, &progress, true, limit)
	if errors.Is(err, errStepLimit) {
		return progress.Entries, false, nil
	}
	if err != nil {
		return progress.Entries, false, err
	}
	current, err := durableIdentity(int(p.root.Fd()), name)
	if err != nil {
		return progress.Entries, false, err
	}
	if current != *a.Identity {
		return progress.Entries, false, fmt.Errorf("quarantine identity changed")
	}
	if progress.Entries >= limit {
		return progress.Entries, false, nil
	}
	if err := unix.Unlinkat(int(p.root.Fd()), name, unix.AT_REMOVEDIR); err != nil {
		return progress.Entries, false, err
	}
	if err := p.root.Sync(); err != nil {
		return progress.Entries, false, err
	}
	return progress.Entries + 1, true, nil
}

var errStepLimit = errors.New("cleanup step limit reached")

func (p *Provider) walk(ctx context.Context, dir *os.File, depth int, survey *Survey, remove bool, limit int) error {
	if depth > 128 {
		return fmt.Errorf("directory depth exceeds cleanup limit")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var ds unix.Stat_t
	if err := unix.Fstat(int(dir.Fd()), &ds); err != nil {
		return err
	}
	mount, err := mountIdentity(int(dir.Fd()))
	if err != nil {
		return err
	}
	if mount != p.mount || uint64(ds.Dev) != p.identity.Device {
		return fmt.Errorf("cleanup refuses a child mount")
	}
	if ds.Uid != p.identity.Owner {
		return fmt.Errorf("directory belongs to another owner")
	}
	if !remove {
		survey.identities[Identity(&ds)] = true
	}
	for {
		entries, readErr := dir.ReadDir(128)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if survey.Entries >= 100000 {
				return fmt.Errorf("cleanup entry limit exceeded")
			}
			if remove && survey.Entries >= limit {
				return errStepLimit
			}
			if remove && survey.beforeRemove != nil {
				if err := survey.beforeRemove(); err != nil {
					return err
				}
				survey.beforeRemove = nil
			}
			name := entry.Name()
			var st unix.Stat_t
			if err := unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			if uint64(st.Dev) != p.identity.Device {
				return fmt.Errorf("cleanup refuses a filesystem boundary")
			}
			if st.Uid != p.identity.Owner {
				return fmt.Errorf("entry belongs to another owner")
			}
			if st.Mode&unix.S_IFMT != unix.S_IFLNK {
				mount, err := mountAt(int(dir.Fd()), name)
				if err != nil {
					return err
				}
				if mount != p.mount {
					return fmt.Errorf("cleanup refuses a child mount")
				}
			}
			if st.Mode&unix.S_IFMT == unix.S_IFDIR {
				before, err := durableIdentity(int(dir.Fd()), name)
				if err != nil {
					return err
				}
				fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if err != nil {
					return err
				}
				child := os.NewFile(uintptr(fd), name)
				opened, err := durableIdentity(fd, "")
				if err == nil && opened != before {
					err = fmt.Errorf("child directory changed")
				}
				if err == nil {
					err = p.walk(ctx, child, depth+1, survey, remove, limit)
				}
				child.Close()
				if err != nil {
					return err
				}
				if remove {
					if survey.Entries >= limit {
						return errStepLimit
					}
					now, err := durableIdentity(int(dir.Fd()), name)
					if err != nil {
						return err
					}
					if now != before {
						return fmt.Errorf("child directory replaced during cleanup")
					}
					if err := unix.Unlinkat(int(dir.Fd()), name, unix.AT_REMOVEDIR); err != nil {
						return err
					}
				}
			} else {
				id := Identity(&st)
				if !remove && !survey.identities[id] {
					if st.Blocks < 0 || st.Blocks > math.MaxInt64/512 || survey.Allocated > math.MaxInt64-st.Blocks*512 {
						return fmt.Errorf("allocated counter overflow")
					}
					survey.Allocated += st.Blocks * 512
					survey.identities[id] = true
				}
				if remove {
					if err := unix.Unlinkat(int(dir.Fd()), name, 0); err != nil {
						return err
					}
				}
			}
			survey.Entries++
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
	}
}
