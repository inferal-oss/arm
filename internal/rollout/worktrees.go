package rollout

import (
	"context"
	"errors"
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
	return worktreesBinding(resolver, path)
}

func worktreesBinding(resolver *gitmeta.Resolver, path string) (Binding, error) {
	info, err := resolver.Resolve(path)
	var registry gitmeta.WorktreeRegistry
	switch {
	case err == nil:
		registry, err = resolver.Worktrees(info)
	case errors.Is(err, gitmeta.ErrUnsupported):
		registry, err = resolver.BareWorktrees(path)
		info.Common = registry.Anchor
	}
	if err != nil {
		return Binding{}, fail("cannot verify the Git worktree registry: %v", err)
	}
	if !registry.Bare {
		main := info
		if registry.Anchor != info.Root {
			main, err = resolver.Resolve(registry.Anchor.Path)
		}
		if err != nil || !main.Primary() || main.Root != registry.Anchor || main.Common != info.Common {
			return Binding{}, fail("primary checkout does not match the selected local repository")
		}
	}
	return Binding{
		Kind: WorktreesKind, Root: registry.Anchor.Path, Identity: registry.Anchor.Identity,
		GitDirectory: info.Common.Path, GitIdentity: info.Common.Identity,
	}, nil
}

type worktreeEvidence struct {
	info     gitmeta.Info
	registry gitmeta.WorktreeRegistry
	nested   bool
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
			registry, err := resolver.Worktrees(info)
			if err != nil {
				registry = gitmeta.WorktreeRegistry{}
			}
			return worktreeEvidence{info: info, registry: registry, nested: nested}
		}
		path = filepath.Dir(info.Root.Path)
		if path == info.Root.Path {
			return worktreeEvidence{}
		}
	}
}

func (e worktreeEvidence) verifies(b Binding) bool {
	return e.registry.Bare == (b.Root == b.GitDirectory) &&
		e.registry.Anchor.Path == b.Root && e.registry.Anchor.Identity == b.Identity &&
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
	registry, ok := registeredWorktreeRegistry(ctx, b)
	if !ok {
		return nil
	}
	roots := make([]string, 0, len(registry.Roots))
	for _, root := range registry.Roots {
		roots = append(roots, root.Path)
	}
	return roots
}

func registeredWorktreeRegistry(ctx context.Context, b Binding) (gitmeta.WorktreeRegistry, bool) {
	if b.Kind != WorktreesKind || b.Removed {
		return gitmeta.WorktreeRegistry{}, false
	}
	if b.Root == b.GitDirectory {
		budget := gitmeta.NewBudget(ctx)
		defer budget.Close()
		resolver, err := gitmeta.NewResolver(budget)
		if err != nil {
			return gitmeta.WorktreeRegistry{}, false
		}
		registry, err := resolver.BareWorktrees(b.Root)
		evidence := worktreeEvidence{info: gitmeta.Info{Common: registry.Anchor}, registry: registry}
		return registry, err == nil && evidence.verifies(b)
	}
	evidence := readWorktreeEvidence(ctx, b.Root, []string{b.GitDirectory})
	return evidence.registry, evidence.verifies(b)
}
