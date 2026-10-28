//go:build linux && (amd64 || arm64)

package syncfs

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const maxCompleteScopeEntries = 4096
const maxCompleteScopeDepth = 32

// This verifier observes an already enforced, dedicated XFS project. It does
// not assign a project ID, enable a quota, create a scope, or mint the original
// owner fence. The latter is intentionally unavailable in this first patch.
func verifyPlatformCompleteScope(ctx context.Context, claim ScopeClaim, project, selected, parent *Root) (ScopeBinding, error) {
	var zero ScopeBinding
	f := claim.fields
	if f.Provider != "xfs-project-quota" || selected.Path() == parent.Path() ||
		filepath.Dir(selected.Path()) != project.Path() || filepath.Dir(parent.Path()) != project.Path() {
		return zero, ErrQuotaUnsupported
	}
	for _, root := range []*Root{project, selected, parent} {
		if err := root.Check(); err != nil {
			return zero, ErrChanged
		}
		st, err := statFD(root.FD())
		if err != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR ||
			st.Uid != uint32(os.Geteuid()) || st.Mode&0077 != 0 {
			return zero, ErrQuotaUnsupported
		}
	}
	projectID, err := project.NativeID()
	if err != nil || projectID != f.ProjectRootID {
		return zero, ErrChanged
	}
	projectMount, err := surveyMountFD(project.FD())
	if err != nil || projectMount != f.MountID {
		return zero, ErrChanged
	}
	before, err := ProbeNativeQuota(ctx, selected, parent)
	if err != nil {
		return zero, err
	}
	if before.Provider() != f.Provider || before.Scope() != f.ScopeID ||
		before.Mount() != f.MountID || before.SelectedRootID() != f.SelectedRootID ||
		before.ParentRootID() != f.ParentRootID {
		return zero, ErrChanged
	}
	if before.HardBytes() > math.MaxInt64 || before.UsedBytes() > before.HardBytes() ||
		before.HardBytes() != uint64(f.ChargedHardBytes) {
		return zero, ErrQuotaUnsupported
	}
	remaining := maxCompleteScopeEntries
	if err := scanCompleteXFSProject(ctx, project.FD(), f.MountID, f.ProjectID, 0, &remaining); err != nil {
		return zero, err
	}
	after, err := ProbeNativeQuota(ctx, selected, parent)
	if err != nil {
		return zero, err
	}
	if after.Provider() != before.Provider() || after.Scope() != before.Scope() ||
		after.Mount() != before.Mount() || after.SelectedRootID() != before.SelectedRootID() ||
		after.ParentRootID() != before.ParentRootID() || after.HardBytes() != before.HardBytes() {
		return zero, ErrChanged
	}
	if err := project.Check(); err != nil {
		return zero, ErrChanged
	}
	projectAfterID, err := project.NativeID()
	if err != nil || projectAfterID != f.ProjectRootID {
		return zero, ErrChanged
	}
	projectAfterMount, err := surveyMountFD(project.FD())
	if err != nil || projectAfterMount != f.MountID {
		return zero, ErrChanged
	}
	used := before.UsedBytes()
	if after.UsedBytes() > used {
		used = after.UsedBytes()
	}
	if used > before.HardBytes() {
		return zero, ErrQuotaUnsupported
	}
	return ScopeBinding{
		provider: f.Provider, scopeID: f.ScopeID, mountID: f.MountID, projectRootID: f.ProjectRootID,
		selectedRootID: f.SelectedRootID, parentRootID: f.ParentRootID,
		ownershipDigest: f.OwnershipDigest, projectID: f.ProjectID,
		ownerEpoch: f.OwnerEpoch, hardBytes: before.HardBytes(), usedBytes: used,
	}, nil
}

// scanCompleteXFSProject refuses anything outside the first provider's
// regular-file/directory shape. It requires the separate original-owner fence
// to exclude concurrent Manager mutations; the walk alone grants no proof.
func scanCompleteXFSProject(ctx context.Context, fd int, mount string, project uint32, depth int, remaining *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxCompleteScopeDepth || *remaining <= 0 {
		return ErrQuotaUnknown
	}
	*remaining -= 1
	initial, err := statFD(fd)
	if err != nil || initial.Mode&unix.S_IFMT != unix.S_IFDIR {
		return ErrChanged
	}
	if observed, err := surveyMountFD(fd); err != nil || observed != mount {
		return ErrChanged
	}
	attr, err := xfsGetAttr(fd)
	if err != nil {
		return xfsQuotaRefusal(err)
	}
	if !xfsProjectEntryMatches(attr, project, true) {
		return ErrQuotaUnsupported
	}
	dup, err := unix.Openat(fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return ErrQuotaUnknown
	}
	dir := os.NewFile(uintptr(dup), "complete-scope")
	defer dir.Close()
	for {
		entries, readErr := dir.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return ErrQuotaUnknown
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			if *remaining <= 0 || validComponent(entry.Name()) != nil {
				return ErrQuotaUnknown
			}
			*remaining -= 1
			before, err := statNamed(fd, entry.Name())
			if err != nil || before.Uid != uint32(os.Geteuid()) {
				return ErrChanged
			}
			kind := before.Mode & unix.S_IFMT
			if kind != unix.S_IFDIR && kind != unix.S_IFREG {
				return ErrQuotaUnsupported
			}
			if kind == unix.S_IFREG && before.Nlink != 1 {
				return ErrQuotaUnsupported
			}
			if observed, err := surveyMountAt(fd, entry.Name()); err != nil || observed != mount {
				return ErrChanged
			}
			flags := unix.O_RDONLY | unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC
			if kind == unix.S_IFDIR {
				flags |= unix.O_DIRECTORY
			}
			child, err := unix.Openat(fd, entry.Name(), flags, 0)
			if err != nil {
				return ErrChanged
			}
			opened, statErr := statFD(child)
			openedMount, mountErr := surveyMountFD(child)
			childAttr, attrErr := xfsGetAttr(child)
			if statErr != nil || mountErr != nil || openedMount != mount ||
				attrErr != nil || !stableStat(before, opened) ||
				!xfsProjectEntryMatches(childAttr, project, kind == unix.S_IFDIR) {
				unix.Close(child)
				return ErrChanged
			}
			if kind == unix.S_IFDIR {
				err = scanCompleteXFSProject(ctx, child, mount, project, depth+1, remaining)
			}
			endAttr, endAttrErr := xfsGetAttr(child)
			endStat, endStatErr := statFD(child)
			closeErr := unix.Close(child)
			if err != nil {
				return err
			}
			if endAttrErr != nil || endStatErr != nil || closeErr != nil ||
				endAttr != childAttr || !stableStat(opened, endStat) {
				return ErrChanged
			}
			after, err := statNamed(fd, entry.Name())
			if err != nil || !stableStat(before, after) {
				return ErrChanged
			}
			if observed, err := surveyMountAt(fd, entry.Name()); err != nil || observed != mount {
				return ErrChanged
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	ending, err := statFD(fd)
	endingAttr, attrErr := xfsGetAttr(fd)
	if err != nil || attrErr != nil || !stableStat(initial, ending) || endingAttr != attr {
		return ErrChanged
	}
	return nil
}

func xfsProjectEntryMatches(attr xfsAttr, project uint32, directory bool) bool {
	if attr.ProjectID != project || attr.Flags&(fsXflagRealtime|fsXflagRTInherit) != 0 {
		return false
	}
	return !directory || attr.Flags&fsXflagProjInherit != 0
}
