package syncfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const maxWorkspaceSearchEntries = 8192
const maxWorkspaceSearchMatches = 32

// FindWorkspaces searches the held private parent for all marked workspaces
// with the original reservation. One operation can own more than one workspace.
// The result is read-only recovery evidence, not cleanup or release authority.
// Incomplete, absent or over-limit evidence leaves the outcome unknown.
func FindWorkspaces(ctx context.Context, selected, parent *Root, want Reservation) (result []*Workspace, err error) {
	if ctx == nil || selected == nil || parent == nil || want.validate() != nil {
		return nil, ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := selected.Check(); err != nil {
		return nil, err
	}
	if err := parent.Check(); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(parent.FD(), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), parent.Path())
	defer dir.Close()
	parentStat, err := statFD(parent.FD())
	if err != nil {
		return nil, err
	}
	selectedStat, err := statFD(selected.FD())
	if err != nil || parentStat.Dev != selectedStat.Dev || parentStat.Uid != uint32(os.Geteuid()) || parentStat.Mode&0077 != 0 {
		return nil, ErrChanged
	}
	scanStat, err := statFD(fd)
	if err != nil || !sameObject(parentStat, scanStat) {
		return nil, ErrChanged
	}
	var found []*Workspace
	defer func() {
		// A successful return transfers ownership of every handle to the caller.
		if result == nil {
			for _, workspace := range found {
				_ = workspace.Close()
			}
		}
	}()
	count := 0
	for {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		entries, readErr := dir.ReadDir(128)
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		count += len(entries)
		if count > maxWorkspaceSearchEntries {
			err = fmt.Errorf("workspace search exceeded %d entries: %w", maxWorkspaceSearchEntries, ErrUnknown)
			return nil, err
		}
		for _, entry := range entries {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			path := filepath.Join(parent.Path(), entry.Name())
			if !workspaceName(path, parent.Path()) {
				if strings.HasPrefix(entry.Name(), ".arm-sync-") {
					return nil, fmt.Errorf("malformed workspace candidate %q: %w", entry.Name(), ErrUnknown)
				}
				continue
			}
			var base *Root
			base, err = OpenRoot(path)
			if err != nil {
				return nil, fmt.Errorf("unreadable workspace candidate %q: %w", entry.Name(), ErrUnknown)
			}
			var marker workspaceMarker
			marker, err = readWorkspaceMarker(base)
			_ = base.Close()
			if err != nil || marker.Version != 1 || marker.Reservation.validate() != nil {
				return nil, fmt.Errorf("incomplete workspace candidate %q: %w", entry.Name(), ErrUnknown)
			}
			if marker.Reservation != want {
				continue
			}
			var candidate *Workspace
			candidate, err = InspectWorkspace(selected, parent, path, want)
			if err != nil {
				return nil, fmt.Errorf("changed workspace candidate %q: %w", entry.Name(), ErrUnknown)
			}
			if len(found) >= maxWorkspaceSearchMatches {
				_ = candidate.Close()
				err = fmt.Errorf("workspace search exceeded %d matches: %w", maxWorkspaceSearchMatches, ErrUnknown)
				return nil, err
			}
			found = append(found, candidate)
		}
		if readErr == io.EOF {
			break
		}
	}
	if err = selected.Check(); err != nil {
		return nil, err
	}
	if err = parent.Check(); err != nil {
		return nil, err
	}
	if len(found) == 0 {
		err = fmt.Errorf("workspace not found in bounded search: %w", ErrUnknown)
		return nil, err
	}
	for _, workspace := range found {
		for _, root := range []*Root{workspace.Base, workspace.Stage, workspace.Quarantine} {
			if err = root.Check(); err != nil {
				return nil, err
			}
		}
	}
	err = nil
	return found, nil
}

// FindWorkspace is the single-workspace form of FindWorkspaces. It refuses
// ambiguity; callers inspecting a multi-workspace operation should use the
// collection form and keep every charged obligation unresolved.
func FindWorkspace(ctx context.Context, selected, parent *Root, want Reservation) (*Workspace, error) {
	found, err := FindWorkspaces(ctx, selected, parent, want)
	if err != nil {
		return nil, err
	}
	if len(found) == 1 {
		return found[0], nil
	}
	for _, workspace := range found {
		_ = workspace.Close()
	}
	return nil, fmt.Errorf("multiple workspaces for reservation: %w", ErrUnknown)
}
