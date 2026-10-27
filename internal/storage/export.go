package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"getarm.org/internal/model"
	"golang.org/x/sys/unix"
)

const MaxExportFiles = 64
const MaxExportBytes = 16 << 30

func (p *Provider) lockExport(ctx context.Context) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.mu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// NormalizeExportSelections accepts explicit regular files, never directories,
// glob patterns or parent traversal. Output paths belong to a new private bundle.
func NormalizeExportSelections(files []model.ExportSelection) ([]model.ExportSelection, error) {
	if len(files) == 0 || len(files) > MaxExportFiles {
		return nil, fmt.Errorf("choose between 1 and %d explicit files", MaxExportFiles)
	}
	out := append([]model.ExportSelection(nil), files...)
	seenSources, seenPaths := map[string]bool{}, map[string]bool{}
	for i := range out {
		var err error
		if out[i].Source, err = exportRelative(out[i].Source); err != nil {
			return nil, fmt.Errorf("source: %w", err)
		}
		if out[i].Path == "" {
			out[i].Path = filepath.Base(out[i].Source)
		}
		if out[i].Path, err = exportRelative(out[i].Path); err != nil {
			return nil, fmt.Errorf("output: %w", err)
		}
		if seenSources[out[i].Source] || seenPaths[out[i].Path] {
			return nil, fmt.Errorf("source and output paths must be unique")
		}
		seenSources[out[i].Source], seenPaths[out[i].Path] = true, true
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	for i := range out {
		for j := 0; j < i; j++ {
			if strings.HasPrefix(out[i].Path, out[j].Path+string(filepath.Separator)) {
				return nil, fmt.Errorf("a file output cannot also be another output's directory")
			}
		}
	}
	return out, nil
}

func exportRelative(path string) (string, error) {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || !filepath.IsLocal(path) {
		return "", fmt.Errorf("use a relative path inside the managed directory")
	}
	for _, r := range path {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return "", fmt.Errorf("path contains control characters")
		}
	}
	path = filepath.Clean(path)
	if path == "." || len(strings.Split(path, string(filepath.Separator))) > 128 {
		return "", fmt.Errorf("choose a file path with at most 128 components")
	}
	return path, nil
}

func exportPathText(path string) bool {
	if !utf8.ValidString(path) || len(path) > 4096 {
		return false
	}
	for _, r := range path {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

// openExportDirectory resolves a previously canonicalized absolute path one
// directory at a time. No component is followed through a symlink.
func openExportDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("directory path must be canonical and absolute")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, component := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if component == "" {
			continue
		}
		next, e := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openExportFile(root *os.File, path string) (*os.File, error) {
	if clean, err := exportRelative(path); err != nil || clean != path {
		return nil, fmt.Errorf("invalid stored export file path")
	}
	components := strings.Split(path, string(filepath.Separator))
	fd, err := unix.FcntlInt(root.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for i, component := range components {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(components)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, component, flags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}

func exportFileVersion(f *os.File, selection model.ExportSelection, expectedOwner uint32, expectedMount string) (model.ExportFile, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return model.ExportFile{}, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != expectedOwner || st.Size < 0 || st.Size > MaxExportBytes {
		return model.ExportFile{}, fmt.Errorf("selected source must be an owned regular file within export size limits")
	}
	mount, err := mountIdentity(int(f.Fd()))
	if err != nil || mount != expectedMount {
		return model.ExportFile{}, fmt.Errorf("source crosses a filesystem or mount boundary")
	}
	id, err := durableIdentity(int(f.Fd()), "")
	if err != nil {
		return model.ExportFile{}, err
	}
	return model.ExportFile{ExportSelection: selection, Identity: id, Size: st.Size, Mode: uint32(st.Mode) & 0777, Links: uint64(st.Nlink), ModifiedSecond: st.Mtim.Sec, ModifiedNanos: st.Mtim.Nsec, ChangedSecond: st.Ctim.Sec, ChangedNanos: st.Ctim.Nsec}, nil
}

func sameExportVersion(a, b model.ExportFile) bool {
	a.SHA256, b.SHA256 = "", ""
	a.OutputIdentity, b.OutputIdentity = nil, nil
	return a == b
}

// countExportLinks checks the selected inodes' link counts across the entire
// verified managed namespace. Cargo's internal hardlinks are allowed; a link
// outside that namespace is not evidence of exclusively managed ownership.
func (p *Provider) countExportLinks(ctx context.Context, root *os.File, selected map[model.FileIdentity]uint64) (map[model.FileIdentity]uint64, error) {
	counts := make(map[model.FileIdentity]uint64)
	entries := 0
	var walk func(*os.File, int) error
	walk = func(dir *os.File, depth int) error {
		if depth > 128 {
			return fmt.Errorf("export directory depth exceeded")
		}
		fd, err := unix.Openat(int(dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		current := os.NewFile(uintptr(fd), "export-inventory")
		defer current.Close()
		mount, err := mountIdentity(fd)
		if err != nil || mount != p.mount {
			return fmt.Errorf("export inventory crosses a mount boundary")
		}
		for {
			batch, e := current.ReadDir(128)
			if e != nil && !errors.Is(e, io.EOF) {
				return e
			}
			for _, item := range batch {
				if err := ctx.Err(); err != nil {
					return err
				}
				entries++
				if entries > 100000 {
					return fmt.Errorf("export inventory entry limit exceeded")
				}
				var st unix.Stat_t
				if err := unix.Fstatat(fd, item.Name(), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
					return err
				}
				if st.Uid != p.identity.Owner || uint64(st.Dev) != p.identity.Device {
					return fmt.Errorf("export inventory contains unowned or foreign entries")
				}
				if st.Mode&unix.S_IFMT == unix.S_IFDIR {
					childFD, err := unix.Openat(fd, item.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
					if err != nil {
						return err
					}
					child := os.NewFile(uintptr(childFD), item.Name())
					err = walk(child, depth+1)
					child.Close()
					if err != nil {
						return err
					}
				} else if st.Mode&unix.S_IFMT == unix.S_IFREG {
					id, err := durableIdentity(fd, item.Name())
					if err != nil {
						return err
					}
					if _, ok := selected[id]; ok {
						counts[id]++
					}
				}
			}
			if errors.Is(e, io.EOF) {
				return nil
			}
		}
	}
	return counts, walk(root, 0)
}

func (p *Provider) exportIdle(ctx context.Context, a model.Artifact) (*os.File, error) {
	name, root, err := p.locate(a)
	if err != nil {
		return nil, err
	}
	if name != a.Name || a.Phase != "owned" {
		root.Close()
		return nil, fmt.Errorf("export requires an active owned allocation before quarantine")
	}
	survey, err := p.survey(ctx, root)
	if err == nil {
		var active []int
		active, err = activeUsers(ctx, p.Path(name), survey.identities)
		if err == nil && len(active) != 0 {
			err = fmt.Errorf("managed files or working directories are active in processes %v", active)
		}
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

func (p *Provider) validateExportFiles(ctx context.Context, root *os.File, files []model.ExportFile) error {
	selected := map[model.FileIdentity]uint64{}
	for _, expected := range files {
		f, err := openExportFile(root, expected.Source)
		if err != nil {
			return err
		}
		actual, err := exportFileVersion(f, expected.ExportSelection, p.identity.Owner, p.mount)
		f.Close()
		if err != nil {
			return err
		}
		if !sameExportVersion(expected, actual) {
			return fmt.Errorf("source %q changed after the export plan", expected.Source)
		}
		selected[expected.Identity] = expected.Links
	}
	counts, err := p.countExportLinks(ctx, root, selected)
	if err != nil {
		return err
	}
	for id, n := range selected {
		if n == 0 || counts[id] != n {
			return fmt.Errorf("selected source has shared/external or changing hardlinks; no export attempted")
		}
	}
	return nil
}

func exportParent(plan model.StorageExportPlan) (*os.File, error) {
	parent := filepath.Dir(plan.Destination)
	f, err := openExportDirectory(parent)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err = unix.Fstat(int(f.Fd()), &st); err == nil && (st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0) {
		err = fmt.Errorf("export parent must be owned by ARM's user and not writable by group/others")
	}
	if err == nil {
		var id model.FileIdentity
		id, err = durableIdentity(int(f.Fd()), "")
		if err == nil && plan.ParentIdentity.BirthKnown && id != plan.ParentIdentity {
			err = fmt.Errorf("export parent identity changed")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func exportOutside(parent *os.File, forbidden []model.FileIdentity) error {
	fd, err := unix.FcntlInt(parent.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { unix.Close(fd) }()
	for range 128 {
		current, err := durableIdentity(fd, "")
		if err != nil {
			return err
		}
		for _, id := range forbidden {
			if current == id {
				return fmt.Errorf("export destination must be outside ARM state and managed allocations")
			}
		}
		next, err := unix.Openat(fd, "..", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		above, err := durableIdentity(next, "")
		if err != nil {
			unix.Close(next)
			return err
		}
		if current.Device == above.Device && current.Inode == above.Inode {
			unix.Close(next)
			return nil
		}
		unix.Close(fd)
		fd = next
	}
	return fmt.Errorf("export parent ancestry limit exceeded")
}

// PlanExport reads ownership/activity/versions and checks a new destination. It
// never creates output. Only explicit regular files are eligible.
func (p *Provider) PlanExport(ctx context.Context, a model.Artifact, destination string, selections []model.ExportSelection, maxBytes int64, forbiddenPaths []string) (model.StorageExportPlan, error) {
	plan := model.StorageExportPlan{Source: a, Detail: "Explicit independent copies into a new operator-owned bundle; no source deletion, overwrite or claim of exclusive block reclamation"}
	if err := p.lockExport(ctx); err != nil {
		return plan, err
	}
	defer p.mu.Unlock()
	selections, err := NormalizeExportSelections(selections)
	if err != nil {
		return plan, err
	}
	if !filepath.IsAbs(destination) || filepath.Base(destination) == "." || destination == "/" || !exportPathText(destination) {
		return plan, fmt.Errorf("choose a new absolute bundle destination")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Dir(destination))
	if err != nil {
		return plan, err
	}
	plan.Destination = filepath.Join(canonical, filepath.Base(destination))
	if !exportPathText(plan.Destination) || len(canonical)+len("/.arm-export-")+32 > 4096 {
		return plan, fmt.Errorf("canonical export path is too long or contains control characters")
	}
	parent, err := exportParent(plan)
	if err != nil {
		return plan, err
	}
	defer parent.Close()
	plan.ParentIdentity, err = durableIdentity(int(parent.Fd()), "")
	if err != nil {
		return plan, err
	}
	forbidden := []model.FileIdentity{p.identity}
	for _, path := range forbiddenPaths {
		canonical, e := filepath.EvalSymlinks(path)
		if e != nil {
			return plan, e
		}
		f, e := openExportDirectory(canonical)
		if e != nil {
			return plan, e
		}
		id, e := durableIdentity(int(f.Fd()), "")
		f.Close()
		if e != nil {
			return plan, e
		}
		forbidden = append(forbidden, id)
	}
	if err := exportOutside(parent, forbidden); err != nil {
		return plan, err
	}
	var st unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), filepath.Base(plan.Destination), &st, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) {
		if err == nil {
			err = fmt.Errorf("export destination already exists; nothing will be overwritten")
		}
		return plan, err
	}
	root, err := p.exportIdle(ctx, a)
	if err != nil {
		return plan, err
	}
	defer root.Close()
	for _, selection := range selections {
		f, err := openExportFile(root, selection.Source)
		if err != nil {
			return plan, err
		}
		version, err := exportFileVersion(f, selection, p.identity.Owner, p.mount)
		f.Close()
		if err != nil {
			return plan, err
		}
		if version.Size > math.MaxInt64-plan.Bytes || version.Size > min(maxBytes, MaxExportBytes)-plan.Bytes {
			return plan, fmt.Errorf("selected output exceeds the storage promise or export size limit")
		}
		plan.Bytes += version.Size
		plan.Files = append(plan.Files, version)
	}
	if err := p.validateExportFiles(ctx, root, plan.Files); err != nil {
		return plan, err
	}
	available, err := availableExportBytes(int(parent.Fd()))
	if err != nil || plan.Bytes > available || available-plan.Bytes < 1<<20 {
		return plan, fmt.Errorf("export destination has insufficient verified available filesystem space")
	}
	return plan, nil
}

func createExportOutput(stage *os.File, path string) (*os.File, error) {
	if clean, err := exportRelative(path); err != nil || clean != path {
		return nil, fmt.Errorf("invalid stored export output path")
	}
	parts := strings.Split(path, string(filepath.Separator))
	fd, err := unix.FcntlInt(stage.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range parts[:len(parts)-1] {
		if err := unix.Mkdirat(fd, part, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			unix.Close(fd)
			return nil, err
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err == nil {
			err = unix.Fsync(fd)
		}
		unix.Close(fd)
		if err != nil {
			if next >= 0 {
				unix.Close(next)
			}
			return nil, err
		}
		fd = next
	}
	out, err := unix.Openat(fd, parts[len(parts)-1], unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err == nil {
		err = unix.Fsync(fd)
	}
	unix.Close(fd)
	if err != nil {
		if out >= 0 {
			unix.Close(out)
		}
		return nil, err
	}
	return os.NewFile(uintptr(out), path), nil
}

func copyExportFile(ctx context.Context, root, stage *os.File, expected model.ExportFile, owner uint32, mount string) (string, *model.FileIdentity, error) {
	source, err := openExportFile(root, expected.Source)
	if err != nil {
		return "", nil, err
	}
	defer source.Close()
	before, err := exportFileVersion(source, expected.ExportSelection, owner, mount)
	if err != nil || !sameExportVersion(before, expected) {
		return "", nil, fmt.Errorf("source changed before copying %q", expected.Source)
	}
	out, err := createExportOutput(stage, expected.Path)
	if err != nil {
		return "", nil, err
	}
	defer out.Close()
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	remaining := expected.Size
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		n, readErr := io.ReadFull(source, buffer[:min(int64(len(buffer)), remaining)])
		if n > 0 {
			if _, err := out.Write(buffer[:n]); err != nil {
				return "", nil, err
			}
			hash.Write(buffer[:n])
			remaining -= int64(n)
		}
		if readErr != nil {
			return "", nil, readErr
		}
	}
	after, err := exportFileVersion(source, expected.ExportSelection, owner, mount)
	if err != nil || !sameExportVersion(before, after) {
		return "", nil, fmt.Errorf("source changed while copying %q", expected.Source)
	}
	// Private bundle; preserve ordinary executable/read bits but never setid,
	// sticky or group/other write permissions from build-tool output.
	if err := out.Chmod(os.FileMode(expected.Mode & 0755)); err != nil {
		return "", nil, err
	}
	if err := out.Sync(); err != nil {
		return "", nil, err
	}
	id, err := durableIdentity(int(out.Fd()), "")
	if err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(hash.Sum(nil)), &id, nil
}

// Export copies and exclusively publishes a previously journalled plan. Each
// checkpoint must persist the supplied operation before the next side effect.
// Failures intentionally preserve partial/uncertain output for explicit review.
func (p *Provider) Export(ctx context.Context, operation model.StorageExport, checkpoint func(model.StorageExport) error) (model.StorageExport, error) {
	if !validName.MatchString(operation.ID) || operation.StageName != ".arm-export-"+operation.ID || !operation.Plan.ParentIdentity.BirthKnown || len(operation.Plan.Files) == 0 || len(operation.Plan.Files) > MaxExportFiles {
		return operation, fmt.Errorf("invalid export operation identity")
	}
	plan := operation.Plan
	parent, err := exportParent(plan)
	if err != nil {
		return operation, err
	}
	defer parent.Close()
	if err := p.lockExport(ctx); err != nil {
		return operation, err
	}
	root, err := p.exportIdle(ctx, plan.Source)
	if err == nil {
		err = p.validateExportFiles(ctx, root, plan.Files)
	}
	p.mu.Unlock()
	if err != nil {
		if root != nil {
			root.Close()
		}
		return operation, err
	}
	defer root.Close()
	if err := unix.Mkdirat(int(parent.Fd()), operation.StageName, 0700); err != nil {
		return operation, err
	}
	stageFD, err := unix.Openat(int(parent.Fd()), operation.StageName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return operation, err
	}
	stage := os.NewFile(uintptr(stageFD), operation.StageName)
	defer stage.Close()
	id, err := durableIdentity(stageFD, "")
	if err != nil {
		return operation, err
	}
	operation.StageIdentity = &id
	operation.State = "copying"
	if err := parent.Sync(); err != nil {
		return operation, err
	}
	if err := checkpoint(operation); err != nil {
		return operation, err
	}
	for i, selected := range plan.Files {
		digest, identity, err := copyExportFile(ctx, root, stage, selected, p.identity.Owner, p.mount)
		if err != nil {
			return operation, err
		}
		operation.Plan.Files[i].SHA256 = digest
		operation.Plan.Files[i].OutputIdentity = identity
	}
	if err := stage.Sync(); err != nil {
		return operation, err
	}
	// Closing file descriptors in copyExportFile lets native activity inspection
	// distinguish another consumer from ARM's own completed source reads.
	if err := p.lockExport(ctx); err != nil {
		return operation, err
	}
	check, err := p.exportIdle(ctx, plan.Source)
	if err == nil {
		err = p.validateExportFiles(ctx, check, plan.Files)
	}
	if check != nil {
		check.Close()
	}
	p.mu.Unlock()
	if err != nil {
		return operation, err
	}
	if err := verifyExportBundle(ctx, stage, operation.Plan.Files, p.identity.Owner); err != nil {
		return operation, err
	}
	operation.State = "ready"
	if err := checkpoint(operation); err != nil {
		return operation, err
	}
	current, err := exportParent(plan)
	if err != nil {
		return operation, err
	}
	current.Close()
	if err := ctx.Err(); err != nil {
		return operation, err
	}
	operation.State = "publishing"
	if err := checkpoint(operation); err != nil {
		return operation, err
	}
	stageIdentity, err := durableIdentity(int(parent.Fd()), operation.StageName)
	if err != nil || stageIdentity != id {
		return operation, fmt.Errorf("staging bundle identity changed before publication")
	}
	if err := renameExclusive(int(parent.Fd()), operation.StageName, filepath.Base(plan.Destination)); err != nil {
		return operation, err
	}
	if err := parent.Sync(); err != nil {
		return operation, err
	}
	current, err = exportParent(plan)
	if err != nil {
		return operation, err
	}
	current.Close()
	publishedIdentity, err := durableIdentity(int(parent.Fd()), filepath.Base(plan.Destination))
	if err != nil || publishedIdentity != id {
		return operation, fmt.Errorf("published bundle identity cannot be verified")
	}
	// A persisted publication checkpoint may take time; reverify content after
	// rename rather than treating the earlier staging verification as current.
	if err := verifyExportBundle(ctx, stage, operation.Plan.Files, p.identity.Owner); err != nil {
		return operation, err
	}
	// Hashing can outlive a namespace change. The old parent descriptor alone
	// does not prove that the canonical destination still names this bundle.
	current, err = exportParent(plan)
	if err != nil {
		return operation, err
	}
	current.Close()
	publishedIdentity, err = durableIdentity(int(parent.Fd()), filepath.Base(plan.Destination))
	if err != nil || publishedIdentity != id {
		return operation, fmt.Errorf("published bundle changed during final verification")
	}
	operation.State = "published"
	operation.DestinationIdentity = &id
	operation.Detail = "Selected files copied into an independent operator-owned bundle; temporary source artifacts remain under their lease until ordinary release"
	operation.NextCommand = []string{"arm", "release", operation.Request.Lease}
	if err := checkpoint(operation); err != nil {
		return operation, err
	}
	return operation, nil
}
