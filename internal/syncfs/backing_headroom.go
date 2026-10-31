package syncfs

import (
	"context"
	"errors"
	"path/filepath"
)

var ErrBackingHeadroom = errors.New("independent charged-sync backing headroom unavailable")

// BackingHeadroom is a descriptor-bound observation, not disk admission or
// proof that the recorded project is exclusively owned by this Manager.
type BackingHeadroom struct {
	mount, rootID    string
	total, available int64
}

func (b BackingHeadroom) MountID() string       { return b.mount }
func (b BackingHeadroom) BackingRootID() string { return b.rootID }
func (b BackingHeadroom) TotalBytes() int64     { return b.total }
func (b BackingHeadroom) AvailableBytes() int64 { return b.available }

// ProbeBackingHeadroom reads the held parent of the dedicated project, not
// statfs(home), whose counters are limited by the project's remaining quota.
// The record is only an expected tuple; a future opener must separately prove
// native enrollment and ownership before using this observation for admission.
// The caller must keep both roots open, with no concurrent Root.Close, until
// this call has returned.
func ProbeBackingHeadroom(ctx context.Context, backing, project *Root, enrollment *RecordedEnrollment) (BackingHeadroom, error) {
	if ctx == nil || ctx.Err() != nil || backing == nil || project == nil || enrollment == nil ||
		!enrollment.record.valid() || enrollment.digest == "" ||
		filepath.Dir(project.Path()) != backing.Path() || backing.Check() != nil || project.Check() != nil {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	record := enrollment.record
	backingID, err := backing.NativeID()
	if err != nil || backingID != record.BackingRootID || backingID != enrollment.anchorID {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	current, err := ReadEnrollmentRecord(backing)
	if err != nil || current.digest != enrollment.digest || current.record != record {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	projectID, err := project.NativeID()
	if err != nil || projectID != record.ProjectRootID {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	result, err := probePlatformBackingHeadroom(ctx, backing, project, record)
	current, recordErr := ReadEnrollmentRecord(backing)
	backingAfterID, backingIDErr := backing.NativeID()
	projectAfterID, projectIDErr := project.NativeID()
	if err != nil || result.mount != record.MountID || result.rootID != backingID || result.total <= 0 ||
		result.available < 0 || result.available > result.total || recordErr != nil ||
		current.digest != enrollment.digest || current.record != record ||
		backingIDErr != nil || backingAfterID != backingID ||
		projectIDErr != nil || projectAfterID != projectID ||
		backing.Check() != nil || project.Check() != nil {
		return BackingHeadroom{}, ErrBackingHeadroom
	}
	return result, nil
}
