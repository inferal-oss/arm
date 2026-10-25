package storage

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/yrashk/arm/internal/model"
	"golang.org/x/sys/unix"
)

// ReadOnlyObservation is a fresh complete native observation of an opened
// allocation. It is data, never publication, adoption, permission or cleanup
// authority. It must be consumed in the same resolution phase; it cannot be
// persisted, accepted from a wire request or reused for a later operation.
// Its fields deliberately match sharefs.Version without a package dependency.
type ReadOnlyObservation struct {
	Identity        model.FileIdentity
	Mount           string
	Size            int64
	Mode            uint32
	Links           uint64
	ModifiedSeconds int64
	ModifiedNanos   int64
	ChangedSeconds  int64
	ChangedNanos    int64
}

// OpenArtifactReadOnlyObserved preserves the ordinary read-only open's exact
// provider/name/quarantine/lifetime checks. It additionally returns all native
// opened-root fields so a caller can immediately compare its full published
// version without repeating the opened-root stat/mount observation in that same
// phase. General storage/cleanup callers retain their existing locate path.
func (p *Provider) OpenArtifactReadOnlyObserved(ctx context.Context, a model.Artifact) (*os.File, ReadOnlyObservation, error) {
	zero := ReadOnlyObservation{}
	if err := ctx.Err(); err != nil {
		return nil, zero, err
	}
	if p == nil || p.root == nil {
		return nil, zero, fmt.Errorf("storage provider unavailable")
	}
	if a.Phase != "owned" || a.Path != p.Path(a.Name) {
		return nil, zero, fmt.Errorf("share backing must be an owned managed allocation")
	}
	// Do not cache root.Fd(): Control pins the provider descriptor against
	// concurrent Close/reuse across name lookup, open and native observation.
	raw, err := p.root.SyscallConn()
	if err != nil {
		return nil, zero, err
	}
	var name string
	var f *os.File
	var observation ReadOnlyObservation
	var locateErr error
	if err = raw.Control(func(uintptr) { name, f, observation, locateErr = p.locateReadOnlyObserved(a) }); err != nil {
		if f != nil {
			f.Close()
		}
		return nil, zero, err
	}
	if locateErr != nil {
		return nil, zero, locateErr
	}
	if name != a.Name {
		f.Close()
		return nil, zero, fmt.Errorf("quarantined storage cannot be shared")
	}
	if err = ctx.Err(); err != nil {
		f.Close()
		return nil, zero, err
	}
	return f, observation, nil
}

// The pre-open predicates/cuts below deliberately match locate. This separate
// read-only path changes no general cleanup caller or stored artifact schema.
// Neither the provider-root checks nor the named-entry identity checks are
// coalesced: that wider change is outside observed-open's scope.
func (p *Provider) locateReadOnlyObserved(a model.Artifact) (string, *os.File, ReadOnlyObservation, error) {
	zero := ReadOnlyObservation{}
	if err := p.validate(); err != nil {
		return "", nil, zero, err
	}
	if !validName.MatchString(a.Name) || a.Identity == nil {
		return "", nil, zero, fmt.Errorf("artifact has no verified identity")
	}
	var found string
	for _, name := range []string{a.Name, ".reclaim-" + a.Name} {
		var s unix.Stat_t
		err := unix.Fstatat(int(p.root.Fd()), name, &s, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return "", nil, zero, err
		}
		if found != "" {
			return "", nil, zero, fmt.Errorf("both artifact and quarantine names exist")
		}
		id, err := durableIdentity(int(p.root.Fd()), name)
		if err != nil {
			return "", nil, zero, err
		}
		if s.Mode&unix.S_IFMT != unix.S_IFDIR || id != *a.Identity {
			return "", nil, zero, fmt.Errorf("artifact directory was replaced")
		}
		found = name
	}
	if found == "" {
		return "", nil, zero, os.ErrNotExist
	}
	fd, err := unix.Openat(int(p.root.Fd()), found, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", nil, zero, err
	}
	f := os.NewFile(uintptr(fd), found)
	observation, err := readOnlyOpenedObservation(fd, *a.Identity)
	if err == nil && observation.Identity != *a.Identity {
		err = fmt.Errorf("artifact changed while opening")
	}
	if err == nil && observation.Mount != p.mount {
		err = fmt.Errorf("artifact crosses a mount boundary")
	}
	if err != nil {
		f.Close()
		return "", nil, zero, err
	}
	return found, f, observation, nil
}
