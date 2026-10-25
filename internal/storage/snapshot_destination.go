package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

func (p *Provider) snapshotCheckDirectory(file *os.File, owners *snapshotFDSet) (model.FileIdentity, error) {
	i, known := owners.files[file]
	if !known || owners.records[i].State != "open" || owners.records[i].Identity == nil {
		return model.FileIdentity{}, fmt.Errorf("destination directory lacks original owned descriptor")
	}
	id := *owners.records[i].Identity
	observed, err := readOnlyOpenedObservation(int(file.Fd()), id)
	if err != nil {
		return id, err
	}
	if observed.Identity.Owner != p.identity.Owner || observed.Mode&unix.S_IFMT != unix.S_IFDIR || observed.Mode&07077 != 0 || observed.Mount != p.mount {
		return id, fmt.Errorf("destination directory owner/private type/mount changed")
	}
	return id, nil
}

func (p *Provider) snapshotCheckNamedChild(parent *os.File, name string, child *os.File, owners *snapshotFDSet) error {
	i, known := owners.files[child]
	if !known || owners.records[i].State != "open" || owners.records[i].Identity == nil {
		return fmt.Errorf("child lacks original owned descriptor")
	}
	named, err := readOnlyNamedAt(int(parent.Fd()), name)
	if err != nil {
		return err
	}
	if named.Identity != *owners.records[i].Identity {
		return fmt.Errorf("destination child named/opened identity changed")
	}
	return nil
}

func (p *Provider) snapshotCheckNewFile(file *os.File, size int64, owners *snapshotFDSet) error {
	i, known := owners.files[file]
	if !known || owners.records[i].State != "open" || owners.records[i].Identity == nil {
		return fmt.Errorf("output lacks original owned descriptor")
	}
	actual, err := readOnlyOpenedObservation(int(file.Fd()), *owners.records[i].Identity)
	if err != nil {
		return err
	}
	if actual.Identity.Owner != p.identity.Owner || actual.Mount != p.mount || actual.Mode&unix.S_IFMT != unix.S_IFREG || actual.Links != 1 || actual.Mode&07777 != 0600 || actual.Size != size {
		return fmt.Errorf("new output type/owner/mount/private mode/identity/size/link changed before payload")
	}
	return nil
}

func (p *Provider) snapshotCheckArtifactName(a model.Artifact) error {
	if a.Identity == nil || a.Phase != "owned" || !validName.MatchString(a.Name) || a.Path != p.Path(a.Name) {
		return fmt.Errorf("snapshot requires exact owned managed allocation")
	}
	if err := p.validateReadOnlyNamed(); err != nil {
		return err
	}
	found := false
	for _, name := range []string{a.Name, ".reclaim-" + a.Name} {
		observed, err := readOnlyNamedAt(int(p.root.Fd()), name)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return err
		}
		if found || name != a.Name || observed.Mode&unix.S_IFMT != unix.S_IFDIR || observed.Identity != *a.Identity || observed.Mode&07077 != 0 {
			return fmt.Errorf("snapshot allocation name/quarantine/identity/private mode changed")
		}
		found = true
	}
	if !found {
		return os.ErrNotExist
	}
	return nil
}

func (p *Provider) snapshotCheckDestinationRoot(ctx context.Context, root *os.File, artifact model.Artifact, owners *snapshotFDSet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.snapshotCheckArtifactName(artifact); err != nil {
		return err
	}
	id, err := p.snapshotCheckDirectory(root, owners)
	if err != nil {
		return err
	}
	if id != *artifact.Identity {
		return fmt.Errorf("destination root is not the exact owned allocation")
	}
	return nil
}

// Every intermediate destination directory is checked BEFORE descent or child
// creation. No-follow alone does not fence mounts. Each current named/opened
// identity and complete relative chain is observed again before payload writes.
func (p *Provider) snapshotWalkOutputDirectories(root *os.File, path string, owners *snapshotFDSet) (*os.File, error) {
	current, err := owners.duplicate(root, "output-revalidation-root")
	if err != nil {
		return nil, err
	}
	if _, err = p.snapshotCheckDirectory(current, owners); err != nil {
		return nil, errors.Join(err, owners.close(current))
	}
	if path == "" {
		return current, nil
	}
	if clean, err := exportRelative(path); err != nil || clean != path {
		return nil, errors.Join(fmt.Errorf("invalid destination parent path"), owners.close(current))
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		next, openErr := owners.acquire("output-revalidation-directory", func() (int, error) {
			return unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		})
		if openErr == nil {
			_, openErr = p.snapshotCheckDirectory(next, owners)
		}
		if openErr == nil {
			openErr = p.snapshotCheckNamedChild(current, part, next, owners)
		}
		closeErr := owners.close(current)
		if openErr != nil || closeErr != nil {
			return nil, errors.Join(openErr, closeErr, owners.close(next))
		}
		current = next
	}
	return current, nil
}

func (p *Provider) snapshotCheckOutputParent(ctx context.Context, root *os.File, artifact model.Artifact, path string, parent *os.File, owners *snapshotFDSet) error {
	if err := p.snapshotCheckDestinationRoot(ctx, root, artifact, owners); err != nil {
		return err
	}
	expected, err := p.snapshotCheckDirectory(parent, owners)
	if err != nil {
		return err
	}
	current, err := p.snapshotWalkOutputDirectories(root, path, owners)
	if err != nil {
		return err
	}
	actual, err := p.snapshotCheckDirectory(current, owners)
	closeErr := owners.close(current)
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if actual != expected {
		return fmt.Errorf("destination parent no longer occupies its exact confined path")
	}
	return p.snapshotCheckDestinationRoot(ctx, root, artifact, owners)
}

func (p *Provider) snapshotCheckOutputFile(ctx context.Context, root *os.File, artifact model.Artifact, path string, file *os.File, size int64, owners *snapshotFDSet) error {
	if err := p.snapshotCheckNewFile(file, size, owners); err != nil {
		return err
	}
	if err := p.snapshotCheckDestinationRoot(ctx, root, artifact, owners); err != nil {
		return err
	}
	parentPath := filepath.Dir(path)
	if parentPath == "." {
		parentPath = ""
	}
	parent, err := p.snapshotWalkOutputDirectories(root, parentPath, owners)
	if err != nil {
		return err
	}
	current, openErr := owners.acquire("output-current-file", func() (int, error) {
		return unix.Openat(int(parent.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	})
	if openErr == nil {
		openErr = p.snapshotCheckNewFile(current, size, owners)
	}
	if openErr == nil {
		openErr = p.snapshotCheckNamedChild(parent, filepath.Base(path), current, owners)
	}
	if openErr == nil {
		left, right := owners.records[owners.files[file]].Identity, owners.records[owners.files[current]].Identity
		if left == nil || right == nil || *left != *right {
			openErr = fmt.Errorf("current output path is not the newly created owned file")
		}
	}
	closeErr := errors.Join(owners.close(current), owners.close(parent))
	if openErr != nil || closeErr != nil {
		return errors.Join(openErr, closeErr)
	}
	if err = p.snapshotCheckDestinationRoot(ctx, root, artifact, owners); err != nil {
		return err
	}
	return p.snapshotCheckNewFile(file, size, owners)
}

// S creation keeps Provider.Create's exclusive/no-adoption rules but routes
// EVERY actual directory/provider sync through this original owned FD roster.
// An observed inode returned with error is only creating/unknown, never Owned.
func (p *Provider) snapshotAllocate(ctx context.Context, name string, owners *snapshotFDSet) (*model.Artifact, error) {
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("invalid snapshot allocation name")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.validateReadOnlyNamed(); err != nil {
		return nil, err
	}
	if _, err := p.snapshotCheckDirectory(p.root, owners); err != nil {
		return nil, err
	}
	if err := unix.Mkdirat(int(p.root.Fd()), name, 0700); err != nil {
		return nil, err
	}
	named, err := readOnlyNamedAt(int(p.root.Fd()), name)
	if err != nil {
		return nil, err
	}
	artifact := &model.Artifact{Name: name, Path: p.Path(name), Identity: &named.Identity, Phase: "creating", Policy: "snapshot independent managed copy; full logical promise; physical/APFS reclamation unknown", Updated: time.Now().UTC()}
	if named.Identity.Owner != p.identity.Owner || named.Mode&unix.S_IFMT != unix.S_IFDIR || named.Mode&07077 != 0 {
		return artifact, fmt.Errorf("created snapshot allocation identity/private type changed")
	}
	dir, err := owners.acquire("allocation-root", func() (int, error) {
		return unix.Openat(int(p.root.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	})
	if err != nil {
		return artifact, err
	}
	if _, err = p.snapshotCheckDirectory(dir, owners); err == nil {
		err = p.snapshotCheckNamedChild(p.root, name, dir, owners)
	}
	if err == nil {
		err = owners.sync(dir)
	}
	if err == nil {
		err = owners.sync(p.root)
	}
	closeErr := owners.close(dir)
	if err != nil || closeErr != nil {
		return artifact, errors.Join(err, closeErr)
	}
	if err = p.validateReadOnlyNamed(); err != nil {
		return artifact, err
	}
	after, err := readOnlyNamedAt(int(p.root.Fd()), name)
	if err != nil || after != named {
		return artifact, errors.Join(fmt.Errorf("new allocation changed before durable ownership checkpoint"), err)
	}
	artifact.Phase = "owned"
	return artifact, nil
}
