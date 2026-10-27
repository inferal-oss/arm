package providerexec

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"getarm.org/internal/model"
	"getarm.org/internal/storage"
	"golang.org/x/sys/unix"
)

const maxImageAncestors = 64

type directoryWitness struct {
	Identity  model.FileIdentity `json:"identity"`
	Mode      uint32             `json:"mode"`
	ACLSHA256 string             `json:"acl_sha256"`
}

// This is a namespace policy against other UIDs under a trusted root/OS. It is
// not an atomic executable-handle launch or protection from the selected UID.
func trustedDirectory(v directoryWitness, next *directoryWitness, uid uint32) bool {
	if v.Mode&syscall.S_IFMT != syscall.S_IFDIR || !v.Identity.BirthKnown || (v.Identity.Owner != 0 && v.Identity.Owner != uid) {
		return false
	}
	if v.Mode&0022 == 0 {
		return true
	}
	// A root-owned sticky temp directory protects its exact next privately owned
	// component from other UIDs. No sticky exception propagates into that child.
	return v.Identity.Owner == 0 && v.Mode&syscall.S_ISVTX != 0 && next != nil &&
		(next.Identity.Owner == 0 || next.Identity.Owner == uid) && next.Mode&0022 == 0 && next.Identity.BirthKnown
}
func directoryOf(f *os.File) (directoryWitness, error) {
	s, err := stamp(f)
	if err != nil {
		return directoryWitness{}, err
	}
	id, err := storage.DirectoryIdentity(int(f.Fd()))
	if err != nil {
		return directoryWitness{}, err
	}
	if s.Device != id.Device || s.Inode != id.Inode || s.UID != id.Owner {
		return directoryWitness{}, fmt.Errorf("ancestor identity changed")
	}
	acl, err := imageACL(f)
	if err != nil {
		return directoryWitness{}, err
	}
	return directoryWitness{id, s.Mode, acl}, nil
}

// Every hop is openat(O_NOFOLLOW) relative to the previous native directory.
// No EvalSymlinks/path-based hop silently adopts a different ancestor namespace.
// The final file is opened through that exact chain and checked separately.
func openSelectedImage(path string) (*os.File, []directoryWitness, error) {
	parts := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(parts) < 1 || len(parts) > maxImageAncestors {
		return nil, nil, fmt.Errorf("selected image ancestor limit is %d", maxImageAncestors)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return nil, nil, fmt.Errorf("bounded canonical image components required")
		}
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	current := os.NewFile(uintptr(fd), "selected-image-root")
	defer func() { current.Close() }()
	root, err := directoryOf(current)
	if err != nil {
		return nil, nil, err
	}
	chain := []directoryWitness{root}
	uid := uint32(os.Geteuid())
	for index, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
		if index < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		childfd, e := unix.Openat(int(current.Fd()), part, flags, 0)
		if e != nil {
			return nil, nil, fmt.Errorf("image component %q: %w", part, e)
		}
		child := os.NewFile(uintptr(childfd), "selected-image-component")
		witness, e := directoryOf(child)
		if e != nil {
			child.Close()
			return nil, nil, e
		}
		if !trustedDirectory(chain[len(chain)-1], &witness, uid) {
			child.Close()
			return nil, nil, fmt.Errorf("untrusted writable, foreign, or unidentified image ancestor")
		}
		if index == len(parts)-1 {
			return child, chain, nil
		}
		current.Close()
		current = child
		chain = append(chain, witness)
	}
	return nil, nil, fmt.Errorf("selected image leaf missing")
}
func sameAncestors(a, b []directoryWitness) bool { return slices.Equal(a, b) }
