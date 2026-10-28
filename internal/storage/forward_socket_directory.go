package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

// ForwardSocketProof observes one socket entry beneath the retained original
// source directory. It does not prove ownership of a live listener FD.
type ForwardSocketProof struct {
	Socket    model.FileIdentity
	Directory model.FileIdentity
	Allocated int64
}

func (h *SourceDirectory) forwardSocketReady(name string) error {
	if h == nil || !nativeSocketBasename.MatchString(name) || h.closing {
		return fmt.Errorf("original forward socket directory unavailable")
	}
	return h.Check()
}

func (h *SourceDirectory) forwardDirectoryBlocks() (int64, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(h.dir.Fd()), &st); err != nil {
		return 0, err
	}
	id, err := durableIdentity(int(h.dir.Fd()), "")
	if err != nil || id != h.dirID || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0077 != 0 || st.Uid != h.dirID.Owner || st.Blocks < 0 || st.Blocks > math.MaxInt64/512 {
		return 0, fmt.Errorf("original forward directory identity or allocation changed")
	}
	return st.Blocks * 512, nil
}

// proveForwardEmptyLocked surveys a fresh descriptor to the same held
// directory. The caller holds h.mu and p.mu; the fresh descriptor's close is
// part of positive proof, not merely deferred cleanup.
func (h *SourceDirectory) proveForwardEmptyLocked(ctx context.Context, name string) (proof EmptyDirectoryProof, resultErr error) {
	if err := h.forwardSocketReady(name); err != nil {
		return proof, err
	}
	before, err := h.forwardDirectoryBlocks()
	if err != nil {
		return proof, err
	}
	fd, err := unix.Openat(int(h.dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return proof, err
	}
	clone := os.NewFile(uintptr(fd), "forward-empty-survey")
	defer func() { resultErr = errors.Join(resultErr, clone.Close()) }()
	if id, err := DirectoryIdentity(fd); err != nil || id != h.dirID {
		return proof, fmt.Errorf("forward empty survey directory differs")
	}
	survey, err := h.provider.survey(ctx, clone)
	if err != nil || survey.Entries != 0 || survey.Allocated != 0 {
		return proof, fmt.Errorf("original forward directory is not empty: %w", err)
	}
	var entry unix.Stat_t
	if err = unix.Fstatat(int(h.dir.Fd()), name, &entry, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
		return proof, fmt.Errorf("original forward socket name is present or uncertain: %w", err)
	}
	after, err := h.forwardDirectoryBlocks()
	if err != nil || after != before {
		return proof, fmt.Errorf("original forward directory allocation changed during survey")
	}
	if err = h.Check(); err != nil || ctx.Err() != nil {
		return proof, errors.Join(err, ctx.Err())
	}
	return EmptyDirectoryProof{Identity: h.dirID, Allocated: after}, nil
}

func (h *SourceDirectory) ProveEmptyForwardSocket(ctx context.Context, name string) (EmptyDirectoryProof, error) {
	if h == nil || ctx == nil || ctx.Err() != nil {
		return EmptyDirectoryProof{}, fmt.Errorf("live original forward directory required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.provider.mu.Lock()
	defer h.provider.mu.Unlock()
	return h.proveForwardEmptyLocked(ctx, name)
}

func (h *SourceDirectory) forwardSocketIdentity(name string, private bool) (model.FileIdentity, int64, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(int(h.dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return model.FileIdentity{}, 0, err
	}
	id, err := durableIdentity(int(h.dir.Fd()), name)
	if err != nil || !id.BirthKnown || id.Device != h.dirID.Device || id.Owner != h.dirID.Owner || uint64(st.Dev) != id.Device || st.Ino != id.Inode || st.Uid != id.Owner || st.Mode&unix.S_IFMT != unix.S_IFSOCK || private && st.Mode&0077 != 0 || st.Blocks < 0 || st.Blocks > math.MaxInt64/512 {
		return model.FileIdentity{}, 0, fmt.Errorf("original forward socket identity, type or allocation changed")
	}
	return id, st.Blocks * 512, nil
}

// ObserveForwardSocket tightens the original fixed entry to 0600 and surveys
// the complete one-entry directory through a fresh checked-close descriptor.
func (h *SourceDirectory) ObserveForwardSocket(ctx context.Context, name string) (proof ForwardSocketProof, resultErr error) {
	if h == nil || ctx == nil || ctx.Err() != nil {
		return proof, fmt.Errorf("live original forward socket required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.provider.mu.Lock()
	defer h.provider.mu.Unlock()
	if err := h.forwardSocketReady(name); err != nil {
		return proof, err
	}
	initial, _, err := h.forwardSocketIdentity(name, false)
	if err != nil {
		return proof, err
	}
	if err = unix.Fchmodat(int(h.dir.Fd()), name, 0600, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return proof, err
	}
	current, socketBlocks, err := h.forwardSocketIdentity(name, true)
	if err != nil || current != initial {
		return proof, fmt.Errorf("original forward socket changed during permission check")
	}
	fd, err := unix.Openat(int(h.dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return proof, err
	}
	clone := os.NewFile(uintptr(fd), "forward-socket-survey")
	defer func() { resultErr = errors.Join(resultErr, clone.Close()) }()
	if id, err := DirectoryIdentity(fd); err != nil || id != h.dirID {
		return proof, fmt.Errorf("forward socket survey directory differs")
	}
	names, readErr := clone.Readdirnames(2)
	if len(names) != 1 || names[0] != name || readErr != nil && !errors.Is(readErr, io.EOF) {
		return proof, fmt.Errorf("forward socket directory has another or uncertain entry")
	}
	if more, err := clone.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) || len(more) != 0 {
		return proof, fmt.Errorf("forward socket directory survey did not reach end")
	}
	directoryBlocks, err := h.forwardDirectoryBlocks()
	if err != nil || directoryBlocks > math.MaxInt64-socketBlocks {
		return proof, fmt.Errorf("forward socket allocation unavailable")
	}
	if again, _, err := h.forwardSocketIdentity(name, true); err != nil || again != initial {
		return proof, fmt.Errorf("original forward socket changed during survey")
	}
	if err = h.Check(); err != nil || ctx.Err() != nil {
		return proof, errors.Join(err, ctx.Err())
	}
	return ForwardSocketProof{Socket: initial, Directory: h.dirID, Allocated: directoryBlocks + socketBlocks}, nil
}

// UnlinkOriginalForwardSocket removes only the exact observed entry through
// the retained directory FD. A changed or absent entry remains uncertain.
func (h *SourceDirectory) UnlinkOriginalForwardSocket(ctx context.Context, name string, expected model.FileIdentity) (EmptyDirectoryProof, error) {
	if h == nil || ctx == nil || ctx.Err() != nil {
		return EmptyDirectoryProof{}, fmt.Errorf("live original forward socket required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.provider.mu.Lock()
	defer h.provider.mu.Unlock()
	if err := h.forwardSocketReady(name); err != nil {
		return EmptyDirectoryProof{}, err
	}
	actual, _, err := h.forwardSocketIdentity(name, true)
	if err != nil || actual != expected {
		return EmptyDirectoryProof{}, fmt.Errorf("original forward socket changed before unlink")
	}
	if err = unix.Unlinkat(int(h.dir.Fd()), name, 0); err != nil {
		return EmptyDirectoryProof{}, err
	}
	if err = h.dir.Sync(); err != nil {
		return EmptyDirectoryProof{}, err
	}
	return h.proveForwardEmptyLocked(ctx, name)
}
