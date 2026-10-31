package residentbirth

import (
	"context"
	"getarm.org/internal/mesh"
	"getarm.org/internal/residentrefs"
	"golang.org/x/sys/unix"
	"os/exec"
)

// Data selectors before the intent append, derived only from the actual bound
// prepared fence. The actual fence remains retained; no File/FD/Close escapes.
// The final committed BootstrapOriginalData must replace the zero intent fields.
func (h *FreshFence) PreparedBindingsOriginalData(prepared *PreparedFence) (P1FenceData, mesh.ResidentImageInventory, error) {
	if h == nil || h.owner == nil {
		return P1FenceData{}, mesh.ResidentImageInventory{}, errOriginalFence
	}
	o := h.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.self != h || prepared == nil || prepared != o.prepared || prepared.owner != o || o.active != nil || o.firstError != nil || !o.bound || o.writeTried || o.writerWithdrawn {
		return P1FenceData{}, mesh.ResidentImageInventory{}, errOriginalFence
	}
	n := o.state
	if n == nil || n.home == nil || n.launch == nil || n.journal == nil || n.daemon == nil || n.directory == nil || n.ledger == nil {
		return P1FenceData{}, mesh.ResidentImageInventory{}, errOriginalFence
	}
	ids := residentrefs.FenceIdentities{Launch: fenceSharedID(n.launch.id), Intent: fenceSharedID(n.journal.id), Home: fenceSharedID(n.home.id), Daemon: fenceSharedID(n.daemon.id), Directory: fenceSharedID(n.directory.id), Ledger: fenceSharedID(n.ledger.id)}
	return P1FenceData{Home: o.path, Identities: ids}, o.image, nil
}

// Bounded final native-reference check after every long census/hash/journal I/O.
// No reopen, cursor, wrapper, journal read or claim of atomic named execution.
func (h *FreshFence) ShortSealOriginal(ctx context.Context, prepared *PreparedFence, command *exec.Cmd) (resultErr error) {
	if h == nil || h.owner == nil || h.owner.self != h || command != h.owner.command {
		return errOriginalFence
	}
	op, err := h.begin(ctx, "short-seal", prepared)
	if err != nil {
		return err
	}
	o := h.owner
	n := o.state
	normal := false
	defer o.finish(op, &normal, &resultErr)
	resultErr = func() error {
		if ctx == nil {
			return errOriginalFence
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := h.commandSelectors(true); err != nil {
			return err
		}
		if command.Stdout != nil || command.Stderr != nil {
			return errOriginalFence
		}
		for _, e := range []struct {
			f         *fenceFD
			directory bool
		}{{n.launch, false}, {n.journal, false}, {n.home, true}, {n.daemon, false}, {n.directory, true}, {n.ledger, false}} {
			if err := n.check(e.f, e.directory); err != nil {
				return err
			}
			if err := n.before("original_final_fence_access_flags"); err != nil {
				return err
			}
			flags, err := unix.FcntlInt(uintptr(e.f.number), unix.F_GETFL, 0)
			if err != nil {
				return err
			}
			want := unix.O_RDWR
			if e.directory {
				want = unix.O_RDONLY
			}
			if flags&unix.O_ACCMODE != want {
				return errOriginalFence
			}
		}
		if err := n.before("original_final_empty_ledger_stat"); err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Fstat(n.ledger.number, &st); err != nil {
			return err
		}
		if st.Size != 0 {
			return errOriginalFence
		}
		return ctx.Err()
	}()
	normal = true
	return resultErr
}
