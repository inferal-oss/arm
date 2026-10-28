package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

// OpenOnFilesystem opens a named provider on a freshly observed native
// FilesystemID (not a volume UUID, shared-backing key or reclaimable-byte claim).
// It holds a no-follow parent FD, checks that FD before mkdir, and performs root
// creation/open relative to it. Open and its existing callers remain unchanged.
// Failures never remove/adopt names; an uncertain created root remains charged
// by the caller's original obligation and must not be retried as an adoption.
func OpenOnFilesystem(path string, expected *model.FileIdentity, filesystemID string) (*Provider, error) {
	if filesystemID == "" || len(filesystemID) > 128 || !strings.HasPrefix(filesystemID, "fsid:") || strings.ContainsAny(filesystemID, "\x00\r\n") {
		return nil, fmt.Errorf("fresh native filesystem identity required")
	}
	parent, err := openProviderParent(path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openProviderAtHeldParent(parent, path, expected, filesystemID)
}
func openProviderParent(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || len(path) > 4096 || strings.ContainsRune(path, 0) || len(filepath.Base(path)) > 255 {
		return nil, fmt.Errorf("bounded canonical provider path required")
	}
	parent := filepath.Dir(path)
	parts := strings.Split(strings.TrimPrefix(parent, "/"), "/")
	if len(parts) > 128 {
		return nil, fmt.Errorf("provider parent exceeds component bound")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), parent), nil
}
func providerFilesystemIdentity(fd int) (string, error) {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		return "", err
	}
	return fmt.Sprintf("fsid:%x", fs.Fsid.Val), nil
}
func providerParentIdentity(parent *os.File, filesystemID string) (model.FileIdentity, error) {
	var st unix.Stat_t
	if parent == nil {
		return model.FileIdentity{}, fmt.Errorf("held provider parent required")
	}
	fd := int(parent.Fd())
	if err := unix.Fstat(fd, &st); err != nil {
		return model.FileIdentity{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return model.FileIdentity{}, fmt.Errorf("provider parent is not a directory")
	}
	got, err := providerFilesystemIdentity(fd)
	if err != nil {
		return model.FileIdentity{}, err
	}
	if got != filesystemID {
		return model.FileIdentity{}, fmt.Errorf("provider parent differs from observed filesystem")
	}
	// The FD keeps this inode alive. Parent need not be private or ARM-owned:
	// configured mounts may be shared, while the provider root must be private.
	return Identity(&st), nil
}
func privateProviderIdentity(fd int, name string) (model.FileIdentity, error) {
	var st unix.Stat_t
	var err error
	if name == "" {
		err = unix.Fstat(fd, &st)
	} else {
		err = unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	}
	if err != nil {
		return model.FileIdentity{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
		return model.FileIdentity{}, fmt.Errorf("provider root must be a private directory owned by ARM's user")
	}
	id, err := durableIdentity(fd, name)
	if err != nil {
		return model.FileIdentity{}, err
	}
	if id.Device != uint64(st.Dev) || id.Inode != st.Ino || id.Owner != st.Uid {
		return model.FileIdentity{}, fmt.Errorf("provider root changed during identity observation")
	}
	if !id.BirthKnown || id.Inode == 0 || id.Owner != uint32(os.Geteuid()) {
		return model.FileIdentity{}, fmt.Errorf("original provider birth identity unavailable")
	}
	return id, nil
}
func openProviderAtHeldParent(parent *os.File, path string, expected *model.FileIdentity, filesystemID string) (result *Provider, resultErr error) {
	parentID, err := providerParentIdentity(parent, filesystemID)
	if err != nil {
		return nil, err
	} // No mkdir may precede this descriptor check.
	name := filepath.Base(path)
	if name == "." || name == ".." || name == "/" || len(name) > 255 || strings.ContainsRune(name, 0) {
		return nil, fmt.Errorf("invalid provider root name")
	}
	fd := int(parent.Fd())
	if expected == nil {
		if err = unix.Mkdirat(fd, name, 0700); err != nil {
			return nil, err
		} // EEXIST is never adoption.
	}
	named, err := privateProviderIdentity(fd, name)
	if err != nil {
		return nil, err
	}
	if expected != nil && named != *expected {
		return nil, fmt.Errorf("managed root identity changed")
	}
	child, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	root := os.NewFile(uintptr(child), path)
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, root.Close())
		}
	}()
	opened, err := privateProviderIdentity(child, "")
	if err != nil {
		return nil, err
	}
	if opened != named {
		return nil, fmt.Errorf("managed root changed while opening")
	}
	actualFS, err := providerFilesystemIdentity(child)
	if err != nil {
		return nil, err
	}
	if actualFS != filesystemID {
		return nil, fmt.Errorf("provider root crossed observed filesystem")
	}
	mount, err := mountIdentity(child)
	if err != nil {
		return nil, err
	}
	p := &Provider{path: path, root: root, identity: opened, mount: mount}
	if expected == nil {
		if err = root.Sync(); err != nil {
			return nil, err
		}
		if err = parent.Sync(); err != nil {
			return nil, err
		}
	}
	// Publication checks are additional to FD-relative creation, not its safety
	// basis. Renaming/replacing a parent cannot redirect the mkdir above.
	current, err := openProviderParent(path)
	if err != nil {
		return nil, err
	}
	currentID, checkErr := providerParentIdentity(current, filesystemID)
	closeErr := current.Close()
	if checkErr != nil {
		return nil, checkErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if currentID != parentID {
		return nil, fmt.Errorf("provider parent changed before publication")
	}
	final, err := privateProviderIdentity(fd, name)
	if err != nil {
		return nil, err
	}
	if final != opened {
		return nil, fmt.Errorf("provider name changed before publication")
	}
	if err = p.validate(); err != nil {
		return nil, err
	}
	return p, nil
}
