package rollout

import (
	"context"
	"time"
)

// BoundaryStatus describes filesystem evidence, never agent activity or adoption.
type BoundaryStatus struct {
	Project string  `json:"project"`
	Binding Binding `json:"binding"`
	State   string  `json:"state"`
	Detail  string  `json:"detail"`
}

// InspectBoundaries inspects only recorded roots and never repairs moved paths.
func InspectBoundaries(ctx context.Context, projects []Project) []BoundaryStatus {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := []BoundaryStatus{}
	cache := map[Binding]BoundaryStatus{}
	for _, p := range projects {
		for _, b := range p.Bindings {
			v, ok := cache[b]
			if p.Removed {
				v = BoundaryStatus{Binding: b, State: "removed", Detail: "Removed project; boundary retained to prevent broader fallback."}
			} else if !ok {
				v = inspectBoundary(ctx, b)
				cache[b] = v
			}
			v.Project = p.ID
			out = append(out, v)
		}
	}
	return out
}

func inspectBoundary(ctx context.Context, b Binding) BoundaryStatus {
	v := BoundaryStatus{Binding: b, State: "stale"}
	if b.Removed {
		v.State, v.Detail = "removed", "Removed boundary; broader fallback remains denied until explicitly rebound."
		return v
	}
	if ctx.Err() != nil {
		v.State, v.Detail = "unknown", "Boundary inspection interrupted or its time budget was exhausted."
		return v
	}
	root, err := canonical(b.Root)
	if err != nil {
		v.Detail = "Recorded directory is missing or inaccessible. Review its location, rebind explicitly, or remove this boundary."
		return v
	}
	if root != b.Root {
		v.Detail = "Recorded root now resolves to a different location. Explicit review is required."
		return v
	}
	id, err := directoryIdentity(root)
	if err != nil || id != b.Identity {
		v.Detail = "Directory identity changed. Explicit review is required before adoption."
		return v
	}
	if b.Kind == "git" {
		gitRoot, rootErr := gitDirectoryContext(ctx, root, "--show-toplevel")
		gitDir, dirErr := gitDirectoryContext(ctx, root, "--absolute-git-dir")
		gitID, idErr := directoryIdentity(gitDir)
		if ctx.Err() != nil {
			v.State, v.Detail = "unknown", "Git boundary inspection interrupted or its time budget was exhausted."
			return v
		}
		if rootErr != nil || dirErr != nil || idErr != nil || gitRoot != root || gitDir != b.GitDirectory || gitID != b.GitIdentity {
			v.Detail = "Git checkout or repository identity changed or cannot be verified. Explicit review is required."
			return v
		}
	}
	v.State, v.Detail = "ready", "Recorded filesystem identities match; session identification and acknowledgment remain separate."
	return v
}
