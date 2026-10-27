package syncfs

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

const maxWorkspaceSurveyEntries = 100000
const maxWorkspaceSurveyDepth = 128

// WorkspaceAllocation is a bounded observation of st_blocks in one marked
// private workspace. Hard links, clones and shared filesystem blocks may be
// counted more than once; this is never exclusive reclamation evidence.
type WorkspaceAllocation struct {
	Entries        int
	AllocatedBytes int64
}

func surveyStable(a, b unix.Stat_t) bool {
	return stableStat(a, b) && a.Blocks == b.Blocks && a.Nlink == b.Nlink
}

func (v *WorkspaceAllocation) add(st unix.Stat_t) error {
	if v.Entries >= maxWorkspaceSurveyEntries || st.Blocks < 0 || st.Blocks > math.MaxInt64/512 || v.AllocatedBytes > math.MaxInt64-st.Blocks*512 {
		return fmt.Errorf("workspace allocation survey bound exceeded: %w", ErrUnknown)
	}
	v.Entries++
	v.AllocatedBytes += st.Blocks * 512
	return nil
}

// SurveyWorkspace reads one exact marked workspace through held descriptors.
// It refuses changed entries and crossings of the reported native mount
// identity: Linux requires STATX_MNT_ID; Darwin pairs Fsid and Mntonname.
// This is not an atomic snapshot against same-account writers, proof of
// exclusive blocks, or cleanup authority.
func SurveyWorkspace(ctx context.Context, selected, parent *Root, path string, want Reservation) (WorkspaceAllocation, error) {
	var out WorkspaceAllocation
	if ctx == nil {
		return out, ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	w, err := InspectWorkspace(selected, parent, path, want)
	if err != nil {
		return out, err
	}
	defer w.Close()
	parentMount, err := surveyMountFD(parent.FD())
	if err != nil {
		return out, err
	}
	baseMount, err := surveyMountFD(w.Base.FD())
	if err != nil || baseMount != parentMount {
		return out, ErrChanged
	}
	baseStat, err := statFD(w.Base.FD())
	if err != nil {
		return out, err
	}
	fd, err := unix.Openat(w.Base.FD(), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return out, err
	}
	dir := os.NewFile(uintptr(fd), w.Base.Path())
	defer dir.Close()
	scanStat, err := statFD(fd)
	if err != nil || !surveyStable(baseStat, scanStat) {
		return out, ErrChanged
	}
	if err := out.add(baseStat); err != nil {
		return WorkspaceAllocation{}, err
	}
	if err := surveyWorkspaceDirectory(ctx, dir, 0, uint64(baseStat.Dev), baseMount, &out); err != nil {
		return WorkspaceAllocation{}, err
	}
	endStat, err := statFD(w.Base.FD())
	if err != nil || !surveyStable(baseStat, endStat) {
		return WorkspaceAllocation{}, ErrChanged
	}
	for _, root := range []*Root{selected, parent, w.Base, w.Stage, w.Quarantine} {
		if err := root.Check(); err != nil {
			return WorkspaceAllocation{}, err
		}
	}
	return out, nil
}

func surveyWorkspaceDirectory(ctx context.Context, dir *os.File, depth int, device uint64, mount string, out *WorkspaceAllocation) error {
	if depth > maxWorkspaceSurveyDepth {
		return fmt.Errorf("workspace survey depth exceeded: %w", ErrUnknown)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	currentMount, err := surveyMountFD(int(dir.Fd()))
	if err != nil || currentMount != mount {
		return ErrChanged
	}
	for {
		entries, readErr := dir.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			if err := validComponent(name); err != nil {
				return ErrChanged
			}
			before, err := statNamed(int(dir.Fd()), name)
			if err != nil {
				return err
			}
			if uint64(before.Dev) != device || before.Uid != uint32(os.Geteuid()) {
				return ErrChanged
			}
			kind := before.Mode & unix.S_IFMT
			if kind != unix.S_IFDIR && kind != unix.S_IFREG && kind != unix.S_IFLNK {
				return ErrChanged
			}
			if kind != unix.S_IFLNK {
				childMount, err := surveyMountAt(int(dir.Fd()), name)
				if err != nil || childMount != mount {
					return ErrChanged
				}
			}
			if err := out.add(before); err != nil {
				return err
			}
			if kind == unix.S_IFDIR {
				fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
				if err != nil {
					return err
				}
				child := os.NewFile(uintptr(fd), name)
				opened, openErr := statFD(fd)
				if openErr == nil && !surveyStable(before, opened) {
					openErr = ErrChanged
				}
				if openErr == nil {
					openErr = surveyWorkspaceDirectory(ctx, child, depth+1, device, mount, out)
				}
				closeErr := child.Close()
				if openErr != nil {
					return openErr
				}
				if closeErr != nil {
					return closeErr
				}
			}
			after, err := statNamed(int(dir.Fd()), name)
			if err != nil || !surveyStable(before, after) {
				return ErrChanged
			}
			if kind != unix.S_IFLNK {
				childMount, err := surveyMountAt(int(dir.Fd()), name)
				if err != nil || childMount != mount {
					return ErrChanged
				}
			}
		}
		if readErr == io.EOF {
			return nil
		}
	}
}
