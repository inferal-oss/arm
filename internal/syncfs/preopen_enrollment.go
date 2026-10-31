package syncfs

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const EnrollmentOwnerLockName = ".arm-sync-owner.lock"

var ErrPreOpenEnrollment = errors.New("pre-open charged sync enrollment unavailable")

// PreOpenEnrollmentCandidate holds a cooperating process owner and fresh
// native observations before Manager startup writes. It is not admission:
// complete mount-wide project assignment, writer fencing, schema binding and
// startup integration remain separate requirements. Close is not concurrent
// with Check; the caller must join all users before releasing the lock.
type PreOpenEnrollmentCandidate struct {
	backing, project, selected, home *Root
	ownerLock                        *os.File
	lockStat                         unix.Stat_t
	enrollment                       *RecordedEnrollment
}

func privateOwnerLockFile(s unix.Stat_t) bool {
	return s.Mode&unix.S_IFMT == unix.S_IFREG && s.Mode&07777 == 0600 &&
		s.Uid == uint32(os.Geteuid()) && s.Nlink == 1 && s.Size == 0
}

func acquireEnrollmentOwnerLock(backing *Root) (*os.File, unix.Stat_t, error) {
	var before unix.Stat_t
	if backing == nil || backing.Check() != nil ||
		unix.Fstatat(backing.FD(), EnrollmentOwnerLockName, &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!privateOwnerLockFile(before) {
		return nil, unix.Stat_t{}, ErrPreOpenEnrollment
	}
	fd, err := unix.Openat(backing.FD(), EnrollmentOwnerLockName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, unix.Stat_t{}, ErrPreOpenEnrollment
	}
	file := os.NewFile(uintptr(fd), EnrollmentOwnerLockName)
	var opened unix.Stat_t
	if unix.Fstat(fd, &opened) != nil || !sameObject(before, opened) || !privateOwnerLockFile(opened) ||
		unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil ||
		checkEnrollmentOwnerLock(backing, file, opened) != nil {
		file.Close()
		return nil, unix.Stat_t{}, ErrPreOpenEnrollment
	}
	return file, opened, nil
}

func checkEnrollmentOwnerLock(backing *Root, file *os.File, expected unix.Stat_t) error {
	if backing == nil || file == nil || backing.Check() != nil {
		return ErrPreOpenEnrollment
	}
	var held, named unix.Stat_t
	if unix.Fstat(int(file.Fd()), &held) != nil ||
		unix.Fstatat(backing.FD(), EnrollmentOwnerLockName, &named, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!sameObject(expected, held) || !sameObject(held, named) ||
		!privateOwnerLockFile(held) || !privateOwnerLockFile(named) ||
		backing.Check() != nil {
		return ErrPreOpenEnrollment
	}
	return nil
}

// OpenPreOpenEnrollmentCandidate never creates or repairs native state. The
// owner file must be provisioned outside the home before this call. Its
// nonblocking flock excludes cooperating openers and offline maintenance.
// A positive result cannot be passed to the ordinary Manager Open path.
// The returned candidate owns its roots; callers must not concurrently Close
// it or any borrowed root while a candidate method is running.
func OpenPreOpenEnrollmentCandidate(ctx context.Context, backingPath, projectPath, selectedPath, homePath string) (_ *PreOpenEnrollmentCandidate, err error) {
	if ctx == nil || ctx.Err() != nil || filepath.Dir(projectPath) != backingPath ||
		filepath.Dir(selectedPath) != projectPath || filepath.Dir(homePath) != projectPath ||
		selectedPath == homePath {
		return nil, ErrPreOpenEnrollment
	}
	c := &PreOpenEnrollmentCandidate{}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()
	if c.backing, err = OpenRoot(backingPath); err != nil {
		return nil, ErrPreOpenEnrollment
	}
	if c.ownerLock, c.lockStat, err = acquireEnrollmentOwnerLock(c.backing); err != nil {
		return nil, ErrPreOpenEnrollment
	}
	if c.project, err = OpenRoot(projectPath); err != nil {
		return nil, ErrPreOpenEnrollment
	}
	if c.selected, err = OpenRoot(selectedPath); err != nil {
		return nil, ErrPreOpenEnrollment
	}
	if c.home, err = OpenRoot(homePath); err != nil {
		return nil, ErrPreOpenEnrollment
	}
	if c.enrollment, err = ReadEnrollmentRecord(c.backing); err != nil {
		return nil, ErrPreOpenEnrollment
	}
	if _, _, err = c.Check(ctx); err != nil {
		return nil, ErrPreOpenEnrollment
	}
	return c, nil
}

// Check refreshes both native quota and independent backing observations
// while the same owner file remains held and named. It rechecks all four roots
// and the named record after the backing read. This is point-in-time evidence
// among cooperating owners, not a lock against an uncooperative writer or
// proof of all project-ID assignments on the mount; it grants no Manager write.
// The caller must join other candidate users before Close.
func (c *PreOpenEnrollmentCandidate) Check(ctx context.Context) (NativeQuotaEvidence, BackingHeadroom, error) {
	return c.checkWith(ctx, ProbeNativeQuota, ProbeBackingHeadroom)
}

// checkWith is a package-private ordering seam for refusal controls. The
// production Check path always passes the native quota and backing probes.
func (c *PreOpenEnrollmentCandidate) checkWith(ctx context.Context,
	quotaProbe func(context.Context, *Root, *Root) (NativeQuotaEvidence, error),
	backingProbe func(context.Context, *Root, *Root, *RecordedEnrollment) (BackingHeadroom, error),
) (NativeQuotaEvidence, BackingHeadroom, error) {
	if ctx == nil || ctx.Err() != nil || c == nil || c.enrollment == nil ||
		c.project == nil || c.selected == nil || c.home == nil ||
		quotaProbe == nil || backingProbe == nil ||
		checkEnrollmentOwnerLock(c.backing, c.ownerLock, c.lockStat) != nil {
		return NativeQuotaEvidence{}, BackingHeadroom{}, ErrPreOpenEnrollment
	}
	r := c.enrollment.record
	if checkPreOpenRoots(c, r) != nil {
		return NativeQuotaEvidence{}, BackingHeadroom{}, ErrPreOpenEnrollment
	}
	quota, err := quotaProbe(ctx, c.selected, c.home)
	if err != nil || quota.Provider() != r.Provider || quota.Scope() != r.ScopeID ||
		quota.Mount() != r.MountID || quota.SelectedRootID() != r.SelectedRootID ||
		quota.ParentRootID() != r.HomeRootID || quota.HardBytes() != uint64(r.ChargedHardBytes) {
		return NativeQuotaEvidence{}, BackingHeadroom{}, ErrPreOpenEnrollment
	}
	headroom, err := backingProbe(ctx, c.backing, c.project, c.enrollment)
	if err != nil || checkPreOpenRoots(c, r) != nil {
		return NativeQuotaEvidence{}, BackingHeadroom{}, ErrPreOpenEnrollment
	}
	current, recordErr := ReadEnrollmentRecord(c.backing)
	if recordErr != nil || current.digest != c.enrollment.digest || current.record != r ||
		checkEnrollmentOwnerLock(c.backing, c.ownerLock, c.lockStat) != nil || ctx.Err() != nil {
		return NativeQuotaEvidence{}, BackingHeadroom{}, ErrPreOpenEnrollment
	}
	return quota, headroom, nil
}

func checkPreOpenRoots(c *PreOpenEnrollmentCandidate, r EnrollmentRecord) error {
	for _, pair := range []struct {
		root *Root
		want string
	}{
		{c.backing, r.BackingRootID}, {c.project, r.ProjectRootID},
		{c.selected, r.SelectedRootID}, {c.home, r.HomeRootID},
	} {
		if pair.root == nil || pair.root.Check() != nil {
			return ErrPreOpenEnrollment
		}
		got, err := pair.root.NativeID()
		if err != nil || got != pair.want {
			return ErrPreOpenEnrollment
		}
	}
	return nil
}

func (c *PreOpenEnrollmentCandidate) Record() EnrollmentRecord {
	if c == nil || c.enrollment == nil {
		return EnrollmentRecord{}
	}
	return c.enrollment.Record()
}

func (c *PreOpenEnrollmentCandidate) EnrollmentDigest() string {
	if c == nil || c.enrollment == nil {
		return ""
	}
	return c.enrollment.Digest()
}

func (c *PreOpenEnrollmentCandidate) Close() error {
	if c == nil {
		return nil
	}
	var first error
	for _, root := range []*Root{c.home, c.selected, c.project, c.backing} {
		if root != nil {
			if err := root.Close(); first == nil {
				first = err
			}
		}
	}
	if c.ownerLock != nil {
		if err := c.ownerLock.Close(); first == nil {
			first = err
		}
	}
	c.home, c.selected, c.project, c.backing, c.ownerLock = nil, nil, nil, nil, nil
	c.enrollment = nil
	return first
}
