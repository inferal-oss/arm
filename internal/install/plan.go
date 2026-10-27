package install

import (
	"crypto/sha256"
	"fmt"
	"getarm.org/internal/local"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"getarm.org/internal/model"
)

// A Plan binds a read-only preview to the actual files, directory identities
// and manifest that were reviewed. Applying it still runs ordinary safeguards.
type Plan struct {
	Report   Report
	home     string
	targets  []Target
	remove   bool
	snapshot *snapshot
}
type pathState struct {
	Exists   bool
	Identity string
	Mode     os.FileMode
	Hash     string
}
type snapshot struct {
	dirs                      map[string]pathState
	files                     map[string]pathState
	backupID, backupOperation string
	changes                   map[string]FileChange
}

func PreviewInstall(home string, targets []Target) (*Plan, error) {
	return preview(home, targets, false)
}
func PreviewUninstall(home string) (*Plan, error) { return preview(home, nil, true) }
func preview(home string, targets []Target, remove bool) (*Plan, error) {
	manifest, err := fileState(home, "install.json")
	if err != nil {
		return nil, err
	}
	m, err := readManifest(home)
	if err != nil {
		return nil, err
	}
	if remove {
		for _, entry := range m.Entries {
			targets = append(targets, entry.Target)
		}
	}
	targets = append([]Target{}, targets...)
	seen := map[string]bool{}
	for _, target := range targets {
		path := filepath.Join(target.Root, target.Name)
		if (target.Name != "AGENTS.md" && target.Name != "CLAUDE.md") || seen[path] {
			return nil, model.Err("invalid_target", "integration targets must be distinct supported instruction files")
		}
		seen[path] = true
	}
	before, err := capture(home, targets)
	if err != nil {
		return nil, err
	}
	if before.files[filepath.Join(home, "install.json")] != manifest {
		return nil, model.Err("plan_changed", "manifest changed during preview; review a fresh preview")
	}
	var report Report
	if remove {
		report, err = Uninstall(home, true)
	} else {
		report, err = Install(home, targets, true)
	}
	if err != nil {
		return nil, err
	}
	if err = before.check(home, false); err != nil {
		return nil, err
	}
	before.changes = map[string]FileChange{}
	mutates := false
	for _, action := range report.Actions {
		if action.Action != "unchanged" && action.Action != "preserved" {
			mutates = true
		}
		if action.Change != nil {
			state := before.files[action.Path]
			if state.Exists != action.Change.BeforeExists || (state.Exists && state.Hash != action.Change.BeforeHash) {
				return nil, model.Err("plan_changed", "diff content changed during preview")
			}
			before.changes[action.Path] = *action.Change
		}
	}
	if mutates {
		before.backupID = local.ID()
		before.backupOperation = "install"
		if remove {
			before.backupOperation = "uninstall"
		}
		report.BackupDirectory = filepath.Join(home, "install-backups", before.backupID)
	}
	return &Plan{Report: report, home: home, targets: targets, remove: remove, snapshot: before}, nil
}
func (p *Plan) Check() error { return p.snapshot.check(p.home, false) }
func (p *Plan) Apply() (Report, error) {
	if p.remove {
		return uninstall(p.home, false, p.snapshot)
	}
	return install(p.home, p.targets, false, p.snapshot)
}
func capture(home string, targets []Target) (*snapshot, error) {
	s := &snapshot{dirs: map[string]pathState{}, files: map[string]pathState{}}
	targets = append(append([]Target{}, targets...), Target{Root: home, Name: "install.json"})
	for _, target := range targets {
		root := filepath.Clean(target.Root)
		if !filepath.IsAbs(root) || filepath.Base(target.Name) != target.Name {
			return nil, model.Err("invalid_target", "approval targets must be absolute roots with file names")
		}
		for dir := root; ; dir = filepath.Dir(dir) {
			state, err := directoryState(dir)
			if err != nil {
				return nil, err
			}
			s.dirs[dir] = state
			if state.Exists {
				break
			}
			if filepath.Dir(dir) == dir {
				return nil, fmt.Errorf("no existing ancestor for integration root")
			}
		}
		state, err := fileState(root, target.Name)
		if err != nil {
			return nil, err
		}
		s.files[filepath.Join(root, target.Name)] = state
	}
	return s, nil
}
func directoryState(path string) (pathState, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return pathState{}, nil
	}
	if err != nil {
		return pathState{}, err
	}
	target, err := os.Stat(path)
	if err != nil {
		return pathState{}, err
	}
	if !target.IsDir() {
		return pathState{}, model.Err("unsafe_path", "integration root or ancestor is not a directory")
	}
	return pathState{Exists: true, Identity: fileIdentity(info) + "/" + fileIdentity(target), Mode: info.Mode()}, nil
}
func fileState(root, name string) (pathState, error) {
	r, err := os.OpenRoot(root)
	if os.IsNotExist(err) {
		return pathState{}, nil
	}
	if err != nil {
		return pathState{}, err
	}
	defer r.Close()
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return pathState{}, nil
	}
	if err != nil {
		return pathState{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return pathState{}, err
	}
	if !before.Mode().IsRegular() {
		return pathState{}, model.Err("unsafe_path", "instruction/manifest target must be a regular file")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, f); err != nil {
		return pathState{}, err
	}
	after, err := f.Stat()
	if err != nil {
		return pathState{}, err
	}
	if fileIdentity(before) != fileIdentity(after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() || before.Mode() != after.Mode() {
		return pathState{}, model.Err("plan_changed", "integration file changed during preview; review a fresh preview")
	}
	return pathState{Exists: true, Identity: fileIdentity(after), Mode: after.Mode(), Hash: fmt.Sprintf("%x", hash.Sum(nil))}, nil
}
func (s *snapshot) check(home string, allowHomeCreation bool) error {
	for path, want := range s.dirs {
		got, err := directoryState(path)
		if err != nil {
			return err
		}
		// local.Lock creates the state directory only after approval. Normalize
		// just those originally absent directories on that exact creation path.
		rel, relErr := filepath.Rel(path, home)
		ancestor := relErr == nil && rel != ".." && !filepath.IsAbs(rel) && (rel == "." || !startsParent(rel))
		if allowHomeCreation && !want.Exists && ancestor && got.Exists && got.Mode.IsDir() {
			continue
		}
		if got != want {
			return model.Err("plan_changed", "integration directory changed after preview; review a fresh preview")
		}
	}
	for path, want := range s.files {
		got, err := fileState(filepath.Dir(path), filepath.Base(path))
		if err != nil {
			return err
		}
		if got != want {
			return model.Err("plan_changed", "integration file or manifest changed after preview; review a fresh preview")
		}
	}
	return nil
}
func startsParent(path string) bool {
	return len(path) > 3 && path[:3] == ".."+string(filepath.Separator)
}
