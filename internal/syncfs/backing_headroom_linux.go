//go:build linux && (amd64 || arm64)

package syncfs

import (
	"context"
	"math"

	"golang.org/x/sys/unix"
)

func probePlatformBackingHeadroom(ctx context.Context, backing, project *Root, record EnrollmentRecord) (BackingHeadroom, error) {
	if err := ctx.Err(); err != nil {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	backingMount, err := surveyMountFD(backing.FD())
	if err != nil || backingMount != record.MountID {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	projectMount, err := surveyMountFD(project.FD())
	if err != nil || projectMount != backingMount {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	backingAttr, err := xfsGetAttr(backing.FD())
	if err != nil || backingAttr.ProjectID != 0 || backingAttr.Flags&fsXflagProjInherit != 0 {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	projectAttr, err := xfsGetAttr(project.FD())
	if err != nil || projectAttr.ProjectID != record.ProjectID || projectAttr.Flags&fsXflagProjInherit == 0 {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	var fs unix.Statfs_t
	if err := unix.Fstatfs(backing.FD(), &fs); err != nil || fs.Type != unix.XFS_SUPER_MAGIC || fs.Bsize <= 0 ||
		fs.Blocks == 0 || fs.Bfree > fs.Blocks || fs.Bavail > fs.Bfree || fs.Blocks > math.MaxInt64/uint64(fs.Bsize) {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	if err := ctx.Err(); err != nil {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	backingAfter, err := xfsGetAttr(backing.FD())
	if err != nil || backingAfter != backingAttr || backingAfter.ProjectID != 0 ||
		backingAfter.Flags&fsXflagProjInherit != 0 {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	projectAfter, err := xfsGetAttr(project.FD())
	if err != nil || projectAfter != projectAttr || projectAfter.ProjectID != record.ProjectID ||
		projectAfter.Flags&fsXflagProjInherit == 0 {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	if mount, err := surveyMountFD(backing.FD()); err != nil || mount != backingMount {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	if mount, err := surveyMountFD(project.FD()); err != nil || mount != projectMount {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	return BackingHeadroom{mount: backingMount, rootID: record.BackingRootID,
		total: int64(fs.Blocks * uint64(fs.Bsize)), available: int64(fs.Bavail * uint64(fs.Bsize))}, nil
}
