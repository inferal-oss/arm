package mesh

import (
	"context"
	"getarm.org/internal/residentrefs"
	"golang.org/x/sys/unix"
)

// DATA from the actual captured original, not a borrowed File/FD/Close handle.
func (o *ResidentRuntimePending) IdentityOriginal(ctx context.Context) (identity residentrefs.Identity, resultErr error) {
	op, err := o.begin("validate")
	if err != nil {
		return identity, err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = o.validate(ctx)
	if resultErr == nil {
		resultErr = ctx.Err()
	}
	if resultErr == nil {
		o.phase("original_runtime_identity_native_stat_pending")
		var st unix.Stat_t
		resultErr = unix.Fstat(o.rawFD, &st)
		if resultErr == nil {
			identity = residentrefs.Identity{Device: uint64(st.Dev), Inode: uint64(st.Ino), UID: st.Uid, GID: st.Gid, Mode: uint32(st.Mode)}
		}
	}
	normal = true
	return identity, resultErr
}
