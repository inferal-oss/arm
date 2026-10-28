package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

var nativeSocketBasename = regexp.MustCompile(`^forward-[a-f0-9]{32}\.sock$`)

// EmptyDirectoryProof is a read-only observation of the original held
// managed directory. Allocated includes the directory's own filesystem
// blocks, which the generic entry survey does not count.
type EmptyDirectoryProof struct {
	Identity  model.FileIdentity
	Allocated int64
}

// ProveEmptyDirectory checks the original directory through Provider's held
// root, surveys every entry and checks the designated socket name absent.
// This does not prove the absence of a listener FD from a different phase;
// only a durable pre-bind phase may use it to discharge no-bind debt.
func (p *Provider) ProveEmptyDirectory(ctx context.Context, a model.Artifact, socketName string) (EmptyDirectoryProof, error) {
	if ctx == nil || ctx.Err() != nil || !nativeSocketBasename.MatchString(socketName) {
		return EmptyDirectoryProof{}, fmt.Errorf("exact original socket name and live context required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	name, f, err := p.locate(a)
	if err != nil {
		return EmptyDirectoryProof{}, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()
	if name != a.Name || a.Identity == nil {
		return EmptyDirectoryProof{}, fmt.Errorf("original managed directory moved to quarantine")
	}
	var before unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &before); err != nil {
		return EmptyDirectoryProof{}, err
	}
	if before.Mode&unix.S_IFMT != unix.S_IFDIR || before.Mode&0077 != 0 || before.Uid != p.identity.Owner || uint64(before.Dev) != p.identity.Device || before.Blocks < 0 || before.Blocks > math.MaxInt64/512 {
		return EmptyDirectoryProof{}, fmt.Errorf("original directory ownership or allocation changed")
	}
	survey, err := p.survey(ctx, f)
	if err != nil {
		return EmptyDirectoryProof{}, err
	}
	if survey.Entries != 0 || survey.Allocated != 0 {
		return EmptyDirectoryProof{}, fmt.Errorf("original directory is not empty")
	}
	var entry unix.Stat_t
	err = unix.Fstatat(int(f.Fd()), socketName, &entry, unix.AT_SYMLINK_NOFOLLOW)
	if !errors.Is(err, unix.ENOENT) {
		if err == nil {
			return EmptyDirectoryProof{}, fmt.Errorf("socket name already exists")
		}
		return EmptyDirectoryProof{}, err
	}
	var after unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &after); err != nil {
		return EmptyDirectoryProof{}, err
	}
	identity, err := durableIdentity(int(f.Fd()), "")
	if err != nil || identity != *a.Identity || after.Mode&unix.S_IFMT != unix.S_IFDIR || after.Mode&0077 != 0 || after.Uid != before.Uid || after.Dev != before.Dev || after.Ino != before.Ino || after.Blocks < 0 || after.Blocks > math.MaxInt64/512 || after.Blocks != before.Blocks {
		return EmptyDirectoryProof{}, fmt.Errorf("original directory changed during empty survey")
	}
	if err = ctx.Err(); err != nil {
		return EmptyDirectoryProof{}, err
	}
	if err = f.Close(); err != nil {
		return EmptyDirectoryProof{}, err
	}
	closed = true
	return EmptyDirectoryProof{Identity: identity, Allocated: after.Blocks * 512}, nil
}
