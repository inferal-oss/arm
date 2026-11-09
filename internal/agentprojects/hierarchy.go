package agentprojects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"getarm.org/internal/agentsessions"
	"getarm.org/internal/gitmeta"
)

const MaxHierarchyGroups = 128
const MaxHierarchyDirectories = 512
const MaxHierarchyTextBytes = 8 << 20

type HierarchySource struct {
	Adapter string
	Home    string
	Source  agentsessions.SavedSessionSource
}

type HierarchyCoverage struct {
	Adapter   string   `json:"adapter"`
	Warnings  []string `json:"warnings"`
	Truncated bool     `json:"truncated,omitempty"`
}

// Hierarchy is an additive display snapshot, separate from the legacy catalog.
// It grants no native Project, policy binding, enrollment or delivery authority.
type Hierarchy struct {
	Version   int                 `json:"version"`
	Groups    []Group             `json:"groups"`
	Coverage  []HierarchyCoverage `json:"coverage"`
	Warnings  []string            `json:"warnings"`
	Truncated bool                `json:"truncated,omitempty"`
}

type hierarchyProbe interface {
	Resolve(string) (gitmeta.Info, error)
}

type hierarchyHooks struct {
	beforeReturn func(Hierarchy)
}

func hierarchyPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && len(path) <= 4096 && !strings.ContainsFunc(path, func(r rune) bool { return r < 32 || r == 127 })
}

// InspectDirectory records canonical filesystem evidence, not an ARM identity.
func InspectDirectory(ctx context.Context, path string) (gitmeta.Directory, error) {
	if err := ctx.Err(); err != nil {
		return gitmeta.Directory{}, err
	}
	if !hierarchyPath(path) {
		return gitmeta.Directory{}, fmt.Errorf("unsupported hierarchy directory")
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return gitmeta.Directory{}, fmt.Errorf("noncanonical hierarchy directory")
	}
	if err := ctx.Err(); err != nil {
		return gitmeta.Directory{}, err
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return gitmeta.Directory{}, fmt.Errorf("hierarchy directory unavailable")
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return gitmeta.Directory{}, fmt.Errorf("unsupported directory identity")
	}
	if err := ctx.Err(); err != nil {
		return gitmeta.Directory{}, err
	}
	return gitmeta.Directory{Path: canonical, Identity: fmt.Sprintf("%d:%d", identity.Dev, identity.Ino)}, nil
}

// DiscoverHierarchy bounds all adapters together. One caller-respecting budget
// covers metadata and the pass-local Git cache. No filesystem enumeration or
// implicit native project association is used to widen the supplied catalog.
func DiscoverHierarchy(ctx context.Context, catalog Catalog, sources []HierarchySource) Hierarchy {
	budget := gitmeta.NewBudget(ctx)
	defer budget.Close()
	return discoverHierarchy(ctx, budget, catalog, sources, nil)
}

func discoverHierarchy(parent context.Context, budget *gitmeta.Budget, catalog Catalog, sources []HierarchySource, probe hierarchyProbe, hooks ...hierarchyHooks) (out Hierarchy) {
	out = Hierarchy{Version: 1, Groups: []Group{}, Coverage: []HierarchyCoverage{}, Warnings: []string{}}
	warn := func(message string) {
		if len(out.Warnings) < 16 && !slices.Contains(out.Warnings, message) {
			out.Warnings = append(out.Warnings, message)
		}
	}
	defer func() {
		if len(hooks) > 0 && hooks[0].beforeReturn != nil {
			hooks[0].beforeReturn(out)
		}
		// Caller cancellation discards even a fully constructed result. Budget
		// exhaustion alone still permits the documented directory fallback.
		if parent.Err() != nil {
			out.Groups = []Group{}
			warn("Hierarchy discovery was canceled; its snapshot was discarded.")
			for index := range out.Coverage {
				coverage := &out.Coverage[index]
				coverage.Truncated = true
				if len(coverage.Warnings) < 16 {
					coverage.Warnings = append(coverage.Warnings, "This adapter's hierarchy snapshot was discarded after caller cancellation.")
				}
			}
		}
	}()
	if len(catalog.Projects) > MaxHierarchyDirectories || len(sources) > 2 {
		warn("Hierarchy input exceeds its supported catalog bounds; use explicit directories.")
		out.Truncated = true
		return out
	}
	remaining := MaxHierarchyTextBytes
	charge := func(values ...string) bool {
		n := 0
		for _, value := range values {
			if len(value) > remaining-n {
				return false
			}
			n += len(value)
		}
		remaining -= n
		return true
	}
	paths := map[string]bool{}
	pathBudget := map[string]bool{}
	members := map[string]gitmeta.Directory{}
	legacyNames := map[string][]string{}
	savedWorkspaces := map[string]bool{}
	legacyNameCount := 0
	addPath := func(path string) bool {
		if paths[path] {
			return true
		}
		if !pathBudget[path] && (len(pathBudget) >= MaxHierarchyDirectories || !charge(path)) {
			out.Truncated = true
			warn("Hierarchy directory or text bound reached; other display suggestions were omitted.")
			return false
		}
		paths[path] = true
		pathBudget[path] = true
		return true
	}
	for _, project := range catalog.Projects {
		if budget.Check() != nil {
			break
		}
		member, err := InspectDirectory(budget.Context(), project.Directory)
		if err != nil {
			warn("Some configured directories are unavailable or noncanonical and were omitted.")
			continue
		}
		if len(project.Names) > agentsessions.Limit {
			out.Truncated = true
			warn("Some configured display names exceed hierarchy bounds and were omitted.")
			continue
		}
		if !addPath(project.Directory) {
			continue
		}
		members[project.Directory] = member
		for _, evidence := range project.Evidence {
			if evidence.Adapter == "codex" && evidence.Kind == "saved-workspace" {
				savedWorkspaces[project.Directory] = true
				break
			}
		}
		for _, name := range project.Names {
			if legacyNameCount >= agentsessions.Limit || len(name) > 64<<10 || !charge(name) {
				out.Truncated = true
				warn("Some configured display names exceed hierarchy bounds and were omitted.")
				break
			}
			if clean := SanitizeTitle(name); clean != "" && !slices.Contains(legacyNames[project.Directory], clean) {
				legacyNames[project.Directory] = append(legacyNames[project.Directory], clean)
				legacyNameCount++
			}
		}
	}
	configuredPaths := map[string]bool{}
	for path := range paths {
		configuredPaths[path] = true
	}
	var children []agentsessions.SavedSession
	seen := map[agentsessions.SavedSessionRef]string{}
	ambiguous := map[agentsessions.SavedSessionRef]bool{}
	adapters := map[string]bool{}
	for _, source := range sources {
		coverage := HierarchyCoverage{Adapter: source.Adapter, Warnings: []string{}}
		cw := func(message string) {
			if len(coverage.Warnings) < 16 && !slices.Contains(coverage.Warnings, message) {
				coverage.Warnings = append(coverage.Warnings, message)
			}
		}
		if (source.Adapter != "codex" && source.Adapter != "claude") || adapters[source.Adapter] || source.Source == nil || !charge(source.Adapter, source.Home) {
			warn("A hierarchy source is unsupported, duplicated or exceeds metadata bounds.")
			out.Truncated = true
			continue
		}
		adapters[source.Adapter] = true
		if budget.Check() != nil {
			cw("This adapter's saved-chat coverage is unavailable because discovery was interrupted.")
			out.Coverage = append(out.Coverage, coverage)
			continue
		}
		homeEvidence, err := InspectDirectory(budget.Context(), source.Home)
		home := homeEvidence.Path
		if err != nil || budget.Check() != nil {
			cw("This adapter's saved-chat coverage is unavailable.")
			out.Coverage = append(out.Coverage, coverage)
			continue
		}
		result := source.Source.DiscoverSavedSessions(budget.Context(), agentsessions.SavedSessionRequest{Scope: agentsessions.SavedWholeClient})
		coverage.Truncated = result.Truncated
		out.Truncated = out.Truncated || result.Truncated
		if len(result.Warnings) > 16 || len(result.Sessions) > agentsessions.Limit {
			cw("This adapter's saved-chat result exceeds supported bounds and was omitted.")
			coverage.Truncated = true
			out.Truncated = true
			out.Coverage = append(out.Coverage, coverage)
			continue
		}
		for _, warning := range result.Warnings {
			if len(warning) > 1024 || !charge(warning) {
				cw("Some adapter diagnostics exceeded supported display bounds.")
				coverage.Truncated = true
				out.Truncated = true
				break
			}
			cw(SanitizeTitle(warning))
		}
		if budget.Check() != nil {
			cw("This adapter's saved-chat discovery was interrupted; its chats were omitted.")
			out.Coverage = append(out.Coverage, coverage)
			continue
		}
		for _, child := range result.Sessions {
			if budget.Check() != nil {
				cw("This adapter's remaining chats were omitted because discovery was interrupted.")
				coverage.Truncated = true
				out.Truncated = true
				break
			}
			if !child.Valid() || child.Ref.Adapter != source.Adapter || child.Ref.Home != home {
				cw("Some saved-chat references exceed supported metadata bounds or source context and were omitted.")
				coverage.Truncated = true
				out.Truncated = true
				continue
			}
			previous, exists := seen[child.Ref]
			seen[child.Ref] = child.Directory
			if exists && previous != child.Directory {
				ambiguous[child.Ref] = true
				cw("Conflicting saved-chat directories were omitted.")
			}
			if exists || ambiguous[child.Ref] {
				continue
			}
			if len(children) >= agentsessions.Limit {
				cw("The combined chat limit was reached; some of this adapter's chats were omitted.")
				coverage.Truncated = true
				out.Truncated = true
				continue
			}
			if len(child.Name) > 64<<10 || !charge(child.Ref.Adapter, child.Ref.Home, child.Ref.ID, child.Directory, child.Name) {
				cw("Some saved-chat references exceed supported text bounds and were omitted.")
				coverage.Truncated = true
				out.Truncated = true
				continue
			}
			member, memberErr := InspectDirectory(budget.Context(), child.Directory)
			if memberErr != nil || budget.Check() != nil {
				cw("Some saved-chat directories are unavailable or noncanonical and were omitted.")
				continue
			}
			if !addPath(child.Directory) {
				coverage.Truncated = true
				continue
			}
			members[child.Directory] = member
			child.Name = SanitizeTitle(child.Name)
			children = append(children, child)
		}
		out.Coverage = append(out.Coverage, coverage)
	}
	children = slices.DeleteFunc(children, func(child agentsessions.SavedSession) bool { return ambiguous[child.Ref] })
	// An association rejected as ambiguous must not leave its first cwd behind
	// as an otherwise unsupported project-only suggestion. Existing configured
	// directories and other independently accepted children retain their paths.
	paths = configuredPaths
	for _, child := range children {
		paths[child.Directory] = true
	}
	if parent.Err() != nil {
		warn("Hierarchy discovery was canceled; its snapshot was discarded.")
		return out
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	slices.Sort(ordered)
	if probe == nil && budget.Check() == nil {
		probe, _ = gitmeta.NewResolver(budget)
	}
	// Only this pass's exact canonical paths are probed/cached. Added Git roots
	// count toward the same path bound, and are never recursively probed.
	// Spend the probe budget on saved project folders before session-only paths.
	infoByPath := map[string]gitmeta.Info{}
	probeOrder := slices.Clone(ordered)
	slices.SortStableFunc(probeOrder, func(a, b string) int {
		if savedWorkspaces[a] == savedWorkspaces[b] {
			return 0
		}
		if savedWorkspaces[a] {
			return -1
		}
		return 1
	})
	for attempt, path := range probeOrder {
		if attempt >= gitmeta.MaxProbes || probe == nil || budget.Check() != nil {
			warn("Git grouping is unavailable or exhausted; exact directory groups are shown where needed.")
			break
		}
		info, err := probe.Resolve(path)
		if err != nil {
			warn("Some Git grouping is unavailable; exact directory groups are shown where needed.")
			continue
		}
		if !hierarchyPath(info.Root.Path) || info.Root.Identity == "" || !hierarchyPath(info.Git.Path) || info.Git.Identity == "" || !hierarchyPath(info.Common.Path) || info.Common.Identity == "" {
			warn("Unsupported Git grouping evidence was omitted.")
			continue
		}
		newPaths := map[string]bool{}
		for _, directory := range []gitmeta.Directory{info.Root, info.Git, info.Common} {
			if !pathBudget[directory.Path] {
				newPaths[directory.Path] = true
			}
		}
		if len(pathBudget)+len(newPaths) > MaxHierarchyDirectories || !charge(info.Root.Path, info.Root.Identity, info.Git.Path, info.Git.Identity, info.Common.Path, info.Common.Identity) {
			out.Truncated = true
			warn("Checkout evidence exceeds combined directory or text bounds; exact directory groups are shown instead.")
			continue
		}
		for path := range newPaths {
			pathBudget[path] = true
		}
		if !addPath(info.Root.Path) {
			continue
		}
		infoByPath[path] = info
	}
	identities := map[string]string{}
	changed := map[string]bool{}
	rootRelationships := map[string]string{}
	gitRelationships := map[string]string{}
	primaryRelationships := map[string]string{}
	changedRoots := map[string]bool{}
	changedGit := map[string]bool{}
	ambiguousPrimary := map[string]bool{}
	tuple := func(directory gitmeta.Directory) string { return directory.Path + "\x00" + directory.Identity }
	for _, info := range infoByPath {
		for _, directory := range []gitmeta.Directory{info.Root, info.Git, info.Common} {
			if previous, exists := identities[directory.Path]; exists && previous != directory.Identity {
				changed[directory.Path] = true
			}
			identities[directory.Path] = directory.Identity
		}
		root, git, common := tuple(info.Root), tuple(info.Git), tuple(info.Common)
		relationship := git + "\x00" + common
		if previous, exists := rootRelationships[root]; exists && previous != relationship {
			changedRoots[root] = true
		}
		rootRelationships[root] = relationship
		if previous, exists := gitRelationships[git]; exists && previous != common {
			changedGit[git] = true
		}
		gitRelationships[git] = common
		if info.Primary() {
			if previous, exists := primaryRelationships[common]; exists && previous != root {
				ambiguousPrimary[common] = true
			}
			primaryRelationships[common] = root
		}
	}
	for path, info := range infoByPath {
		if changed[info.Root.Path] || changed[info.Git.Path] || changed[info.Common.Path] || changedRoots[tuple(info.Root)] || changedGit[tuple(info.Git)] || ambiguousPrimary[tuple(info.Common)] {
			delete(infoByPath, path)
			warn("Conflicting Git identities, relationships or primary checkouts were omitted; exact directory groups are shown instead.")
		}
	}
	if parent.Err() != nil {
		warn("Hierarchy discovery was canceled; its snapshot was discarded.")
		return out
	}
	byKey := map[string]int{}
	pathGroup := map[string]string{}
	primary := map[string]string{}
	savedCheckout := map[string]string{}
	for _, path := range ordered {
		info, ok := infoByPath[path]
		if !ok {
			continue
		}
		key := "git\x00" + info.Common.Path + "\x00" + info.Common.Identity
		if info.Primary() {
			primary[key] = info.Root.Path
		}
		if savedWorkspaces[path] && savedCheckout[key] == "" {
			savedCheckout[key] = info.Root.Path
		}
		pathGroup[path] = key
	}
	for _, path := range ordered {
		key := pathGroup[path]
		kind := "git"
		directory := path
		if key == "" {
			key = "directory\x00" + path
			kind = "directory"
		} else {
			directory = infoByPath[path].Root.Path
			if primary[key] != "" {
				directory = primary[key]
			} else if savedCheckout[key] != "" {
				directory = savedCheckout[key]
			}
		}
		index, exists := byKey[key]
		if !exists {
			if len(out.Groups) >= MaxHierarchyGroups {
				out.Truncated = true
				warn("Hierarchy project limit reached; other groups and their chats were omitted.")
				continue
			}
			digest := sha256.Sum256([]byte(key))
			label := SanitizeTitle(filepath.Base(directory))
			if kind == "git" && primary[key] == "" {
				label = CheckoutName(infoByPath[path])
			}
			if len(label) > 1024 {
				label = string([]rune(label)[:255])
			}
			group := Group{Key: hex.EncodeToString(digest[:]), Kind: kind, Name: label, Directory: directory, Directories: []string{directory}, Sessions: []agentsessions.SavedSession{}}
			if kind == "git" && primary[key] == "" {
				group.Warnings = []string{"Git has no verified primary checkout in this selection. The displayed checkout is a representative."}
			}
			index = len(out.Groups)
			byKey[key] = index
			out.Groups = append(out.Groups, group)
		}
		group := &out.Groups[index]
		membership := Membership{Member: members[path]}
		if info, ok := infoByPath[path]; ok {
			copyInfo := info
			membership.Checkout = &copyInfo
		}
		group.Memberships = append(group.Memberships, membership)
		if !slices.Contains(group.Directories, path) {
			group.Directories = append(group.Directories, path)
		}
		if info, exists := infoByPath[path]; exists && !slices.Contains(group.Directories, info.Root.Path) {
			group.Directories = append(group.Directories, info.Root.Path)
		}
		for _, name := range legacyNames[path] {
			if !slices.Contains(group.Names, name) && len(group.Names) < agentsessions.Limit {
				group.Names = append(group.Names, name)
			}
		}
	}
	for _, child := range children {
		key := pathGroup[child.Directory]
		if key == "" {
			key = "directory\x00" + child.Directory
		}
		if index, exists := byKey[key]; exists {
			out.Groups[index].Sessions = append(out.Groups[index].Sessions, child)
		} else {
			for index := range out.Coverage {
				coverage := &out.Coverage[index]
				if coverage.Adapter == child.Ref.Adapter {
					coverage.Truncated = true
					if len(coverage.Warnings) < 16 && !slices.Contains(coverage.Warnings, "Some chats were omitted with groups beyond the combined project limit.") {
						coverage.Warnings = append(coverage.Warnings, "Some chats were omitted with groups beyond the combined project limit.")
					}
				}
			}
		}
	}
	for i := range out.Groups {
		group := &out.Groups[i]
		slices.Sort(group.Directories)
		slices.Sort(group.Names)
		slices.SortFunc(group.Sessions, func(a, b agentsessions.SavedSession) int {
			return strings.Compare(a.Ref.Adapter+"\x00"+a.Ref.Home+"\x00"+a.Ref.ID, b.Ref.Adapter+"\x00"+b.Ref.Home+"\x00"+b.Ref.ID)
		})
	}
	// Refuse an unrepresentable result, including repeated root/path context in
	// the output. Strings were bounded before sanitizing and are only referenced
	// above; this check precedes any consumer's deep snapshot copy.
	textBytes := 0
	for _, group := range out.Groups {
		textBytes += len(group.Key) + len(group.Kind) + len(group.Name) + len(group.Directory)
		for _, values := range [][]string{group.Directories, group.Names, group.Warnings} {
			for _, value := range values {
				textBytes += len(value)
			}
		}
		for _, child := range group.Sessions {
			textBytes += len(child.Ref.Adapter) + len(child.Ref.Home) + len(child.Ref.ID) + len(child.Directory) + len(child.Name)
		}
		for _, membership := range group.Memberships {
			textBytes += len(membership.Member.Path) + len(membership.Member.Identity)
			if membership.Checkout != nil {
				for _, directory := range []gitmeta.Directory{membership.Checkout.Root, membership.Checkout.Git, membership.Checkout.Common} {
					textBytes += len(directory.Path) + len(directory.Identity)
				}
			}
		}
	}
	for _, coverage := range out.Coverage {
		textBytes += len(coverage.Adapter)
		for _, warning := range coverage.Warnings {
			textBytes += len(warning)
		}
	}
	for _, warning := range out.Warnings {
		textBytes += len(warning)
	}
	if textBytes > MaxHierarchyTextBytes {
		out.Groups = []Group{}
		out.Truncated = true
		warn("Hierarchy output exceeds the combined text bound and was refused; use explicit directories.")
		for index := range out.Coverage {
			out.Coverage[index].Truncated = true
		}
	}
	return out
}

// CheckoutName gives linked checkouts a repository label when Git metadata
// uses a conventional repository directory name.
func CheckoutName(info gitmeta.Info) string {
	if !info.Primary() {
		base := filepath.Base(info.Common.Path)
		if base == ".git" {
			return SanitizeTitle(filepath.Base(filepath.Dir(info.Common.Path)))
		}
		if name, ok := strings.CutSuffix(base, ".git"); ok && name != "" {
			return SanitizeTitle(name)
		}
	}
	return SanitizeTitle(filepath.Base(info.Root.Path))
}
