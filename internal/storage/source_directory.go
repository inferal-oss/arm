package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

// SourceDirectory is original directory custody, not resource admission. Only
// the native Manager's registered original pair may publish a source factory.
// It has no constructor from a caller's FD or directory identity snapshot.
type SourceDirectory struct {
	provider       *Provider
	artifact       model.Artifact
	dir, state     *os.File
	dirID, stateID model.FileIdentity
	mount          string
	mu             sync.Mutex
	closing        bool
	done           chan struct{}
	closeErr       error
}

func (p *Provider) HoldSourceDirectory(a model.Artifact) (result *SourceDirectory, resultErr error) {
	if a.Phase != "owned" || a.Identity == nil {
		return nil, fmt.Errorf("owned source artifact required")
	}
	if !validName.MatchString(a.Name) {
		return nil, fmt.Errorf("invalid source artifact name")
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(p.root.Fd()), a.Name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	h := &SourceDirectory{provider: p, artifact: a, dir: os.NewFile(uintptr(fd), a.Name), dirID: *a.Identity, done: make(chan struct{})}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, h.Close())
		}
	}()
	fd, err = unix.Open(filepath.Dir(p.path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	h.state = os.NewFile(uintptr(fd), "source-state-root")
	h.stateID, err = DirectoryIdentity(fd)
	if err != nil {
		return nil, err
	}
	h.mount, err = sourceMountFD(fd)
	if err != nil {
		return nil, err
	}
	if err = h.Check(); err != nil {
		return nil, err
	}
	return h, nil
}
func sourcePrivateDir(f *os.File, id model.FileIdentity) error {
	if f == nil || !id.BirthKnown {
		return fmt.Errorf("original known directory identity required")
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return err
	}
	got, err := DirectoryIdentity(int(f.Fd()))
	if err != nil {
		return err
	}
	if got != id || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0077 != 0 || st.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("source directory identity, mode or owner changed")
	}
	return nil
}
func (h *SourceDirectory) Check() error {
	if h == nil || h.provider == nil || h.dir == nil || h.state == nil {
		return fmt.Errorf("original source directory unavailable")
	}
	if err := sourcePrivateDir(h.dir, h.dirID); err != nil {
		return err
	}
	if err := sourcePrivateDir(h.state, h.stateID); err != nil {
		return err
	}
	if err := h.provider.validate(); err != nil {
		return err
	}
	named, err := durableIdentity(int(h.provider.root.Fd()), h.artifact.Name)
	if err != nil || named != h.dirID {
		return fmt.Errorf("named source directory differs")
	}
	state, err := durableIdentity(unix.AT_FDCWD, filepath.Dir(h.provider.path))
	if err != nil || state != h.stateID {
		return fmt.Errorf("named state directory differs")
	}
	for _, f := range []*os.File{h.state, h.provider.root, h.dir} {
		mount, err := sourceMountFD(int(f.Fd()))
		if err != nil || mount != h.mount {
			return fmt.Errorf("source directory crosses native mount identity")
		}
		var st unix.Stat_t
		if err = unix.Fstat(int(f.Fd()), &st); err != nil || uint64(st.Dev) != h.stateID.Device {
			return fmt.Errorf("source directory crosses state filesystem")
		}
	}
	return nil
}

var sourceNonce = regexp.MustCompile(`^[a-f0-9]{32}$`)

func SourceFileName(purpose, nonce string) (string, error) {
	valid := false
	for _, p := range []string{"payload", "ship", "lookup_binary", "catalog_proof", "lookup_start_marker", "runtime_proof"} {
		if p == purpose {
			valid = true
		}
	}
	if !valid || !sourceNonce.MatchString(nonce) {
		return "", fmt.Errorf("invalid fixed source file purpose")
	}
	return purpose + "-" + nonce, nil
}

// OpenExclusive performs only the open. The receiver must register the returned
// original FD immediately, before any further fallible validation or unlink.
func (h *SourceDirectory) OpenExclusive(purpose, nonce string) (*os.File, error) {
	name, err := SourceFileName(purpose, nonce)
	if err != nil {
		return nil, err
	}
	if err = h.Check(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(h.dir.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
func (h *SourceDirectory) CheckFile(f *os.File, name string, linked bool) error {
	if err := h.Check(); err != nil {
		return err
	}
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return err
	}
	id, err := DirectoryIdentity(int(f.Fd()))
	if err != nil || !id.BirthKnown {
		return fmt.Errorf("source file creation identity unavailable")
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0077 != 0 || st.Uid != uint32(os.Geteuid()) || uint64(st.Dev) != h.stateID.Device {
		return fmt.Errorf("source file type, owner, mode or filesystem differs")
	}
	mount, err := sourceMountFD(int(f.Fd()))
	if err != nil || mount != h.mount {
		return fmt.Errorf("source file mount differs")
	}
	if linked {
		named, err := durableIdentity(int(h.dir.Fd()), name)
		if err != nil || named != id || st.Nlink != 1 {
			return fmt.Errorf("original linked source file differs")
		}
	} else if st.Nlink != 0 {
		return fmt.Errorf("source file is not unlinked")
	}
	return nil
}
func (h *SourceDirectory) UnlinkOriginal(f *os.File, name string) error {
	if err := h.CheckFile(f, name, true); err != nil {
		return err
	}
	return unix.Unlinkat(int(h.dir.Fd()), name, 0)
}
func (h *SourceDirectory) Close() error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	if h.closing {
		done := h.done
		h.mu.Unlock()
		if done != nil {
			<-done
		}
		h.mu.Lock()
		err := h.closeErr
		h.mu.Unlock()
		return err
	}
	h.closing = true
	if h.done == nil {
		h.done = make(chan struct{})
	}
	dir, state := h.dir, h.state
	h.mu.Unlock()
	var err error
	if dir != nil {
		err = errors.Join(err, dir.Close())
	}
	if state != nil {
		err = errors.Join(err, state.Close())
	}
	h.mu.Lock()
	h.closeErr = err
	close(h.done)
	h.mu.Unlock()
	return err
}
