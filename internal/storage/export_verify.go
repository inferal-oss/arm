package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

// The root is an output entry too: a private manifest cannot establish a
// protected bundle if its containing directory became writable/readable by
// others. Repeat this check after hashing, when permissions may have changed.
func verifyExportRoot(root *os.File, owner uint32) error {
	var st unix.Stat_t
	if err := unix.Fstat(int(root.Fd()), &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != owner || st.Mode&07077 != 0 {
		return fmt.Errorf("export bundle root is not an owned protected directory")
	}
	return nil
}

func verifyExportBundle(ctx context.Context, root *os.File, files []model.ExportFile, owner uint32) error {
	if err := verifyExportRoot(root, owner); err != nil {
		return err
	}
	if len(files) == 0 || len(files) > MaxExportFiles {
		return fmt.Errorf("export manifest has an invalid file count")
	}
	mount, err := mountIdentity(int(root.Fd()))
	if err != nil {
		return err
	}
	expected := make(map[string]model.ExportFile)
	directories := map[string]bool{}
	for _, file := range files {
		path, err := exportRelative(file.Path)
		if err != nil || path != file.Path || expected[path].Path != "" || file.OutputIdentity == nil || len(file.SHA256) != 64 {
			return fmt.Errorf("export manifest is incomplete or ambiguous")
		}
		expected[path] = file
		for dir := filepath.Dir(path); dir != "."; dir = filepath.Dir(dir) {
			directories[dir] = true
		}
	}
	seen := map[string]bool{}
	var walk func(*os.File, string) error
	walk = func(dir *os.File, prefix string) error {
		fd, err := unix.Openat(int(dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		current := os.NewFile(uintptr(fd), "verify-export")
		defer current.Close()
		for {
			items, readErr := current.ReadDir(128)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return readErr
			}
			for _, item := range items {
				if err := ctx.Err(); err != nil {
					return err
				}
				path := filepath.Join(prefix, item.Name())
				var st unix.Stat_t
				if err := unix.Fstatat(fd, item.Name(), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
					return err
				}
				if st.Uid != owner {
					return fmt.Errorf("export entry owner changed")
				}
				if st.Mode&unix.S_IFMT == unix.S_IFDIR {
					if !directories[path] || st.Mode&0077 != 0 {
						return fmt.Errorf("unexpected or unprotected export directory")
					}
					childFD, err := unix.Openat(fd, item.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
					if err != nil {
						return err
					}
					child := os.NewFile(uintptr(childFD), path)
					childMount, e := mountIdentity(childFD)
					if e == nil && childMount != mount {
						e = fmt.Errorf("export directory crosses a mount boundary")
					}
					if e == nil {
						e = walk(child, path)
					}
					child.Close()
					if e != nil {
						return e
					}
					continue
				}
				file, ok := expected[path]
				if !ok || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Nlink != 1 || uint32(st.Mode)&07777 != file.Mode&0755 || st.Size != file.Size {
					return fmt.Errorf("export output is missing, changed, shared or unexpected")
				}
				f, err := openExportFile(root, path)
				if err != nil {
					return err
				}
				err = verifyExportFile(ctx, f, file, owner, mount)
				f.Close()
				if err != nil {
					return err
				}
				seen[path] = true
			}
			if errors.Is(readErr, io.EOF) {
				return nil
			}
		}
	}
	if err := walk(root, ""); err != nil {
		return err
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("export bundle does not contain every selected output")
	}
	return verifyExportRoot(root, owner)
}

func verifyExportFile(ctx context.Context, file *os.File, expected model.ExportFile, owner uint32, mount string) error {
	before, err := exportFileVersion(file, expected.ExportSelection, owner, mount)
	if err != nil || expected.OutputIdentity == nil || before.Identity != *expected.OutputIdentity || before.Identity == expected.Identity || before.Links != 1 || before.Size != expected.Size || before.Mode != expected.Mode&0755 {
		return fmt.Errorf("independent exported-file identity cannot be verified")
	}
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	remaining := expected.Size
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.ReadFull(file, buffer[:min(int64(len(buffer)), remaining)])
		if err != nil {
			return err
		}
		hash.Write(buffer[:n])
		remaining -= int64(n)
	}
	after, err := exportFileVersion(file, expected.ExportSelection, owner, mount)
	if err != nil || !sameExportVersion(before, after) || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return fmt.Errorf("exported-file content changed or its digest does not match")
	}
	return nil
}

// ReconcileExport only confirms a bundle already published by exclusive rename.
// A partial staging bundle is preserved for explicit operator handoff rather
// than guessed complete, automatically resumed or recursively deleted.
func (p *Provider) ReconcileExport(ctx context.Context, operation model.StorageExport) (model.StorageExport, error) {
	if !validName.MatchString(operation.ID) || operation.StageName != ".arm-export-"+operation.ID || operation.StageIdentity == nil {
		return operation, fmt.Errorf("export has no verified staging identity; preserve paths and use explicit handoff")
	}
	parent, err := exportParent(operation.Plan)
	if err != nil {
		return operation, err
	}
	defer parent.Close()
	var st unix.Stat_t
	stageErr := unix.Fstatat(int(parent.Fd()), operation.StageName, &st, unix.AT_SYMLINK_NOFOLLOW)
	if stageErr == nil {
		return operation, fmt.Errorf("staging output remains; inspect it and explicitly hand off retained partial output")
	}
	if !errors.Is(stageErr, unix.ENOENT) {
		return operation, stageErr
	}
	name := filepath.Base(operation.Plan.Destination)
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return operation, err
	}
	bundle := os.NewFile(uintptr(fd), name)
	defer bundle.Close()
	id, err := durableIdentity(fd, "")
	if err != nil || id != *operation.StageIdentity {
		return operation, fmt.Errorf("published bundle identity differs; no ownership adopted")
	}
	if err := verifyExportBundle(ctx, bundle, operation.Plan.Files, p.identity.Owner); err != nil {
		return operation, err
	}
	current, err := exportParent(operation.Plan)
	if err != nil {
		return operation, err
	}
	current.Close()
	publishedIdentity, err := durableIdentity(int(parent.Fd()), name)
	if err != nil || publishedIdentity != id {
		return operation, fmt.Errorf("published bundle changed during recovery verification")
	}
	if err := ctx.Err(); err != nil {
		return operation, err
	}
	if err := parent.Sync(); err != nil {
		return operation, err
	}
	operation.State = "published"
	operation.Error = ""
	operation.DestinationIdentity = &id
	operation.Detail = "Previously published bundle verified against recorded independent identities and content digests; source reclamation remains a separate lease action"
	operation.NextCommand = []string{"arm", "release", operation.Request.Lease}
	return operation, nil
}

func exportStagePath(operation model.StorageExport) string {
	return filepath.Join(filepath.Dir(operation.Plan.Destination), operation.StageName)
}

// ExportHandoffDetail makes the external responsibility explicit without
// deleting, publishing or adopting any uncertain path.
func ExportHandoffDetail(operation model.StorageExport) string {
	return "Operator assumes responsibility for any remaining output at " + strings.Join([]string{exportStagePath(operation), operation.Plan.Destination}, " and ") + "; no output was deleted and no exclusive physical reclamation is claimed"
}
