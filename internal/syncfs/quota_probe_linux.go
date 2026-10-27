//go:build linux && (amd64 || arm64)

package syncfs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"
)

// These read-only ABI values come from linux/fs.h and linux/dqblk_xfs.h.
// quotactl_fd binds the query to the held filesystem descriptor (Linux 5.14+).
const (
	fsIOCFSGetXattr    = 0x801c581f
	fsXflagProjInherit = 0x00000200
	fsXflagRealtime    = 0x00000001
	fsXflagRTInherit   = 0x00000100
	xqmProjectQuota    = 2
	xqmGetQuota        = (('X'<<8 | 3) << 8) | xqmProjectQuota
	xqmGetStatusV      = (('X'<<8 | 8) << 8) | xqmProjectQuota
	fsQuotaProjectAcct = 1 << 4
	fsQuotaProjectEnfd = 1 << 5
	fsProjectQuotaType = 1 << 1
)

type xfsAttr struct {
	Flags, ExtSize, Nextents, ProjectID, COWExtSize uint32
	Pad                                             [8]byte
}

type xfsQuotaFileStat struct {
	Inode, Blocks uint64
	Extents, Pad  uint32
}

type xfsQuotaStatus struct {
	Version                                 int8
	Pad1                                    uint8
	Flags                                   uint16
	InCore                                  uint32
	User, Group, Project                    xfsQuotaFileStat
	BlockTimer, InodeTimer, RTBlockTimer    int32
	BlockWarn, InodeWarn, RTBlockWarn, Pad3 uint16
	Pad4                                    uint32
	Pad2                                    [7]uint64
}

type xfsDiskQuota struct {
	Version, Flags                                   int8
	FieldMask                                        uint16
	ID                                               uint32
	BlockHard, BlockSoft, InodeHard, InodeSoft       uint64
	Blocks, Inodes                                   uint64
	InodeTimer, BlockTimer                           int32
	InodeWarn, BlockWarn                             uint16
	InodeTimerHi, BlockTimerHi, RTBlockTimerHi, Pad2 int8
	RTBlockHard, RTBlockSoft, RTBlocks               uint64
	RTBlockTimer                                     int32
	RTBlockWarn                                      uint16
	Pad3                                             int16
	Pad4                                             [8]byte
}

func xfsGetAttr(fd int) (xfsAttr, error) {
	var out xfsAttr
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), fsIOCFSGetXattr, uintptr(unsafe.Pointer(&out)))
	if errno != 0 {
		return xfsAttr{}, errno
	}
	return out, nil
}

func xfsQuotaCall(fd int, command int, id uint32, out unsafe.Pointer) error {
	_, _, errno := unix.Syscall6(unix.SYS_QUOTACTL_FD, uintptr(fd), uintptr(command), uintptr(id), uintptr(out), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func xfsQuotaRefusal(err error) error {
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ESRCH) {
		return ErrQuotaUnsupported
	}
	return fmt.Errorf("XFS quota query %v: %w", err, ErrQuotaUnknown)
}

func xfsEvidenceFromReadings(mount string, selected, parent xfsAttr, status xfsQuotaStatus, quota xfsDiskQuota) (NativeQuotaEvidence, error) {
	if selected.ProjectID == 0 || parent.ProjectID != selected.ProjectID ||
		selected.Flags&fsXflagProjInherit == 0 || parent.Flags&fsXflagProjInherit == 0 ||
		selected.Flags&(fsXflagRealtime|fsXflagRTInherit) != 0 || parent.Flags&(fsXflagRealtime|fsXflagRTInherit) != 0 {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	if status.Version != 1 || status.Flags&fsQuotaProjectAcct == 0 {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	if status.Flags&fsQuotaProjectEnfd == 0 {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	if quota.Version != 1 || quota.Flags != fsProjectQuotaType || quota.ID != selected.ProjectID {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if quota.BlockHard == 0 || quota.Blocks > quota.BlockHard {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	if quota.BlockHard > math.MaxUint64/512 {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	return NativeQuotaEvidence{
		provider:  "xfs-project-quota",
		scope:     "xfs-project:" + mount + ":" + strconv.FormatUint(uint64(selected.ProjectID), 10),
		hardBytes: quota.BlockHard * 512,
		usedBytes: quota.Blocks * 512,
	}, nil
}

func probePlatformQuota(ctx context.Context, selected, parent *Root, mount string) (NativeQuotaEvidence, error) {
	var selectedFS, parentFS unix.Statfs_t
	if err := unix.Fstatfs(selected.FD(), &selectedFS); err != nil {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if err := unix.Fstatfs(parent.FD(), &parentFS); err != nil {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if selectedFS.Type != unix.XFS_SUPER_MAGIC || parentFS.Type != unix.XFS_SUPER_MAGIC {
		return NativeQuotaEvidence{}, ErrQuotaUnsupported
	}
	if err := ctx.Err(); err != nil {
		return NativeQuotaEvidence{}, err
	}
	selectedAttr, err := xfsGetAttr(selected.FD())
	if err != nil {
		return NativeQuotaEvidence{}, xfsQuotaRefusal(err)
	}
	parentAttr, err := xfsGetAttr(parent.FD())
	if err != nil {
		return NativeQuotaEvidence{}, xfsQuotaRefusal(err)
	}
	var status xfsQuotaStatus
	status.Version = 1
	if err := xfsQuotaCall(selected.FD(), xqmGetStatusV, 0, unsafe.Pointer(&status)); err != nil {
		return NativeQuotaEvidence{}, xfsQuotaRefusal(err)
	}
	var quota xfsDiskQuota
	if err := xfsQuotaCall(selected.FD(), xqmGetQuota, selectedAttr.ProjectID, unsafe.Pointer(&quota)); err != nil {
		return NativeQuotaEvidence{}, xfsQuotaRefusal(err)
	}
	v, err := xfsEvidenceFromReadings(mount, selectedAttr, parentAttr, status, quota)
	if err != nil {
		return NativeQuotaEvidence{}, err
	}
	var statusAfter xfsQuotaStatus
	statusAfter.Version = 1
	if err := xfsQuotaCall(selected.FD(), xqmGetStatusV, 0, unsafe.Pointer(&statusAfter)); err != nil {
		return NativeQuotaEvidence{}, xfsQuotaRefusal(err)
	}
	if statusAfter.Version != status.Version || statusAfter.Flags != status.Flags {
		return NativeQuotaEvidence{}, ErrChanged
	}
	var quotaAfter xfsDiskQuota
	if err := xfsQuotaCall(selected.FD(), xqmGetQuota, selectedAttr.ProjectID, unsafe.Pointer(&quotaAfter)); err != nil {
		return NativeQuotaEvidence{}, xfsQuotaRefusal(err)
	}
	if quotaAfter.Version != quota.Version || quotaAfter.Flags != quota.Flags || quotaAfter.ID != quota.ID || quotaAfter.BlockHard != quota.BlockHard {
		return NativeQuotaEvidence{}, ErrChanged
	}
	if quotaAfter.Blocks > quotaAfter.BlockHard {
		return NativeQuotaEvidence{}, ErrQuotaUnknown
	}
	if quotaAfter.Blocks > quota.Blocks {
		v.usedBytes = quotaAfter.Blocks * 512
	}
	selectedAfter, err := xfsGetAttr(selected.FD())
	if err != nil || selectedAfter != selectedAttr {
		return NativeQuotaEvidence{}, ErrChanged
	}
	parentAfter, err := xfsGetAttr(parent.FD())
	if err != nil || parentAfter != parentAttr {
		return NativeQuotaEvidence{}, ErrChanged
	}
	return v, nil
}
