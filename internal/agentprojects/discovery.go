// Package agentprojects discovers directory suggestions from client metadata.
// Discovery has no enrollment, messaging, process-control or resource authority.
package agentprojects

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
)

const maxBytes = 4 << 20
const maxProjects = 512

// Adapter is the extension point for supported client project catalogs.
// Implementations return only directory evidence, never conversations or IDs.
type Adapter interface {
	Name() string
	Discover(context.Context) Result
}
type Evidence struct {
	Adapter   string    `json:"adapter"`
	Kind      string    `json:"kind"`
	UpdatedAt time.Time `json:"metadata_updated_at"`
}
type Project struct {
	Directory string     `json:"directory"`
	Evidence  []Evidence `json:"evidence"`
}
type Result struct {
	Adapter  string    `json:"adapter"`
	Projects []Project `json:"projects,omitempty"`
	Warnings []string  `json:"warnings"`
}
type Catalog struct {
	Projects []Project `json:"projects"`
	Adapters []Result  `json:"adapters"`
	Activity string    `json:"activity"`
}

func Discover(ctx context.Context, adapters []Adapter) Catalog {
	out := Catalog{Projects: []Project{}, Adapters: []Result{}, Activity: "Client metadata suggests directories; selected workspaces and session markers do not prove running work."}
	byPath := map[string]int{}
	for _, adapter := range adapters {
		if ctx.Err() != nil {
			break
		}
		result := adapter.Discover(ctx)
		for _, project := range result.Projects {
			if index, ok := byPath[project.Directory]; ok {
				out.Projects[index].Evidence = append(out.Projects[index].Evidence, project.Evidence...)
			} else if len(out.Projects) < maxProjects {
				byPath[project.Directory] = len(out.Projects)
				out.Projects = append(out.Projects, project)
			} else {
				replaced := false
				if preferred(project) {
					for i, old := range out.Projects {
						if !preferred(old) {
							delete(byPath, old.Directory)
							out.Projects[i] = project
							byPath[project.Directory] = i
							replaced = true
							break
						}
					}
				}
				warning := "Project suggestion limit reached; use an explicit directory for other projects."
				if !slices.Contains(result.Warnings, warning) {
					result.Warnings = append(result.Warnings, warning)
				}
				if replaced {
					continue
				}
			}
		}
		// A single directory/evidence roster avoids copying private metadata into
		// both the combined roster and adapter diagnostics.
		result.Projects = nil
		out.Adapters = append(out.Adapters, result)
	}
	slices.SortFunc(out.Projects, func(a, b Project) int {
		rank := func(p Project) int {
			if preferred(p) {
				return 0
			}
			return 1
		}
		if d := rank(a) - rank(b); d != 0 {
			return d
		}
		return strings.Compare(a.Directory, b.Directory)
	})
	return out
}

func preferred(project Project) bool {
	for _, e := range project.Evidence {
		if e.Kind == "session-marker" || e.Kind == "selected-workspace" {
			return true
		}
	}
	return false
}

type scanner struct {
	result    Result
	remaining int
	ctx       context.Context
	paths     map[string]int
}

func newScanner(ctx context.Context, name string) *scanner {
	return &scanner{result: Result{Adapter: name, Projects: []Project{}, Warnings: []string{}}, remaining: maxBytes, ctx: ctx, paths: map[string]int{}}
}
func (s *scanner) warn(message string) {
	if len(s.result.Warnings) < 16 && !slices.Contains(s.result.Warnings, message) {
		s.result.Warnings = append(s.result.Warnings, message)
	}
}
func (s *scanner) read(path string, limit int) ([]byte, time.Time, error) {
	if e := s.ctx.Err(); e != nil {
		return nil, time.Time{}, e
	}
	limit = min(limit, s.remaining)
	if limit <= 0 {
		return nil, time.Time{}, fmt.Errorf("metadata budget reached")
	}
	r, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, time.Time{}, e
	}
	defer r.Close()
	f, e := r.OpenFile(filepath.Base(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, time.Time{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, time.Time{}, e
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return nil, time.Time{}, fmt.Errorf("metadata is not a bounded regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	s.remaining -= len(b)
	if e != nil || len(b) > limit {
		return nil, time.Time{}, fmt.Errorf("metadata could not be read within its budget")
	}
	after, e := f.Stat()
	if e != nil || info.Size() != after.Size() || info.ModTime() != after.ModTime() {
		return nil, time.Time{}, fmt.Errorf("metadata changed during discovery")
	}
	return b, info.ModTime().UTC(), s.ctx.Err()
}
func (s *scanner) add(path, kind string, at time.Time) {
	if s.ctx.Err() != nil {
		return
	}
	if !filepath.IsAbs(path) || len(path) > 4096 {
		s.warn("Some project paths were unsupported; select those directories manually.")
		return
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil {
		s.warn("Some recorded directories are missing or unavailable.")
		return
	}
	info, e := os.Stat(resolved)
	if e != nil || !info.IsDir() {
		s.warn("Some recorded directories are missing or unavailable.")
		return
	}
	evidence := Evidence{Adapter: s.result.Adapter, Kind: kind, UpdatedAt: at}
	if index, ok := s.paths[resolved]; ok {
		for i, old := range s.result.Projects[index].Evidence {
			if old.Kind == kind {
				if at.After(old.UpdatedAt) {
					s.result.Projects[index].Evidence[i] = evidence
				}
				return
			}
		}
		s.result.Projects[index].Evidence = append(s.result.Projects[index].Evidence, evidence)
		return
	}
	if len(s.result.Projects) >= maxProjects {
		s.warn("Project suggestion limit reached.")
		return
	}
	s.paths[resolved] = len(s.result.Projects)
	s.result.Projects = append(s.result.Projects, Project{Directory: resolved, Evidence: []Evidence{evidence}})
}

type Codex struct{ Home string }

func (Codex) Name() string { return "codex" }
func (c Codex) Discover(ctx context.Context) Result {
	s := newScanner(ctx, c.Name())
	if !filepath.IsAbs(c.Home) {
		s.warn("Codex metadata home must be absolute.")
		return s.result
	}
	// Optional desktop cache hints are version-dependent. Only known root arrays
	// are interpreted; selection is never labeled as live execution.
	b, at, e := s.read(filepath.Join(c.Home, ".codex-global-state.json"), 1<<20)
	if e == nil {
		var state struct {
			Saved  []string `json:"saved-workspace-roots"`
			Active []string `json:"active-workspace-roots"`
		}
		if json.Unmarshal(b, &state) != nil {
			s.warn("Codex saved-workspace metadata is unsupported; configured directories remain available.")
		} else {
			for _, path := range state.Active {
				s.add(path, "selected-workspace", at)
			}
			for _, path := range state.Saved {
				s.add(path, "saved-workspace", at)
			}
		}
	} else if !os.IsNotExist(e) {
		s.warn("Codex saved-workspace metadata is unavailable or exceeds the metadata limit.")
	}
	b, at, e = s.read(filepath.Join(c.Home, "config.toml"), 1<<20)
	if e == nil {
		var config struct{ Projects map[string]struct{} }
		if _, e = toml.Decode(string(b), &config); e != nil {
			s.warn("Codex project configuration could not be parsed; no configuration values are displayed.")
		} else {
			for _, path := range slices.Sorted(maps.Keys(config.Projects)) {
				s.add(path, "configured-project", at)
			}
		}
	} else if !os.IsNotExist(e) {
		s.warn("Codex project configuration is unavailable or exceeds the metadata limit.")
	}
	return s.result
}

type Claude struct{ Home, ConfigFile string }

func (Claude) Name() string { return "claude" }
func (c Claude) Discover(ctx context.Context) Result {
	s := newScanner(ctx, c.Name())
	if !filepath.IsAbs(c.Home) || c.ConfigFile != "" && !filepath.IsAbs(c.ConfigFile) {
		s.warn("Claude metadata paths must be absolute.")
		return s.result
	}
	config := c.ConfigFile
	if config == "" {
		config = filepath.Join(c.Home, ".claude.json")
	}
	b, at, e := s.read(config, 1<<20)
	var configured []string
	if e == nil {
		var state struct {
			Projects map[string]struct{} `json:"projects"`
		}
		if json.Unmarshal(b, &state) != nil {
			s.warn("Claude project configuration could not be parsed; no account or configuration values are displayed.")
		} else {
			configured = slices.Sorted(maps.Keys(state.Projects))
		}
	} else if !os.IsNotExist(e) {
		s.warn("Claude project configuration is unavailable or exceeds the metadata limit.")
	}
	s.claudeMarkers(c.Home)
	for _, path := range configured {
		s.add(path, "configured-project", at)
	}
	return s.result
}

func (s *scanner) claudeMarkers(home string) {
	// Read only small running-session markers, never transcripts/history. Crash
	// leftovers can survive, so marker presence is explicitly unverified activity.
	dir, e := os.OpenFile(filepath.Join(home, "sessions"), os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(e) {
		return
	}
	if e != nil {
		s.warn("Claude session markers are unavailable.")
		return
	}
	defer dir.Close()
	entries, e := dir.ReadDir(257)
	if e != nil && e != io.EOF {
		s.warn("Claude session markers could not be listed.")
		return
	}
	if len(entries) > 256 {
		s.warn("Claude session-marker limit reached; some directories may be omitted.")
		entries = entries[:256]
	}
	for _, entry := range entries {
		if s.ctx.Err() != nil {
			break
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		b, at, e := s.read(filepath.Join(home, "sessions", entry.Name()), 64<<10)
		if e != nil {
			s.warn("Some Claude session markers could not be read.")
			continue
		}
		var marker struct {
			CWD string `json:"cwd"`
		}
		if json.Unmarshal(b, &marker) != nil || marker.CWD == "" {
			s.warn("Some Claude session markers use an unsupported format.")
			continue
		}
		s.add(marker.CWD, "session-marker", at)
	}
}
