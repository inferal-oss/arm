package syncreconcile

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// Directory fingerprints exclude their children. A newly changed descendant
// still makes a concurrent deletion of that directory a delete/edit conflict.
func modifiedDescendants(paths []string, current map[string]Entry, prior map[string]State) map[string]bool {
	out := map[string]bool{}
	for _, p := range paths {
		old, known := prior[p]
		entry, present := current[p]
		if !changed(old, known, entry, present) {
			continue
		}
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			if out[parent] {
				break
			}
			out[parent] = true
		}
	}
	return out
}

// A retained additive subtree can have an absent/tombstoned peer parent. An
// actual later child edit needs that parent to exist again, but must not copy
// unchanged retained siblings. Only structural directories are restored here.
func restoreRequiredDirectories(goal map[string]desired, local, remote map[string]Entry) error {
	paths := make([]string, 0, len(goal))
	for p := range goal {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		d := goal[p]
		source := local
		if d.source == Remote {
			source = remote
		}
		for parent, sourceParent := path.Dir(p), path.Dir(d.sourcePath); parent != "."; parent, sourceParent = path.Dir(parent), path.Dir(sourceParent) {
			if existing, ok := goal[parent]; ok {
				if existing.entry.Kind != Directory {
					return fmt.Errorf("non-directory desired parent %q for %q", parent, p)
				}
				continue
			}
			entry, ok := source[sourceParent]
			if !ok || entry.Kind != Directory {
				return fmt.Errorf("no selected source directory for required parent %q", parent)
			}
			goal[parent] = desired{entry: entry, source: d.source, sourcePath: sourceParent}
		}
	}
	return nil
}

func validateExclusionNames(s Snapshot, xs []string) error {
	seen := map[string]bool{}
	for _, x := range xs {
		for p := x; p != "."; p = path.Dir(p) {
			if seen[p] {
				break
			}
			seen[p] = true
			if len(seen) > MaxEntries*3 {
				return fmt.Errorf("exclusion native name count exceeds limit")
			}
			if err := validateKeys(s, []string{p}); err != nil {
				return fmt.Errorf("exclusion names: %w", err)
			}
		}
	}
	return nil
}
func canonicalExclusions(s Snapshot, xs []string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, s.NameKeys[x])
	}
	slices.Sort(out)
	return slices.Compact(out)
}
func validateDesiredExclusions(goal map[string]desired, keys map[string]string, xs []string) error {
	for p := range goal {
		if excluded(keys[p], xs) {
			return fmt.Errorf("desired path %q aliases an excluded path", p)
		}
	}
	return nil
}
func validateActionExclusions(actions []Action, local, remote Snapshot, localExclusions, remoteExclusions []string) error {
	for _, a := range actions {
		keys, xs := local.NameKeys, localExclusions
		if a.Side == Remote {
			keys, xs = remote.NameKeys, remoteExclusions
		}
		if a.Kind != Remove && (a.After == nil || a.After.Kind == Directory) {
			continue
		}
		key := keys[a.Path]
		prefix := key + "/"
		i, _ := slices.BinarySearch(xs, prefix)
		if i < len(xs) && strings.HasPrefix(xs[i], prefix) {
			return fmt.Errorf("action at %q would remove or mask excluded subtree %q", a.Path, xs[i])
		}
	}
	return nil
}

// Full propagation of a known directory deletion removes its unchanged,
// selected descendants as individual reviewed actions, including entries that
// existed only at an additive receiver. Concurrent descendant changes were
// rejected before this point; excluded descendants are protected separately.
func applyDirectoryRemovals(goal map[string]desired, roots map[string]bool, paths []string) {
	for root := range roots {
		delete(goal, root)
		prefix := root + "/"
		start, _ := slices.BinarySearch(paths, prefix)
		for i := start; i < len(paths) && strings.HasPrefix(paths[i], prefix); i++ {
			delete(goal, paths[i])
		}
	}
}

func occupiedNames(s Snapshot, paths []string) map[string]bool {
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		out[s.NameKeys[p]] = true
	}
	return out
}
func reserveNativeNames(s Snapshot, r Resolution, occupied map[string]bool) error {
	left, right := s.NameKeys[r.LocalName], s.NameKeys[r.RemoteName]
	if left == right || occupied[left] || occupied[right] {
		return fmt.Errorf("keep-both target names are not unused in the native namespace")
	}
	occupied[left], occupied[right] = true, true
	return nil
}

// A newly unrepresentable or newly excluded historical path is a changed
// comparison/selection scope, not a deletion observation. Never propagate it.
func validateBaselineNativeScope(states map[string]State, s Snapshot, exclusions []string) error {
	canonical := canonicalExclusions(s, exclusions)
	present := map[string]desired{}
	for p, state := range states {
		if excluded(s.NameKeys[p], canonical) {
			return fmt.Errorf("historical path %q is now excluded by native comparison rules", p)
		}
		if state.Present {
			present[p] = desired{entry: state.Entry}
		}
	}
	return validateDesired(present, s.NameKeys)
}
