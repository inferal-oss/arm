package residentrefs

import (
	"context"
	"encoding/hex"
	"strings"

	"golang.org/x/sys/unix"
)

type ComposedIdentities struct {
	Control Identity
	Service ServiceIdentities
	Fence   FenceIdentities
	Image   ImageIdentities
}

// Revalidates SAME originals and their fixed relative names, never adopts an
// arbitrary FD, path, private registry or caller success flag. Canonical pathname
// and actual daemon lock authority must ALSO match the real native issuer.
func (c *ChildPending) ValidateComposedOriginal(ctx context.Context, debitID string) (value ComposedIdentities, resultErr error) {
	if c == nil || c.state == nil {
		return value, ErrOriginal
	}
	if len(debitID) != 64 || strings.ToLower(debitID) != debitID {
		return value, ErrOriginal
	}
	if _, err := hex.DecodeString(debitID); err != nil {
		return value, ErrOriginal
	}
	op, err := c.begin(ctx, "composed", nil, nil)
	if err != nil {
		return value, err
	}
	s := c.state
	normal := false
	defer s.finish(op, &normal, &resultErr)
	resultErr = func() error {
		for _, entry := range s.entries {
			s.mu.Lock()
			op.entry = entry
			s.mu.Unlock()
			if err := s.before(op, "original_composed_all13_native_stat"); err != nil {
				return err
			}
			var st unix.Stat_t
			if err := unix.Fstat(entry.number, &st); err != nil {
				return err
			}
			if (Identity{uint64(st.Dev), uint64(st.Ino), st.Uid, st.Gid, uint32(st.Mode)}) != entry.identity {
				return ErrOriginal
			}
			if err := validateFixedKind(entry.number, &st); err != nil {
				return err
			}
			if err := s.before(op, "original_composed_all13_CLOEXEC"); err != nil {
				return err
			}
			flags, err := unix.FcntlInt(uintptr(entry.number), unix.F_GETFD, 0)
			if err != nil {
				return err
			}
			if flags&unix.FD_CLOEXEC == 0 {
				return ErrOriginal
			}
		}
		// Exact same native home, independently inherited original references.
		if s.entries[1].identity != s.entries[9].identity {
			return ErrOriginal
		}
		for _, link := range []struct {
			parent, target int
			name           string
		}{
			{9, 2, "resident-service.lock"}, {9, 3, "resident-service.journal"}, {9, 4, "resident-launch.lock"}, {9, 5, "resident-birth.json"},
			{9, 7, "resident-images"}, {9, 10, "daemon.lock"}, {9, 11, "state.db"}, {11, 12, "ledger.sqlite"},
			{7, 8, "pin-" + debitID}, {8, 6, "arm"},
		} {
			parent, target := s.entries[link.parent], s.entries[link.target]
			s.mu.Lock()
			op.entry = parent
			s.mu.Unlock()
			if err := s.before(op, "original_composed_current_relative_name"); err != nil {
				return err
			}
			var st unix.Stat_t
			if err := unix.Fstatat(parent.number, link.name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			if (Identity{uint64(st.Dev), uint64(st.Ino), st.Uid, st.Gid, uint32(st.Mode)}) != target.identity || st.Nlink == 0 || uint32(st.Mode)&unix.S_IFMT == unix.S_IFREG && st.Nlink != 1 {
				return ErrOriginal
			}
		}
		value = ComposedIdentities{Control: s.entries[0].identity,
			Service: ServiceIdentities{s.entries[1].identity, s.entries[2].identity, s.entries[3].identity},
			Fence:   FenceIdentities{s.entries[4].identity, s.entries[5].identity, s.entries[9].identity, s.entries[10].identity, s.entries[11].identity, s.entries[12].identity},
			Image:   ImageIdentities{s.entries[6].identity, s.entries[7].identity, s.entries[8].identity}}
		return nil
	}()
	normal = true
	return value, resultErr
}
func (r *FenceOriginal) ValidateEmptyLedgerOriginal(ctx context.Context) error {
	if r == nil || r.self != r || r.state == nil {
		return ErrOriginal
	}
	info, err := r.state.stat(ctx, r.state.entries[5])
	if err != nil {
		return err
	}
	if info.Size() != 0 {
		return ErrOriginal
	}
	return nil
}
