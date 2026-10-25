package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

const snapshotFDHistoryLimit = 32768

// Descriptor numbers are deliberately not serialized. Sequence identifies this
// original acquisition/close receipt, never authority to reopen/reclose a saved
// integer. Unknown closes consume their original slot until reconciliation.
type SnapshotFDReceipt struct {
	Sequence   int                 `json:"sequence"`
	Role       string              `json:"role"`
	Identity   *model.FileIdentity `json:"identity,omitempty"`
	State      string              `json:"state"`
	Sync       string              `json:"sync"`
	Error      string              `json:"error,omitempty"`
	SyncError  string              `json:"sync_error,omitempty"`
	CloseError string              `json:"close_error,omitempty"`
}

type SnapshotNativeReceipt struct {
	Descriptors []SnapshotFDReceipt `json:"descriptors"`
	HighWater   int                 `json:"high_water"`
	Closed      bool                `json:"original_descriptors_closed"`
	Unknown     bool                `json:"unknown_close"`
}

type snapshotFDSet struct {
	records     []SnapshotFDReceipt
	files       map[*os.File]int
	closeErrors map[*os.File]error
	syncErrors  map[*os.File]error
	owned       int
	high        int
	// Tests may explicitly report a close failure AFTER the real File.Close.
	// Such a control is not native failure evidence and cannot authorize retry.
	closeFile func(*os.File) error
	syncFile  func(*os.File) error
}

func newSnapshotFDSet() *snapshotFDSet {
	return &snapshotFDSet{files: make(map[*os.File]int), closeErrors: make(map[*os.File]error), syncErrors: make(map[*os.File]error), closeFile: func(f *os.File) error { return f.Close() }, syncFile: func(f *os.File) error { return f.Sync() }}
}

func (s *snapshotFDSet) acquire(role string, open func() (int, error)) (*os.File, error) {
	if s.owned >= 8 || len(s.records) >= snapshotFDHistoryLimit {
		return nil, fmt.Errorf("snapshot owned FD/history budget exhausted")
	}
	i := len(s.records)
	s.records = append(s.records, SnapshotFDReceipt{Sequence: i + 1, Role: role, State: "opening", Sync: "not-requested"})
	s.owned++
	s.high = max(s.high, s.owned)
	fd, err := open()
	if err == nil && fd < 0 {
		err = fmt.Errorf("native open returned no owned descriptor")
	}
	if err != nil {
		s.records[i].State = "not-opened"
		s.records[i].Error = err.Error()
		s.owned--
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "snapshot-"+role)
	s.files[f] = i
	s.records[i].State = "open"
	// Missing native identity refuses the caller's operation; it never erases
	// the exact original close obligation acquired by this successful open.
	id, err := durableIdentity(fd, "")
	if err != nil {
		return nil, errors.Join(err, s.close(f))
	}
	s.records[i].Identity = &id
	return f, nil
}

func (s *snapshotFDSet) close(f *os.File) error {
	if f == nil {
		return nil
	}
	i, known := s.files[f]
	if !known {
		return fmt.Errorf("unregistered snapshot descriptor")
	}
	r := &s.records[i]
	if r.State == "closed" {
		return nil
	}
	if r.State != "open" {
		if first := s.closeErrors[f]; first != nil {
			return first
		}
		return errors.Join(errSnapshotClose, fmt.Errorf("original snapshot close remains %s", r.State))
	}
	// Set before entering Close: even a panic/cancellation must not permit a
	// second raw close on an integer the OS might already have recycled.
	r.State = "closing"
	err := s.closeFile(f)
	if err != nil {
		r.State = "unknown"
		r.CloseError = err.Error()
		s.closeErrors[f] = errors.Join(errSnapshotClose, err)
		return s.closeErrors[f]
	}
	r.State = "closed"
	s.owned--
	return nil
}

func (s *snapshotFDSet) sync(f *os.File) error {
	i, known := s.files[f]
	if !known || s.records[i].State != "open" {
		return fmt.Errorf("sync requires original owned open descriptor")
	}
	if s.records[i].Sync == "unknown" || s.records[i].Sync == "syncing" {
		if first := s.syncErrors[f]; first != nil {
			return first
		}
		return fmt.Errorf("original snapshot sync is unresolved")
	}
	s.records[i].Sync = "syncing"
	err := s.syncFile(f)
	if err != nil {
		s.records[i].Sync = "unknown"
		s.records[i].SyncError = err.Error()
		s.syncErrors[f] = err
	} else {
		s.records[i].Sync = "synced"
	}
	return err
}

func (s *snapshotFDSet) closeAll() error {
	var result error
	// Map iteration never supplies ordering authority. Each exact still-open
	// object is attempted once; closed/unknown entries are never reclosed.
	for f, i := range s.files {
		if s.records[i].State == "open" {
			result = errors.Join(result, s.close(f))
		}
		if s.records[i].State == "unknown" || s.records[i].State == "closing" {
			if first := s.closeErrors[f]; first != nil {
				result = errors.Join(result, first)
			} else {
				result = errors.Join(result, errSnapshotClose)
			}
		}
	}
	return result
}

func (s *snapshotFDSet) receipt() SnapshotNativeReceipt {
	r := SnapshotNativeReceipt{Descriptors: append([]SnapshotFDReceipt(nil), s.records...), HighWater: s.high, Closed: true}
	for _, descriptor := range r.Descriptors {
		if descriptor.State != "closed" && descriptor.State != "not-opened" {
			r.Closed = false
			r.Unknown = true
		}
	}
	return r
}

func (r SnapshotNativeReceipt) validateClosed() error {
	if !r.Closed || r.Unknown || r.HighWater < 1 || r.HighWater > 8 || len(r.Descriptors) < 1 || len(r.Descriptors) > snapshotFDHistoryLimit {
		return fmt.Errorf("complete bounded original descriptor closure required")
	}
	for i, d := range r.Descriptors {
		if d.Sequence != i+1 || len(d.Role) < 1 || len(d.Role) > 64 || d.State != "closed" && d.State != "not-opened" || d.State == "closed" && (d.Identity == nil || !d.Identity.BirthKnown) || d.Sync != "not-requested" && d.Sync != "synced" {
			return fmt.Errorf("original descriptor receipt incomplete/unknown")
		}
	}
	return nil
}

func (s *snapshotFDSet) duplicate(root *os.File, role string) (*os.File, error) {
	return s.acquire(role, func() (int, error) {
		raw, err := root.SyscallConn()
		if err != nil {
			return -1, err
		}
		fd := -1
		var openErr error
		err = raw.Control(func(original uintptr) { fd, openErr = unix.FcntlInt(original, unix.F_DUPFD_CLOEXEC, 0) })
		// Control either invokes the callback on a pinned FD or returns before
		// it; a successfully created duplicate remains explicitly owned here.
		if fd >= 0 {
			return fd, nil
		}
		return -1, errors.Join(err, openErr)
	})
}

func (s *snapshotFDSet) openFile(root *os.File, path, role string) (*os.File, error) {
	if clean, err := exportRelative(path); err != nil || clean != path {
		return nil, fmt.Errorf("invalid confined snapshot file path")
	}
	current, err := s.duplicate(root, "walk-root")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(path, string(filepath.Separator))
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		nextRole := "walk-directory"
		if i == len(parts)-1 {
			nextRole = role
		}
		next, openErr := s.acquire(nextRole, func() (int, error) { return unix.Openat(int(current.Fd()), part, flags, 0) })
		closeErr := s.close(current)
		if openErr != nil || closeErr != nil {
			return nil, errors.Join(openErr, closeErr, s.close(next))
		}
		current = next
	}
	return current, nil
}

func (s *snapshotFDSet) createOutput(ctx context.Context, p *Provider, root *os.File, artifact model.Artifact, path string) (*os.File, error) {
	if clean, err := exportRelative(path); err != nil || clean != path {
		return nil, fmt.Errorf("invalid confined snapshot output path")
	}
	current, err := s.duplicate(root, "output-walk-root")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(path, string(filepath.Separator))
	parentPath := ""
	for _, part := range parts[:len(parts)-1] {
		if err = p.snapshotCheckOutputParent(ctx, root, artifact, parentPath, current, s); err != nil {
			return nil, errors.Join(err, s.close(current))
		}
		if err = unix.Mkdirat(int(current.Fd()), part, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, errors.Join(err, s.close(current))
		}
		next, openErr := s.acquire("output-directory", func() (int, error) {
			return unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		})
		var syncErr error
		if openErr == nil {
			_, openErr = p.snapshotCheckDirectory(next, s)
			if openErr == nil {
				openErr = p.snapshotCheckNamedChild(current, part, next, s)
			}
			if openErr == nil {
				syncErr = s.sync(current)
			}
		}
		closeErr := s.close(current)
		if openErr != nil || syncErr != nil || closeErr != nil {
			return nil, errors.Join(openErr, syncErr, closeErr, s.close(next))
		}
		current = next
		parentPath = filepath.Join(parentPath, part)
	}
	if err = p.snapshotCheckOutputParent(ctx, root, artifact, parentPath, current, s); err != nil {
		return nil, errors.Join(err, s.close(current))
	}
	out, openErr := s.acquire("destination-file", func() (int, error) {
		return unix.Openat(int(current.Fd()), parts[len(parts)-1], unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	})
	var syncErr error
	if openErr == nil {
		openErr = p.snapshotCheckNewFile(out, 0, s)
		if openErr == nil {
			openErr = p.snapshotCheckNamedChild(current, parts[len(parts)-1], out, s)
		}
		if openErr == nil {
			syncErr = s.sync(current)
		}
	}
	closeErr := s.close(current)
	if openErr != nil || syncErr != nil || closeErr != nil {
		return nil, errors.Join(openErr, syncErr, closeErr, s.close(out))
	}
	return out, nil
}

// Same existing named/quarantine/native predicates as read-only managed opens,
// with every acquired descriptor and error-path Close retained in this scope.
func (p *Provider) snapshotOpenArtifact(ctx context.Context, a model.Artifact, owners *snapshotFDSet, role string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.Identity == nil || a.Phase != "owned" || !validName.MatchString(a.Name) || a.Path != p.Path(a.Name) {
		return nil, fmt.Errorf("snapshot requires exact owned managed allocation")
	}
	if err := p.validateReadOnlyNamed(); err != nil {
		return nil, err
	}
	found := false
	for _, name := range []string{a.Name, ".reclaim-" + a.Name} {
		observed, err := readOnlyNamedAt(int(p.root.Fd()), name)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if found || name != a.Name || observed.Mode&unix.S_IFMT != unix.S_IFDIR || observed.Identity != *a.Identity || observed.Mode&0077 != 0 {
			return nil, fmt.Errorf("snapshot allocation name/quarantine/identity/private mode changed")
		}
		found = true
	}
	if !found {
		return nil, os.ErrNotExist
	}
	f, err := owners.acquire(role, func() (int, error) {
		return unix.Openat(int(p.root.Fd()), a.Name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	})
	if err != nil {
		return nil, err
	}
	observation, err := readOnlyOpenedObservation(int(f.Fd()), *a.Identity)
	if err != nil || observation.Mount != p.mount || observation.Mode&unix.S_IFMT != unix.S_IFDIR || observation.Mode&0077 != 0 {
		return nil, errors.Join(fmt.Errorf("snapshot opened allocation differs"), err, owners.close(f))
	}
	if err = ctx.Err(); err != nil {
		return nil, errors.Join(err, owners.close(f))
	}
	return f, nil
}
