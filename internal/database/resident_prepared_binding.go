package database

import (
	"context"

	"getarm.org/internal/residentrefs"
	"getarm.org/internal/server/lifetimeview"
	"golang.org/x/sys/unix"
)

// Actual original claim + actual original SQL registry, no constructor from
// the snapshot and no caller success callback. Native issuer derives these
// selectors from its retained inherited fence. We compare our OWN fresh current
// native state/ledger objects before physical Connect; official VFS internals
// remain opaque and no atomic pathname/VFS-inode/closure claim is invented.
func (o *ResidentOpen) matchOriginalPreparedFiles(ctx context.Context, snapshot lifetimeview.Snapshot) error {
	if o == nil || o.self != o || ctx == nil {
		return errResidentDatabaseOriginal
	}
	if !snapshot.InheritedFence {
		return nil
	} // existing original legacy/fixture path
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.files.validateCanonical(); err != nil {
		return err
	}
	for _, entry := range []struct {
		file     *residentSQLFD
		expected residentrefs.Identity
	}{
		{o.files.directory, snapshot.PreparedState}, {o.files.ledger, snapshot.PreparedLedger},
	} {
		if entry.file == nil || entry.file.file == nil || entry.file.closeTried {
			return errResidentDatabaseOriginal
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		o.publishFileProgress("actual_original_prepared_SQL_reference_match")
		var st unix.Stat_t
		if err := unix.Fstat(int(entry.file.file.Fd()), &st); err != nil {
			return err
		}
		actual := residentrefs.Identity{Device: uint64(st.Dev), Inode: uint64(st.Ino), UID: st.Uid, GID: st.Gid, Mode: uint32(st.Mode)}
		if actual != entry.expected || st.Nlink == 0 || uint32(st.Mode)&unix.S_IFMT == unix.S_IFREG && st.Nlink != 1 {
			return errResidentDatabaseOriginal
		}
	}
	return ctx.Err()
}
