package agentprojects

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/agentsessions"
	"getarm.org/internal/gitmeta"
)

type hierarchyFixtureSource struct {
	result agentsessions.SavedSessionResult
	action func(context.Context, agentsessions.SavedSessionRequest)
}

func (s hierarchyFixtureSource) DiscoverSavedSessions(ctx context.Context, r agentsessions.SavedSessionRequest) agentsessions.SavedSessionResult {
	if s.action != nil {
		s.action(ctx, r)
	}
	return s.result
}

type hierarchyFixtureProbe struct {
	infos  map[string]gitmeta.Info
	calls  []string
	action func()
}

func (p *hierarchyFixtureProbe) Resolve(path string) (gitmeta.Info, error) {
	p.calls = append(p.calls, path)
	if p.action != nil {
		p.action()
	}
	if info, ok := p.infos[path]; ok {
		return info, nil
	}
	return gitmeta.Info{}, gitmeta.ErrUnsupported
}
func hierarchyFixtureDir(t *testing.T) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return path
}
func hierarchyFixtureChild(adapter, home, cwd string, n int) agentsessions.SavedSession {
	return agentsessions.SavedSession{Ref: agentsessions.SavedSessionRef{Adapter: adapter, Home: home, ID: fmt.Sprintf("%08x-0000-4000-8000-000000000000", n)}, Directory: cwd, Name: fmt.Sprintf("Chat %d", n)}
}
func hierarchyFixtureRun(t *testing.T, catalog Catalog, sources []HierarchySource, probe *hierarchyFixtureProbe) Hierarchy {
	t.Helper()
	budget := gitmeta.NewBudget(t.Context())
	defer budget.Close()
	return discoverHierarchy(t.Context(), budget, catalog, sources, probe)
}

func TestHierarchyVerifiedGitRootsAndExactDirectoryFallback(t *testing.T) {
	primary, linked, common, linkedGit, plain := hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	cwd := filepath.Join(linked, "subdir")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	git := gitmeta.Directory{Path: common, Identity: "owned-common"}
	probe := &hierarchyFixtureProbe{infos: map[string]gitmeta.Info{primary: {Root: gitmeta.Directory{Path: primary, Identity: "owned-primary"}, Git: git, Common: git}, cwd: {Root: gitmeta.Directory{Path: linked, Identity: "owned-linked"}, Git: gitmeta.Directory{Path: linkedGit, Identity: "owned-linked-git"}, Common: git}}}
	codexHome, claudeHome := hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	requestChecked := false
	sources := []HierarchySource{{Adapter: "codex", Home: codexHome, Source: hierarchyFixtureSource{result: agentsessions.SavedSessionResult{Sessions: []agentsessions.SavedSession{hierarchyFixtureChild("codex", codexHome, cwd, 1)}}, action: func(ctx context.Context, r agentsessions.SavedSessionRequest) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 15*time.Second || r.Scope != agentsessions.SavedWholeClient || len(r.Directories) != 0 {
			t.Fatal("implicit or unbounded source scope", r)
		}
		requestChecked = true
	}}}, {Adapter: "claude", Home: claudeHome, Source: hierarchyFixtureSource{result: agentsessions.SavedSessionResult{Sessions: []agentsessions.SavedSession{hierarchyFixtureChild("claude", claudeHome, primary, 1)}, Warnings: []string{"Local markers only"}}}}}
	out := hierarchyFixtureRun(t, Catalog{Projects: []Project{{Directory: primary}, {Directory: plain}}}, sources, probe)
	if !requestChecked || len(out.Groups) != 2 || len(probe.calls) != 3 {
		t.Fatal(out, probe.calls)
	}
	var group *Group
	for i := range out.Groups {
		if out.Groups[i].Kind == "git" {
			group = &out.Groups[i]
		} else if out.Groups[i].Directory != plain || len(out.Groups[i].Directories) != 1 {
			t.Fatal(out.Groups[i])
		}
	}
	if group == nil || group.Directory != primary || group.Name != filepath.Base(primary) || len(group.Sessions) != 2 || len(group.Warnings) != 0 || !slices.Contains(group.Directories, linked) || !slices.Contains(group.Directories, cwd) || !slices.Contains(group.Directories, primary) {
		t.Fatal(group)
	}
	if len(out.Coverage) != 2 || !slices.Contains(out.Coverage[1].Warnings, "Local markers only") {
		t.Fatal(out.Coverage)
	}
}

func TestHierarchyUnknownPrimaryDoesNotTrimCommonPath(t *testing.T) {
	linked, common, gitDir, home := hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	probe := &hierarchyFixtureProbe{infos: map[string]gitmeta.Info{linked: {Root: gitmeta.Directory{Path: linked, Identity: "owned-root"}, Git: gitmeta.Directory{Path: gitDir, Identity: "owned-git"}, Common: gitmeta.Directory{Path: common, Identity: "owned-common"}}}}
	out := hierarchyFixtureRun(t, Catalog{}, []HierarchySource{{Adapter: "codex", Home: home, Source: hierarchyFixtureSource{result: agentsessions.SavedSessionResult{Sessions: []agentsessions.SavedSession{hierarchyFixtureChild("codex", home, linked, 1)}}}}}, probe)
	if len(out.Groups) != 1 || out.Groups[0].Kind != "git" || out.Groups[0].Directory != linked || len(out.Groups[0].Warnings) != 1 || slices.Contains(out.Groups[0].Directories, common) {
		t.Fatal(out)
	}
}

func TestHierarchyCombinedChildLimitPreservesAdapterCoverage(t *testing.T) {
	cwd, codexHome, claudeHome := hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	var codex []agentsessions.SavedSession
	for i := 0; i < 512; i++ {
		codex = append(codex, hierarchyFixtureChild("codex", codexHome, cwd, i))
	}
	out := hierarchyFixtureRun(t, Catalog{}, []HierarchySource{{Adapter: "codex", Home: codexHome, Source: hierarchyFixtureSource{result: agentsessions.SavedSessionResult{Sessions: codex}}}, {Adapter: "claude", Home: claudeHome, Source: hierarchyFixtureSource{result: agentsessions.SavedSessionResult{Sessions: []agentsessions.SavedSession{hierarchyFixtureChild("claude", claudeHome, cwd, 1)}, Warnings: []string{"Local markers only"}}}}}, &hierarchyFixtureProbe{})
	if len(out.Groups) != 1 || len(out.Groups[0].Sessions) != 512 || !out.Truncated || len(out.Coverage) != 2 || !out.Coverage[1].Truncated || len(out.Coverage[1].Warnings) < 2 {
		t.Fatal(out)
	}
}

func TestHierarchyGitRootsCountTowardDirectoryAndGroupLimits(t *testing.T) {
	base, home, extraRoot, common := hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	catalog := Catalog{}
	for i := 0; i < 512; i++ {
		path := filepath.Join(base, fmt.Sprintf("%04d", i))
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		catalog.Projects = append(catalog.Projects, Project{Directory: path})
	}
	first, last := catalog.Projects[0].Directory, catalog.Projects[511].Directory
	git := gitmeta.Directory{Path: common, Identity: "owned-common"}
	probe := &hierarchyFixtureProbe{infos: map[string]gitmeta.Info{first: {Root: gitmeta.Directory{Path: extraRoot, Identity: "owned-extra"}, Git: git, Common: git}}}
	out := hierarchyFixtureRun(t, catalog, []HierarchySource{{Adapter: "claude", Home: home, Source: hierarchyFixtureSource{result: agentsessions.SavedSessionResult{Sessions: []agentsessions.SavedSession{hierarchyFixtureChild("claude", home, last, 1)}}}}}, probe)
	paths := map[string]bool{}
	for _, group := range out.Groups {
		for _, path := range group.Directories {
			paths[path] = true
		}
	}
	if len(out.Groups) != 128 || len(paths) > 512 || paths[extraRoot] || len(probe.calls) != 64 || !out.Truncated || len(out.Coverage) != 1 || !out.Coverage[0].Truncated || len(out.Coverage[0].Warnings) == 0 {
		t.Fatal(len(out.Groups), len(paths), len(probe.calls), out.Coverage)
	}
	for _, group := range out.Groups {
		if group.Directory == first && group.Kind != "directory" {
			t.Fatal("unrepresentable Git root was admitted", group)
		}
	}
}

func TestHierarchyTextAndLegacyNameBounds(t *testing.T) {
	cwd, home := hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	var names []string
	for i := 0; i < 200; i++ {
		names = append(names, strings.Repeat("x", 64<<10))
	}
	child := hierarchyFixtureChild("codex", home, cwd, 1)
	child.Name = strings.Repeat("x", (64<<10)+1)
	out := hierarchyFixtureRun(t, Catalog{Projects: []Project{{Directory: cwd, Names: names}}}, []HierarchySource{{Adapter: "codex", Home: home, Source: hierarchyFixtureSource{result: agentsessions.SavedSessionResult{Sessions: []agentsessions.SavedSession{child}}}}}, &hierarchyFixtureProbe{})
	if !out.Truncated || len(out.Groups) != 1 || len(out.Groups[0].Sessions) != 0 || len(out.Coverage[0].Warnings) == 0 {
		t.Fatal(out)
	}
	var many []string
	for i := 0; i < 512; i++ {
		many = append(many, fmt.Sprintf("Name%d", i))
	}
	other := hierarchyFixtureDir(t)
	out = hierarchyFixtureRun(t, Catalog{Projects: []Project{{Directory: cwd, Names: many}, {Directory: other, Names: many}}}, nil, &hierarchyFixtureProbe{})
	count := 0
	for _, group := range out.Groups {
		count += len(group.Names)
	}
	if count > 512 || !out.Truncated {
		t.Fatal(count, out)
	}
}

func TestHierarchyReferenceConflictsContextAndCopiedDisplay(t *testing.T) {
	cwd, other, home, otherHome := hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	first := hierarchyFixtureChild("codex", home, cwd, 1)
	conflict := first
	conflict.Directory = other
	good := hierarchyFixtureChild("codex", home, cwd, 2)
	good.Name = "Saved\x1b[31m\u202ename"
	wrong := hierarchyFixtureChild("codex", otherHome, cwd, 3)
	input := []agentsessions.SavedSession{first, conflict, good, wrong}
	result := agentsessions.SavedSessionResult{Sessions: input, Warnings: []string{"Coverage\x1b"}}
	out := hierarchyFixtureRun(t, Catalog{}, []HierarchySource{{Adapter: "codex", Home: home, Source: hierarchyFixtureSource{result: result}}}, &hierarchyFixtureProbe{})
	count := 0
	for _, group := range out.Groups {
		count += len(group.Sessions)
		for _, child := range group.Sessions {
			if child.Ref.ID != good.Ref.ID || strings.ContainsAny(child.Name, "\x1b\u202e") {
				t.Fatal(child)
			}
		}
	}
	input[2].Name = "Changed later"
	result.Warnings[0] = "Changed later"
	if count != 1 || len(out.Coverage[0].Warnings) < 2 {
		t.Fatal(out)
	}
	for _, group := range out.Groups {
		for _, child := range group.Sessions {
			if child.Name == "Changed later" {
				t.Fatal("aliased caller slice")
			}
		}
	}
}

func TestHierarchyCanceledSnapshotAndSourceBounds(t *testing.T) {
	cwd, home := hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	budget := gitmeta.NewBudget(ctx)
	defer budget.Close()
	out := discoverHierarchy(ctx, budget, Catalog{Projects: []Project{{Directory: cwd}}}, []HierarchySource{{Adapter: "codex", Home: home, Source: hierarchyFixtureSource{action: func(context.Context, agentsessions.SavedSessionRequest) { cancel() }}}}, &hierarchyFixtureProbe{})
	if len(out.Groups) != 0 || len(out.Warnings) == 0 {
		t.Fatal(out)
	}
	for _, mode := range []string{"sessions", "warnings", "diagnostic"} {
		t.Run(mode, func(t *testing.T) {
			result := agentsessions.SavedSessionResult{}
			switch mode {
			case "sessions":
				result.Sessions = make([]agentsessions.SavedSession, 513)
			case "warnings":
				result.Warnings = make([]string, 17)
			case "diagnostic":
				result.Warnings = []string{strings.Repeat("x", 1025)}
			}
			out := hierarchyFixtureRun(t, Catalog{Projects: []Project{{Directory: cwd}}}, []HierarchySource{{Adapter: "claude", Home: home, Source: hierarchyFixtureSource{result: result}}}, &hierarchyFixtureProbe{})
			if !out.Truncated || len(out.Coverage) != 1 || !out.Coverage[0].Truncated || len(out.Coverage[0].Warnings) == 0 {
				t.Fatal(out)
			}
		})
	}
}

func TestHierarchyConflictingGitIdentitiesFallBackTogether(t *testing.T) {
	a, b, common := hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	first := gitmeta.Directory{Path: common, Identity: "owned-first"}
	second := gitmeta.Directory{Path: common, Identity: "owned-replaced"}
	probe := &hierarchyFixtureProbe{infos: map[string]gitmeta.Info{a: {Root: gitmeta.Directory{Path: a, Identity: "owned-a"}, Git: first, Common: first}, b: {Root: gitmeta.Directory{Path: b, Identity: "owned-b"}, Git: second, Common: second}}}
	out := hierarchyFixtureRun(t, Catalog{Projects: []Project{{Directory: a}, {Directory: b}}}, nil, probe)
	if len(out.Groups) != 2 || len(out.Warnings) == 0 {
		t.Fatal(out)
	}
	for _, group := range out.Groups {
		if group.Kind != "directory" || len(group.Directories) != 1 {
			t.Fatal(group)
		}
	}
}

func TestHierarchyFinalCancellationDiscardsBuiltSnapshot(t *testing.T) {
	cwd, home := hierarchyFixtureDir(t), hierarchyFixtureDir(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	budget := gitmeta.NewBudget(ctx)
	defer budget.Close()
	built := false
	hooks := hierarchyHooks{beforeReturn: func(snapshot Hierarchy) {
		built = len(snapshot.Groups) == 1 && len(snapshot.Groups[0].Sessions) == 1
		cancel()
	}}
	sources := []HierarchySource{{Adapter: "codex", Home: home, Source: hierarchyFixtureSource{result: agentsessions.SavedSessionResult{Sessions: []agentsessions.SavedSession{hierarchyFixtureChild("codex", home, cwd, 1)}}}}}
	out := discoverHierarchy(ctx, budget, Catalog{}, sources, &hierarchyFixtureProbe{}, hooks)
	if !built || len(out.Groups) != 0 || len(out.Coverage) != 1 || !out.Coverage[0].Truncated || len(out.Coverage[0].Warnings) == 0 || !slices.Contains(out.Warnings, "Hierarchy discovery was canceled; its snapshot was discarded.") {
		t.Fatal(built, out)
	}
}

func TestHierarchyChangedGitRelationshipsFallBackTogether(t *testing.T) {
	for _, mode := range []string{"root-common", "git-common", "multiple-primary"} {
		t.Run(mode, func(t *testing.T) {
			root, gitA, gitB, commonA, commonB := hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t), hierarchyFixtureDir(t)
			a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
			for _, path := range []string{a, b} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			first := gitmeta.Info{Root: gitmeta.Directory{Path: root, Identity: "owned-root"}, Git: gitmeta.Directory{Path: gitA, Identity: "owned-git-a"}, Common: gitmeta.Directory{Path: commonA, Identity: "owned-common-a"}}
			second := gitmeta.Info{Root: first.Root, Git: gitmeta.Directory{Path: gitB, Identity: "owned-git-b"}, Common: gitmeta.Directory{Path: commonB, Identity: "owned-common-b"}}
			if mode != "root-common" {
				first.Root = gitmeta.Directory{Path: a, Identity: "owned-a"}
				second.Root = gitmeta.Directory{Path: b, Identity: "owned-b"}
				second.Git = first.Git
			}
			if mode == "multiple-primary" {
				first.Git = first.Common
				second.Git, second.Common = first.Common, first.Common
			}
			probe := &hierarchyFixtureProbe{infos: map[string]gitmeta.Info{a: first, b: second}}
			out := hierarchyFixtureRun(t, Catalog{Projects: []Project{{Directory: a}, {Directory: b}}}, nil, probe)
			if len(out.Groups) != 2 || len(out.Warnings) == 0 {
				t.Fatal(out)
			}
			for _, group := range out.Groups {
				if group.Kind != "directory" || len(group.Directories) != 1 || (group.Directory != a && group.Directory != b) {
					t.Fatal("inconsistent relationship retained a verified Git group", group)
				}
			}
		})
	}
}
