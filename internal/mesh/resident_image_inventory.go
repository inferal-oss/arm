package mesh

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
)

// DATA census only. The caller must retain the ACTUAL original pin. It does not
// lend File/Close, certify execution, or replace final original image validation.
type ResidentImageInventoryID struct {
	Device, Inode  uint64
	UID, GID, Mode uint32
}
type ResidentImageInventory struct {
	Home, DebitID, Path, SHA256            string
	Size                                   int64
	HomeID, ImagesID, DirectoryID, ImageID ResidentImageInventoryID
}

// One actual native census under the original pin operation registry. A copied,
// zero or unprepared holder cannot manufacture this from an external DTO.
func (p *ResidentImagePinPending) InventoryOriginal(ctx context.Context) (inventory ResidentImageInventory, resultErr error) {
	if p == nil || p.residentImagePinState == nil || p.self != p {
		return inventory, errResidentPinOriginal
	}
	p.mu.Lock()
	if !p.ready || p.active != nil || p.firstError != nil || p.p1InventoryTried {
		p.mu.Unlock()
		return inventory, errResidentPinOriginal
	}
	p.p1InventoryTried = true
	op := &residentPinOperation{phase: "original_inventory_registered_before_effect"}
	p.active = op
	p.mu.Unlock()
	normal := false
	defer p.finish(op, &normal, &resultErr)
	resultErr = p.inventoryBody(ctx, &inventory)
	normal = true
	return inventory, resultErr
}
func (p *ResidentImagePinPending) inventoryBody(ctx context.Context, out *ResidentImageInventory) error {
	if ctx == nil {
		return errResidentPinOriginal
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.writable == nil || !p.writable.closed || p.writable.closeError != nil || p.source == nil || p.source.residentRuntimeState == nil {
		return errResidentPinOriginal
	}
	p.source.mu.Lock()
	closed := p.source.pinSourceClosed && p.source.pinSourceCloseTried && p.source.pinSourceCloseError == nil
	p.source.mu.Unlock()
	if !closed {
		return errResidentPinOriginal
	}
	out.Home, out.DebitID, out.Path, out.SHA256, out.Size = p.home, p.debit, p.path, p.expectedSHA, p.expectedSize
	for _, entry := range []struct {
		file      *residentPinFile
		id        *ResidentImageInventoryID
		directory bool
	}{
		{p.homeFile, &out.HomeID, true}, {p.images, &out.ImagesID, true}, {p.directory, &out.DirectoryID, true}, {p.readonly, &out.ImageID, false},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.file == nil || entry.file.file == nil || entry.file.closed || entry.file.closeTried {
			return errResidentPinOriginal
		}
		p.phase("original_inventory_native_stat")
		var st unix.Stat_t
		if err := unix.Fstat(entry.file.fd, &st); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.file.file.Stat()
		if err != nil {
			return err
		}
		mode := uint32(unix.S_IFREG) | 0500
		if entry.directory {
			mode = uint32(unix.S_IFDIR) | 0700
		}
		if uint32(st.Mode) != mode || st.Uid != uint32(os.Geteuid()) || st.Nlink == 0 || !entry.directory && (st.Nlink != 1 || st.Size != p.expectedSize) || !os.SameFile(entry.file.before, info) {
			return errResidentPinOriginal
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		flags, err := unix.FcntlInt(uintptr(entry.file.fd), unix.F_GETFD, 0)
		if err != nil {
			return err
		}
		if flags&unix.FD_CLOEXEC == 0 {
			return errResidentPinOriginal
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		flags, err = unix.FcntlInt(uintptr(entry.file.fd), unix.F_GETFL, 0)
		if err != nil {
			return err
		}
		if flags&unix.O_ACCMODE != unix.O_RDONLY {
			return errResidentPinOriginal
		}
		*entry.id = ResidentImageInventoryID{uint64(st.Dev), uint64(st.Ino), st.Uid, st.Gid, uint32(st.Mode)}
	}
	return ctx.Err()
}
