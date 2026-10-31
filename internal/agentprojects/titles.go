package agentprojects

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"

	"getarm.org/internal/agentsessions"
)

// WithTitles decorates existing suggestions; names cannot introduce directories
// or change their evidence, ordering, scope or policy. Sources are optional.
type WithTitles struct {
	Adapter
	Source agentsessions.TitleSource
}

func (a WithTitles) Discover(ctx context.Context) Result {
	out := a.Adapter.Discover(ctx)
	if a.Source == nil || ctx.Err() != nil || len(out.Projects) == 0 {
		return out
	}
	// Send the bounded catalog once, so later worktree suggestions are not
	// silently excluded. The source retains one global response/time budget.
	roots := []string{}
	for _, p := range out.Projects[:min(maxProjects, len(out.Projects))] {
		roots = append(roots, p.Directory)
	}
	if len(out.Projects) > len(roots) {
		out.Warnings = append(out.Warnings, "Session name root limit reached; other directories remain selectable.")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	metadata := a.Source.DiscoverTitles(ctx, roots)
	out.Warnings = append(out.Warnings, metadata.Warnings...)
	if ctx.Err() != nil {
		out.Warnings = append(out.Warnings, "Session names are unavailable; directory suggestions remain selectable.")
		return out
	}
	byPath := map[string]int{}
	for i, root := range roots {
		byPath[root] = i
	}
	now := time.Now()
	for _, title := range metadata.Titles[:min(agentsessions.Limit, len(metadata.Titles))] {
		index, ok := byPath[title.Directory]
		// Activity age does not invalidate a name. Undated or implausibly future
		// metadata cannot establish a supported association.
		if !ok || title.Updated.IsZero() || title.Updated.After(now.Add(time.Minute)) {
			continue
		}
		name := SanitizeTitle(title.Name)
		if name != "" && !slices.Contains(out.Projects[index].Names, name) {
			out.Projects[index].Names = append(out.Projects[index].Names, name)
		}
	}
	for i := range out.Projects {
		slices.Sort(out.Projects[i].Names)
	}
	return out
}

// SanitizeTitle bounds display/search metadata and removes terminal and Unicode
// direction controls. It is never used to derive a project or session identity.
func SanitizeTitle(name string) string {
	var b strings.Builder
	n := 0
	truncated := false
	for _, r := range name {
		if n == 256 {
			truncated = true
			break
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
		n++
	}
	clean := strings.Join(strings.Fields(b.String()), " ")
	if clean != "" && truncated {
		clean += "…"
	}
	return clean
}
