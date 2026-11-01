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
func (o *WorkerResidentOpen) matchOriginalPreparedFiles(ctx context.Context, snapshot lifetimeview.WorkerSnapshot) error {
	if o == nil || o.self != o || ctx == nil {
		return errWorkerResidentDatabaseOriginal
	}
	if snapshot.Fence.Home.Inode == 0 || snapshot.Fence.Directory.Inode == 0 || snapshot.Fence.Ledger.Inode == 0 || snapshot.Birth.HomeID.Inode == 0 {
		return errWorkerResidentDatabaseOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := o.files.validateCanonical(); err != nil {
		return err
	}
	for _, entry := range []struct {
		file     *workerResidentSQLFD
		expected residentrefs.Identity
	}{
		{o.files.directory, snapshot.Fence.Directory}, {o.files.ledger, snapshot.Fence.Ledger},
	} {
		if entry.file == nil || entry.file.file == nil || entry.file.closeTried {
			return errWorkerResidentDatabaseOriginal
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
			return errWorkerResidentDatabaseOriginal
		}
	}
	return ctx.Err()
}
