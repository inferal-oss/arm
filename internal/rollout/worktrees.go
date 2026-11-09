package rollout

import (
	"context"
	"path/filepath"
	"slices"

	"getarm.org/internal/gitmeta"
)

const WorktreesKind = "git-worktrees"

func newWorktreesBinding(path string) (Binding, error) {
	budget := gitmeta.NewBudget(context.Background())
	defer budget.Close()
	resolver, err := gitmeta.NewResolver(budget)
	if err != nil {
		return Binding{}, err
	}
	info, err := resolver.Resolve(path)
	if err != nil {
		return Binding{}, err
	}
	return worktreesBinding(resolver, info)
}

func worktreesBinding(resolver *gitmeta.Resolver, info gitmeta.Info) (Binding, error) {
	roots, err := resolver.Worktrees(info)
	if err != nil {
		return Binding{}, fail("cannot verify registered worktrees and the primary checkout: %v", err)
	}
	main := info
	if roots[0] != info.Root {
		main, err = resolver.Resolve(roots[0].Path)
	}
	if err != nil || !main.Primary() || main.Root != roots[0] || main.Common != info.Common {
		return Binding{}, fail("primary checkout does not match the selected local repository")
	}
	return Binding{
		Kind: WorktreesKind, Root: main.Root.Path, Identity: main.Root.Identity,
		GitDirectory: main.Common.Path, GitIdentity: main.Common.Identity,
	}, nil
}

type worktreeEvidence struct {
	info   gitmeta.Info
	roots  []gitmeta.Directory
	nested bool
}

func readWorktreeEvidence(ctx context.Context, path string, bindings []string) worktreeEvidence {
	budget := gitmeta.NewBudget(ctx)
	defer budget.Close()
	resolver, err := gitmeta.NewResolver(budget)
	if err != nil {
		return worktreeEvidence{}
	}
	// If the nearest checkout is foreign, look for an enclosing registered
	// worktree so it can still block fallback to a broader directory grant.
	for nested := false; ; nested = true {
		info, err := resolver.Resolve(path)
		if err != nil {
			return worktreeEvidence{}
		}
		if slices.Contains(bindings, info.Common.Path) {
			roots, _ := resolver.Worktrees(info)
			return worktreeEvidence{info: info, roots: roots, nested: nested}
		}
		path = filepath.Dir(info.Root.Path)
		if path == info.Root.Path {
			return worktreeEvidence{}
		}
	}
}

func (e worktreeEvidence) verifies(b Binding) bool {
	return len(e.roots) > 0 && e.roots[0].Path == b.Root && e.roots[0].Identity == b.Identity &&
		e.info.Common.Path == b.GitDirectory && e.info.Common.Identity == b.GitIdentity
}

func (e worktreeEvidence) match(b Binding, path string) (root, why string) {
	if e.info.Common.Path == b.GitDirectory {
		if !e.verifies(b) {
			return e.info.Root.Path, "Git worktree registration or repository identity could not be verified; review the binding"
		}
		if e.nested {
			return e.info.Root.Path, "working directory belongs to a nested Git repository; no adoption authorized"
		}
		return e.info.Root.Path, ""
	}
	if within(b.Root, path) {
		return b.Root, "working directory is outside the registered Git worktrees; no adoption authorized"
	}
	return "", ""
}

// RegisteredWorktreeRoots supplies candidate metadata roots. The caller must
// still resolve each root against the full project policy before querying it.
func RegisteredWorktreeRoots(ctx context.Context, b Binding) []string {
	if b.Kind != WorktreesKind || b.Removed {
		return nil
	}
	evidence := readWorktreeEvidence(ctx, b.Root, []string{b.GitDirectory})
	if !evidence.verifies(b) {
		return nil
	}
	roots := make([]string, 0, len(evidence.roots))
	for _, root := range evidence.roots {
		roots = append(roots, root.Path)
	}
	return roots
}
