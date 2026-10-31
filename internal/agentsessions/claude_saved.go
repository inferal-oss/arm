package agentsessions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// DiscoverSavedSessions exposes only supported local session markers. It does
// not claim a historical Claude chat catalog, explicit titles or native Project
// membership, and it never constructs an ARM/native activation identity.
func (c Claude) DiscoverSavedSessions(ctx context.Context, request SavedSessionRequest) SavedSessionResult {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	return c.discoverSaved(ctx, request, savedCatalogHooks{})
}

func (c Claude) discoverSaved(ctx context.Context, request SavedSessionRequest, hooks savedCatalogHooks) SavedSessionResult {
	out, err := c.readSaved(ctx, request, hooks)
	if err != nil {
		out.Sessions = nil
		savedWarning(&out, "Claude session-marker discovery is unavailable or unsupported.")
	}
	return out
}

func (c Claude) readSaved(ctx context.Context, request SavedSessionRequest, hooks savedCatalogHooks) (out SavedSessionResult, err error) {
	savedWarning(&out, "Claude coverage is local session markers only; historical chats and saved titles are unavailable.")
	if !request.valid() || !savedPath(c.Home) {
		return out, fmt.Errorf("unsupported Claude display scope")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	wanted := map[string]bool{}
	for _, path := range request.Directories {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if _, err = canonicalSavedDirectory(path); err != nil {
			return out, err
		}
		if hooks.afterDirectory != nil {
			hooks.afterDirectory(path)
		}
		if err = ctx.Err(); err != nil {
			return out, err
		}
		wanted[path] = true
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	home, err := filepath.EvalSymlinks(c.Home)
	if err != nil || !savedPath(home) {
		return out, errors.Join(err, fmt.Errorf("unsupported Claude display home"))
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if hooks.beforeMetadataOpen != nil {
		hooks.beforeMetadataOpen("home")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, hooks.close("home", root), ctx.Err()) }()
	homeInfo, err := root.Stat(".")
	if err != nil {
		return out, err
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if hooks.beforeMetadataOpen != nil {
		hooks.beforeMetadataOpen("marker-directory")
	}
	dir, err := root.OpenFile("sessions", os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, hooks.close("marker-directory", dir)) }()
	dirInfo, err := dir.Stat()
	if err != nil || !dirInfo.IsDir() {
		return out, errors.Join(err, fmt.Errorf("unsupported marker directory"))
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if hooks.beforeMetadataOpen != nil {
		hooks.beforeMetadataOpen("marker-root")
	}
	markers, err := root.OpenRoot("sessions")
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, hooks.close("marker-root", markers)) }()
	heldDir, err := markers.Stat(".")
	if err != nil || !os.SameFile(dirInfo, heldDir) {
		return out, errors.Join(err, fmt.Errorf("marker directory changed"))
	}
	entries, err := dir.ReadDir(Limit + 1)
	if err != nil && err != io.EOF {
		return out, err
	}
	if len(entries) > Limit {
		entries = entries[:Limit]
		out.Truncated = true
		savedWarning(&out, "Claude marker discovery reached its bounded window; other chats may be omitted.")
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var held []*storedFile
	defer func() {
		for _, file := range held {
			err = errors.Join(err, hooks.close("marker", file.file))
		}
	}()
	remaining := 4 << 20
	seen := map[string]string{}
	ambiguous := map[string]bool{}
	byID := map[string]SavedSession{}
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if hooks.beforeMetadataOpen != nil {
			hooks.beforeMetadataOpen("marker")
		}
		file, openErr := openStored(markers, entry.Name(), false)
		if openErr != nil {
			return out, openErr
		}
		held = append(held, file)
		if file.info.Size() > 64<<10 || file.info.Size() > int64(remaining) {
			return out, fmt.Errorf("Claude marker read bound")
		}
		data, readErr := io.ReadAll(io.LimitReader(file.file, int64(min(64<<10, remaining))+1))
		remaining -= len(data)
		if readErr != nil || len(data) > 64<<10 || remaining < 0 || !file.stable(markers) {
			return out, errors.Join(readErr, fmt.Errorf("Claude marker changed or exceeded bound"))
		}
		var marker struct {
			ID  string `json:"sessionId"`
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(data, &marker) != nil {
			savedWarning(&out, "Some Claude markers have unsupported IDs or directories and were omitted.")
			continue
		}
		id, parseErr := uuid.Parse(marker.ID)
		if parseErr != nil || id.String() != marker.ID {
			savedWarning(&out, "Some Claude markers have unsupported IDs or directories and were omitted.")
			continue
		}
		previous, exists := seen[marker.ID]
		seen[marker.ID] = marker.CWD
		if exists && previous != marker.CWD {
			ambiguous[marker.ID] = true
			delete(byID, marker.ID)
			savedWarning(&out, "Conflicting Claude marker directories were omitted.")
		}
		if ambiguous[marker.ID] {
			continue
		}
		directory, canonicalErr := canonicalSavedDirectory(marker.CWD)
		if canonicalErr != nil {
			savedWarning(&out, "Some Claude markers have unsupported IDs or directories and were omitted.")
			continue
		}
		if request.Scope == SavedExactDirectories && !wanted[directory] {
			continue
		}
		byID[marker.ID] = SavedSession{Ref: SavedSessionRef{Adapter: "claude", Home: home, ID: marker.ID}, Directory: directory, Updated: file.info.ModTime().UTC()}
	}
	if hooks.beforeStable != nil {
		hooks.beforeStable()
	}
	for _, file := range held {
		if !file.stable(markers) {
			return out, fmt.Errorf("Claude marker snapshot changed")
		}
	}
	current, statErr := root.Lstat("sessions")
	heldDir, heldErr := dir.Stat()
	currentHome, homeErr := os.Stat(home)
	if statErr != nil || heldErr != nil || homeErr != nil || !current.IsDir() || !os.SameFile(dirInfo, current) || !os.SameFile(dirInfo, heldDir) || !dirInfo.ModTime().Equal(current.ModTime()) || !dirInfo.ModTime().Equal(heldDir.ModTime()) || !os.SameFile(homeInfo, currentHome) {
		return out, errors.Join(statErr, heldErr, homeErr, fmt.Errorf("Claude marker directory or home changed"))
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		out.Sessions = append(out.Sessions, byID[id])
	}
	return out, ctx.Err()
}
