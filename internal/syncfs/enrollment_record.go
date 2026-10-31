package syncfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// EnrollmentRecordName is stored under the held backing anchor, outside the
// selected project's home. Reading it is not native enrollment authority.
const EnrollmentRecordName = ".arm-sync-enrollment"

const maxEnrollmentRecordBytes = 4096

var ErrEnrollmentRecord = errors.New("charged sync enrollment record unavailable")

// EnrollmentRecord is a canonical, persisted intent for one dedicated XFS
// project. It cannot grant a scope: the native project, mount, quota and
// exclusive owner must still be checked before any Manager startup write.
type EnrollmentRecord struct {
	Version          string `json:"version"`
	Provider         string `json:"provider"`
	MountID          string `json:"mount_id"`
	ScopeID          string `json:"scope_id"`
	ProjectID        uint32 `json:"project_id"`
	ProjectRootID    string `json:"project_root_id"`
	SelectedRootID   string `json:"selected_root_id"`
	HomeRootID       string `json:"home_root_id"`
	BackingRootID    string `json:"backing_root_id"`
	OwnerUID         string `json:"owner_uid"`
	OwnerEpoch       uint64 `json:"owner_epoch"`
	ChargedHardBytes int64  `json:"charged_hard_bytes"`
}

// RecordedEnrollment can only come from the held, exact named record read.
// A zero value or copied JSON fields cannot authorize the backing probe.
// It still does not establish native quota ownership or Manager admission.
type RecordedEnrollment struct {
	record   EnrollmentRecord
	digest   string
	anchorID string
}

func (e *RecordedEnrollment) Record() EnrollmentRecord {
	if e == nil {
		return EnrollmentRecord{}
	}
	return e.record
}

func (e *RecordedEnrollment) Digest() string {
	if e == nil {
		return ""
	}
	return e.digest
}

func boundedEnrollmentText(s string, max int) bool {
	if len(s) == 0 || len(s) > max || strings.TrimSpace(s) != s {
		return false
	}
	for _, c := range s {
		if c < ' ' || c == 0x7f {
			return false
		}
	}
	return true
}

func (r EnrollmentRecord) valid() bool {
	uid, uidErr := strconv.ParseUint(r.OwnerUID, 10, 32)
	return r.Version == "arm-sync-enrollment-v1" && r.Provider == "xfs-project-quota" &&
		boundedEnrollmentText(r.MountID, 512) && boundedEnrollmentText(r.ScopeID, 512) &&
		boundedEnrollmentText(r.ProjectRootID, 512) && boundedEnrollmentText(r.SelectedRootID, 512) &&
		boundedEnrollmentText(r.HomeRootID, 512) && boundedEnrollmentText(r.BackingRootID, 512) &&
		uidErr == nil && strconv.FormatUint(uid, 10) == r.OwnerUID && r.ProjectID != 0 &&
		r.OwnerEpoch != 0 && r.OwnerEpoch <= math.MaxInt64 && r.ChargedHardBytes > 0
}

// DecodeEnrollmentRecord accepts only the one bounded canonical encoding.
// In particular, aliases, duplicate fields, trailing bytes and unknown fields
// cannot be normalized into a different ownership claim.
func DecodeEnrollmentRecord(raw []byte) (EnrollmentRecord, error) {
	var zero EnrollmentRecord
	if len(raw) == 0 || len(raw) > maxEnrollmentRecordBytes {
		return zero, ErrEnrollmentRecord
	}
	var r EnrollmentRecord
	if err := json.Unmarshal(raw, &r); err != nil || !r.valid() {
		return zero, ErrEnrollmentRecord
	}
	canonical, err := json.Marshal(r)
	if err != nil || !bytes.Equal(raw, canonical) {
		return zero, ErrEnrollmentRecord
	}
	return r, nil
}

func privateEnrollmentFile(s unix.Stat_t) bool {
	return s.Mode&unix.S_IFMT == unix.S_IFREG && s.Mode&07777 == 0600 &&
		s.Uid == uint32(os.Geteuid()) && s.Nlink == 1 &&
		s.Size > 0 && s.Size <= maxEnrollmentRecordBytes
}

// ReadEnrollmentRecord opens only the fixed no-follow file relative to the
// held backing anchor. It never creates or repairs enrollment. Its digest is
// an integrity comparison for a later native verifier, not bearer authority.
// The caller must keep backing open, with no concurrent Root.Close, until
// this call has returned.
func ReadEnrollmentRecord(backing *Root) (*RecordedEnrollment, error) {
	if backing == nil || backing.Check() != nil {
		return nil, ErrEnrollmentRecord
	}
	var before unix.Stat_t
	if err := unix.Fstatat(backing.FD(), EnrollmentRecordName, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil ||
		!privateEnrollmentFile(before) {
		return nil, ErrEnrollmentRecord
	}
	fd, err := unix.Openat(backing.FD(), EnrollmentRecordName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrEnrollmentRecord
	}
	file := os.NewFile(uintptr(fd), EnrollmentRecordName)
	defer file.Close()
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || !sameObject(before, opened) ||
		!privateEnrollmentFile(opened) || opened.Size != before.Size || opened.Mode != before.Mode {
		return nil, ErrEnrollmentRecord
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxEnrollmentRecordBytes+1))
	if err != nil || len(raw) != int(before.Size) {
		return nil, ErrEnrollmentRecord
	}
	var after unix.Stat_t
	var namedAfter unix.Stat_t
	if err := unix.Fstat(fd, &after); err != nil || !sameObject(opened, after) ||
		!privateEnrollmentFile(after) || after.Size != opened.Size || after.Mode != opened.Mode ||
		unix.Fstatat(backing.FD(), EnrollmentRecordName, &namedAfter, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameObject(after, namedAfter) || !privateEnrollmentFile(namedAfter) ||
		namedAfter.Size != after.Size || namedAfter.Mode != after.Mode || backing.Check() != nil {
		return nil, ErrEnrollmentRecord
	}
	r, err := DecodeEnrollmentRecord(raw)
	if err != nil {
		return nil, err
	}
	if r.OwnerUID != strconv.Itoa(os.Geteuid()) {
		return nil, ErrEnrollmentRecord
	}
	anchorID, err := backing.NativeID()
	if err != nil || anchorID != r.BackingRootID {
		return nil, ErrEnrollmentRecord
	}
	sum := sha256.Sum256(append([]byte("arm-sync-enrollment-record-v1\x00"), raw...))
	return &RecordedEnrollment{record: r, digest: hex.EncodeToString(sum[:]), anchorID: anchorID}, nil
}
