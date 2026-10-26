// Package syncreconcile computes bounded, deterministic file synchronization plans.
// It observes no filesystem and never commits a baseline. Callers must obtain both
// endpoint completion receipts before persisting Plan.ProposedBaseline().
package syncreconcile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"reflect"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxEntries        = 200000
	MaxActions        = 400000
	MaxPathBytes      = 4096
	MaxDepth          = 128
	MaxComponentBytes = 255
	MaxResolutions    = MaxEntries
	MaxBindingBytes   = 16 << 10
)

type Side string

const (
	Local  Side = "local"
	Remote Side = "remote"
)

type Direction string

const (
	Push Direction = "push"
	Pull Direction = "pull"
	Both Direction = "both"
)

type Policy string

const (
	Additive Policy = "additive"
	Full     Policy = "full"
)

type Kind string

const (
	File      Kind = "file"
	Directory Kind = "directory"
	Symlink   Kind = "symlink"
)

// Entry is a fingerprint, not file contents. ContentHash is SHA-256 of file
// bytes or symlink target bytes. MetadataHash is SHA-256 of the selected metadata
// encoding. Directory contents are represented by their child entries.
type Entry struct {
	Kind         Kind   `json:"kind"`
	Size         int64  `json:"size"`
	ContentHash  string `json:"content_hash,omitempty"`
	MetadataHash string `json:"metadata_hash"`
}

// Endpoint binds an account/host identity and a native, held root identity.
// ID must include enrollment and effective account; RootID must include the
// named root and native directory/volume identity.
type Endpoint struct {
	ID     string `json:"id"`
	RootID string `json:"root_id"`
}

// Binding ties a baseline to exact ordered endpoints and selection/metadata
// policy. Exclusions are relative path prefixes, including their descendants.
type Binding struct {
	Local       Endpoint `json:"local"`
	Remote      Endpoint `json:"remote"`
	SelectionID string   `json:"selection_id"`
	MetadataID  string   `json:"metadata_id"`
	Exclusions  []string `json:"exclusions"`
}

// State is the last jointly confirmed state at one endpoint. Tombstone records
// a known deletion; absent without a tombstone is a confirmed empty slot.
type State struct {
	Present   bool  `json:"present"`
	Tombstone bool  `json:"tombstone"`
	Entry     Entry `json:"entry"`
}

// ConfirmedBaseline may only be constructed from durable, matching receipts at
// both endpoints. Maps are copied before planning.
type ConfirmedBaseline struct {
	Binding    Binding          `json:"binding"`
	Generation uint64           `json:"generation"`
	Local      map[string]State `json:"local"`
	Remote     map[string]State `json:"remote"`
	// Seal detects a missing or partial persisted record. It is an integrity
	// checksum, not a substitute for matching endpoint completion receipts.
	Seal string `json:"seal"`
}

// Snapshot contains only selected entries. NameKeys must include a key for
// every path present at either endpoint, resolution names, exclusions and all
// exclusion ancestors. Each key
// is the receiver's native canonical relative name; an empty key means that
// the path cannot be represented. The native enumerator must generate keys
// using the destination filesystem's case/Unicode rules, even for absent paths.
type Snapshot struct {
	Entries  map[string]Entry  `json:"entries"`
	NameKeys map[string]string `json:"name_keys"`
}

type ResolutionChoice string

const (
	KeepLocal  ResolutionChoice = "keep-local"
	KeepRemote ResolutionChoice = "keep-remote"
	KeepBoth   ResolutionChoice = "keep-both"
	Delete     ResolutionChoice = "delete"
)

// Resolution binds the exact observed fingerprints. KeepBoth moves both
// variants to two explicitly reviewed, unused sibling names at both endpoints.
type Resolution struct {
	Path           string           `json:"path"`
	Choice         ResolutionChoice `json:"choice"`
	ExpectedLocal  *Entry           `json:"expected_local,omitempty"`
	ExpectedRemote *Entry           `json:"expected_remote,omitempty"`
	LocalName      string           `json:"local_name,omitempty"`
	RemoteName     string           `json:"remote_name,omitempty"`
}

type Request struct {
	Binding      Binding
	Direction    Direction
	LocalPolicy  Policy
	RemotePolicy Policy
	Local        Snapshot
	Remote       Snapshot
	// FirstRun explicitly declares a new pair. Nil Baseline without FirstRun is
	// an unknown/lost baseline and must never infer deletion.
	FirstRun           bool
	Baseline           *ConfirmedBaseline
	ExpectedGeneration uint64
	Resolutions        []Resolution
}

type ActionKind string

const (
	Create    ActionKind = "create"
	Overwrite ActionKind = "overwrite"
	Metadata  ActionKind = "metadata"
	Remove    ActionKind = "remove"
)

type Action struct {
	// Before is the exact receiver precondition. Source and SourcePath refer
	// to the observed source, which must be staged and verified before applying
	// any removals or overwrites in this plan. The sorted action list is a
	// publication order after staging, not a transfer schedule.
	Side       Side       `json:"side"`
	Kind       ActionKind `json:"kind"`
	Path       string     `json:"path"`
	Before     *Entry     `json:"before,omitempty"`
	After      *Entry     `json:"after,omitempty"`
	Source     Side       `json:"source,omitempty"`
	SourcePath string     `json:"source_path,omitempty"`
}
type Conflict struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
	Local  *Entry `json:"local,omitempty"`
	Remote *Entry `json:"remote,omitempty"`
}

// Plan accessors return copies. A plan with conflicts has no executable
// actions or baseline proposal; all conflicts must be resolved in a new plan.
type Plan struct {
	digest     string
	generation uint64
	actions    []Action
	conflicts  []Conflict
	proposal   *ConfirmedBaseline
}

func (p Plan) Digest() string        { return p.digest }
func (p Plan) Generation() uint64    { return p.generation }
func (p Plan) Actions() []Action     { return cloneActions(p.actions) }
func (p Plan) Conflicts() []Conflict { return cloneConflicts(p.conflicts) }
func (p Plan) ProposedBaseline() (ConfirmedBaseline, bool) {
	if p.proposal == nil {
		return ConfirmedBaseline{}, false
	}
	return cloneBaseline(*p.proposal), true
}

type desired struct {
	entry      Entry
	source     Side
	sourcePath string
}

func Reconcile(req Request) (Plan, error) {
	if err := validateRequest(req); err != nil {
		return Plan{}, err
	}
	local := cloneEntries(req.Local.Entries)
	remote := cloneEntries(req.Remote.Entries)
	var prior ConfirmedBaseline
	if req.Baseline != nil {
		prior = cloneBaseline(*req.Baseline)
	}
	paths := pathUnion(local, remote, prior.Local, prior.Remote)
	if len(paths) > MaxEntries {
		return Plan{}, errors.New("selected path count exceeds limit")
	}
	pathSet := make(map[string]bool, len(paths))
	for _, p := range paths {
		pathSet[p] = true
	}
	if err := validateKeys(req.Local, paths); err != nil {
		return Plan{}, fmt.Errorf("local names: %w", err)
	}
	if err := validateKeys(req.Remote, paths); err != nil {
		return Plan{}, fmt.Errorf("remote names: %w", err)
	}
	if req.Baseline != nil {
		if err := validateBaselineNativeScope(prior.Local, req.Local, req.Binding.Exclusions); err != nil {
			return Plan{}, fmt.Errorf("local baseline scope: %w", err)
		}
		if err := validateBaselineNativeScope(prior.Remote, req.Remote, req.Binding.Exclusions); err != nil {
			return Plan{}, fmt.Errorf("remote baseline scope: %w", err)
		}
	}
	ld, rd := make(map[string]desired, len(local)), make(map[string]desired, len(remote))
	for p, e := range local {
		ld[p] = desired{e, Local, p}
	}
	for p, e := range remote {
		rd[p] = desired{e, Remote, p}
	}
	var conflicts []Conflict
	localDirectoryRemovals, remoteDirectoryRemovals := map[string]bool{}, map[string]bool{}
	localChanges, remoteChanges := map[string]bool{}, map[string]bool{}
	if req.Direction == Both && !req.FirstRun {
		localChanges = modifiedDescendants(paths, local, prior.Local)
		remoteChanges = modifiedDescendants(paths, remote, prior.Remote)
	}
	for _, p := range paths {
		le, lok := local[p]
		re, rok := remote[p]
		if req.Direction != Both {
			var source, receiver map[string]Entry
			var target map[string]desired
			var from Side
			var policy Policy
			if req.Direction == Push {
				source, receiver, target, from, policy = local, remote, rd, Local, req.RemotePolicy
			} else {
				source, receiver, target, from, policy = remote, local, ld, Remote, req.LocalPolicy
			}
			if e, ok := source[p]; ok {
				target[p] = desired{e, from, p}
			} else if policy == Full {
				delete(target, p)
			} else if e, ok := receiver[p]; ok {
				target[p] = desired{e, opposite(from), p}
			}
			continue
		}
		if req.FirstRun {
			switch {
			case lok && !rok:
				rd[p] = desired{le, Local, p}
			case rok && !lok:
				ld[p] = desired{re, Remote, p}
			case lok && rok && le != re:
				conflicts = append(conflicts, conflict(p, "first-run collision", le, lok, re, rok))
			}
			continue
		}
		if lok && rok && le.Kind != re.Kind {
			conflicts = append(conflicts, conflict(p, "type collision", le, lok, re, rok))
			continue
		}
		ls, lknown := prior.Local[p]
		rs, rknown := prior.Remote[p]
		if lknown != rknown {
			return Plan{}, fmt.Errorf("partial baseline at %q", p)
		}
		localDeletedDirectory := lknown && ls.Present && ls.Entry.Kind == Directory && !lok
		remoteDeletedDirectory := rknown && rs.Present && rs.Entry.Kind == Directory && !rok
		if localDeletedDirectory && rok && remoteChanges[p] || remoteDeletedDirectory && lok && localChanges[p] {
			conflicts = append(conflicts, conflict(p, "directory deletion conflicts with peer subtree changes", le, lok, re, rok))
			continue
		}
		lc := changed(ls, lknown, le, lok)
		rc := changed(rs, rknown, re, rok)
		switch {
		case lc && rc:
			if lok != rok || (lok && le != re) {
				conflicts = append(conflicts, conflict(p, "concurrent changes", le, lok, re, rok))
			}
		case lc:
			if lok {
				rd[p] = desired{le, Local, p}
			} else if req.RemotePolicy == Full {
				delete(rd, p)
				if rok && re.Kind == Directory {
					remoteDirectoryRemovals[p] = true
				}
			}
		case rc:
			if rok {
				ld[p] = desired{re, Remote, p}
			} else if req.LocalPolicy == Full {
				delete(ld, p)
				if lok && le.Kind == Directory {
					localDirectoryRemovals[p] = true
				}
			}
		default:
			// A policy change from additive to full may apply a previously
			// retained peer tombstone, but a stable asymmetric copy never
			// resurrects an unchanged deleted peer.
			if lok && !rok && rs.Tombstone && req.LocalPolicy == Full && !localChanges[p] {
				delete(ld, p)
				if le.Kind == Directory {
					localDirectoryRemovals[p] = true
				}
			}
			if rok && !lok && ls.Tombstone && req.RemotePolicy == Full && !remoteChanges[p] {
				delete(rd, p)
				if re.Kind == Directory {
					remoteDirectoryRemovals[p] = true
				}
			}
		}
	}
	applyDirectoryRemovals(ld, localDirectoryRemovals, paths)
	applyDirectoryRemovals(rd, remoteDirectoryRemovals, paths)
	resolved := make(map[string]bool, len(req.Resolutions))
	reservedNames := make(map[string]bool, len(req.Resolutions)*2)
	localOccupied, remoteOccupied := occupiedNames(req.Local, paths), occupiedNames(req.Remote, paths)
	resolutionPaths := make(map[string]bool, len(req.Resolutions))
	for _, r := range req.Resolutions {
		if resolutionPaths[r.Path] {
			return Plan{}, errors.New("duplicate conflict resolution")
		}
		resolutionPaths[r.Path] = true
	}
	for p := range resolutionPaths {
		for ancestor := path.Dir(p); ancestor != "."; ancestor = path.Dir(ancestor) {
			if resolutionPaths[ancestor] {
				return Plan{}, errors.New("overlapping conflict resolutions")
			}
		}
	}
	conflictByPath := make(map[string]Conflict, len(conflicts))
	for _, c := range conflicts {
		conflictByPath[c.Path] = c
	}
	for _, r := range req.Resolutions {
		c, exists := conflictByPath[r.Path]
		if !exists {
			return Plan{}, fmt.Errorf("resolution for nonconflict %q", r.Path)
		}
		if resolved[r.Path] {
			return Plan{}, fmt.Errorf("duplicate resolution for %q", r.Path)
		}
		if !reflect.DeepEqual(r.ExpectedLocal, c.Local) || !reflect.DeepEqual(r.ExpectedRemote, c.Remote) {
			return Plan{}, fmt.Errorf("stale resolution at %q", r.Path)
		}
		resolved[r.Path] = true
		switch r.Choice {
		case KeepLocal:
			replaceSubtree(ld, rd, local, r.Path, Local, paths)
		case KeepRemote:
			replaceSubtree(ld, rd, remote, r.Path, Remote, paths)
		case Delete:
			replaceSubtree(ld, rd, nil, r.Path, Local, paths)
		case KeepBoth:
			le, lok := local[r.Path]
			re, rok := remote[r.Path]
			if !lok || !rok || le.Kind == Directory || re.Kind == Directory {
				return Plan{}, fmt.Errorf("keep-both requires two leaf entries at %q", r.Path)
			}
			if err := validateKeepBoth(r, pathSet, req.Binding.Exclusions); err != nil {
				return Plan{}, err
			}
			if reservedNames[r.LocalName] || reservedNames[r.RemoteName] {
				return Plan{}, errors.New("duplicate keep-both target name")
			}
			reservedNames[r.LocalName], reservedNames[r.RemoteName] = true, true
			for _, s := range []Snapshot{req.Local, req.Remote} {
				if err := validateKeys(s, []string{r.LocalName, r.RemoteName}); err != nil {
					return Plan{}, err
				}
			}
			if err := reserveNativeNames(req.Local, r, localOccupied); err != nil {
				return Plan{}, err
			}
			if err := reserveNativeNames(req.Remote, r, remoteOccupied); err != nil {
				return Plan{}, err
			}
			delete(ld, r.Path)
			delete(rd, r.Path)
			ld[r.LocalName], rd[r.LocalName] = desired{le, Local, r.Path}, desired{le, Local, r.Path}
			ld[r.RemoteName], rd[r.RemoteName] = desired{re, Remote, r.Path}, desired{re, Remote, r.Path}
		default:
			return Plan{}, fmt.Errorf("invalid resolution %q", r.Choice)
		}
		if r.Choice != KeepBoth && (r.LocalName != "" || r.RemoteName != "") {
			return Plan{}, errors.New("names require keep-both resolution")
		}
	}
	remaining := conflicts[:0]
	for _, c := range conflicts {
		covered := resolved[c.Path]
		for ancestor := path.Dir(c.Path); !covered && ancestor != "."; ancestor = path.Dir(ancestor) {
			covered = resolved[ancestor]
		}
		if !covered {
			remaining = append(remaining, c)
		}
	}
	if len(remaining) != 0 {
		return conflictPlan(req, paths, remaining), nil
	}
	if err := restoreRequiredDirectories(ld, local, remote); err != nil {
		return Plan{}, fmt.Errorf("local result: %w", err)
	}
	if err := restoreRequiredDirectories(rd, local, remote); err != nil {
		return Plan{}, fmt.Errorf("remote result: %w", err)
	}
	if err := validateDesired(ld, req.Local.NameKeys); err != nil {
		return Plan{}, fmt.Errorf("local result: %w", err)
	}
	if err := validateDesired(rd, req.Remote.NameKeys); err != nil {
		return Plan{}, fmt.Errorf("remote result: %w", err)
	}
	localExclusions, remoteExclusions := canonicalExclusions(req.Local, req.Binding.Exclusions), canonicalExclusions(req.Remote, req.Binding.Exclusions)
	if err := validateDesiredExclusions(ld, req.Local.NameKeys, localExclusions); err != nil {
		return Plan{}, err
	}
	if err := validateDesiredExclusions(rd, req.Remote.NameKeys, remoteExclusions); err != nil {
		return Plan{}, err
	}
	actions := diffActions(local, remote, ld, rd)
	if err := validateActionExclusions(actions, req.Local, req.Remote, localExclusions, remoteExclusions); err != nil {
		return Plan{}, err
	}
	if len(actions) > MaxActions {
		return Plan{}, errors.New("action count exceeds limit")
	}
	gen := uint64(1)
	if req.Baseline != nil {
		gen = req.Baseline.Generation + 1
	}
	next := ConfirmedBaseline{Binding: cloneBinding(req.Binding), Generation: gen, Local: make(map[string]State), Remote: make(map[string]State)}
	all := pathUnionDesired(ld, rd, prior.Local, prior.Remote)
	if len(all) > MaxEntries {
		return Plan{}, errors.New("proposed baseline path count exceeds limit")
	}
	for _, p := range all {
		ls, lprev := prior.Local[p]
		rs, rprev := prior.Remote[p]
		next.Local[p] = nextState(ld, p, ls, lprev, local)
		next.Remote[p] = nextState(rd, p, rs, rprev, remote)
	}
	next.Seal = sealBaseline(next)
	p := Plan{generation: gen, actions: actions, proposal: &next}
	p.digest = digestPlan(req, paths, p)
	return p, nil
}

func replaceSubtree(ld, rd map[string]desired, source map[string]Entry, root string, side Side, paths []string) {
	replace := func(p string) {
		delete(ld, p)
		delete(rd, p)
		if e, ok := source[p]; ok {
			d := desired{e, side, p}
			ld[p], rd[p] = d, d
		}
	}
	replace(root)
	prefix := root + "/"
	start, _ := slices.BinarySearch(paths, prefix)
	for i := start; i < len(paths) && strings.HasPrefix(paths[i], prefix); i++ {
		replace(paths[i])
	}
}

func nextState(goal map[string]desired, p string, prev State, hadPrev bool, observed map[string]Entry) State {
	if d, ok := goal[p]; ok {
		return State{Present: true, Entry: d.entry}
	}
	_, wasPresent := observed[p]
	return State{Tombstone: wasPresent || (hadPrev && prev.Tombstone) || (hadPrev && prev.Present)}
}

func changed(prior State, known bool, current Entry, present bool) bool {
	if !known {
		return present
	}
	if prior.Present != present {
		return true
	}
	return present && prior.Entry != current
}

func opposite(s Side) Side {
	if s == Local {
		return Remote
	}
	return Local
}
func entryPtr(e Entry, ok bool) *Entry {
	if !ok {
		return nil
	}
	v := e
	return &v
}
func conflict(p, reason string, le Entry, lok bool, re Entry, rok bool) Conflict {
	return Conflict{p, reason, entryPtr(le, lok), entryPtr(re, rok)}
}

func conflictPlan(req Request, paths []string, conflicts []Conflict) Plan {
	slices.SortFunc(conflicts, func(a, b Conflict) int { return strings.Compare(a.Path, b.Path) })
	gen := uint64(1)
	if req.Baseline != nil {
		gen = req.Baseline.Generation + 1
	}
	p := Plan{generation: gen, conflicts: cloneConflicts(conflicts)}
	p.digest = digestPlan(req, paths, p)
	return p
}

func diffActions(local, remote map[string]Entry, ld, rd map[string]desired) []Action {
	var out []Action
	for _, side := range []Side{Local, Remote} {
		old, goal := local, ld
		if side == Remote {
			old, goal = remote, rd
		}
		for p, e := range old {
			if _, ok := goal[p]; !ok {
				out = append(out, Action{Side: side, Kind: Remove, Path: p, Before: entryPtr(e, true)})
			}
		}
		for p, d := range goal {
			e, ok := old[p]
			if ok && e == d.entry {
				continue
			}
			kind := Create
			if ok {
				kind = Overwrite
				if e.Kind == d.entry.Kind && e.Size == d.entry.Size && e.ContentHash == d.entry.ContentHash {
					kind = Metadata
				}
			}
			out = append(out, Action{Side: side, Kind: kind, Path: p, Before: entryPtr(e, ok), After: entryPtr(d.entry, true), Source: d.source, SourcePath: d.sourcePath})
		}
	}
	// Creating, replacing, or removing a descendant can change a directory's
	// selected metadata (notably mtime). Restore every pre-existing ancestor
	// after child publication, even when its observed and desired fingerprints
	// were equal. A newly created directory is finalized from its Create action.
	seen := make(map[string]bool, len(out))
	for _, a := range out {
		seen[string(a.Side)+"\x00"+a.Path] = true
	}
	for _, a := range slices.Clone(out) {
		if a.Kind == Metadata {
			continue
		}
		old, goal := local, ld
		if a.Side == Remote {
			old, goal = remote, rd
		}
		for parent := path.Dir(a.Path); parent != "."; parent = path.Dir(parent) {
			key := string(a.Side) + "\x00" + parent
			if seen[key] {
				continue
			}
			e, exists := old[parent]
			d, desiredExists := goal[parent]
			if !exists || !desiredExists || e.Kind != Directory || d.entry.Kind != Directory {
				continue
			}
			out = append(out, Action{Side: a.Side, Kind: Metadata, Path: parent, Before: entryPtr(e, true), After: entryPtr(d.entry, true), Source: d.source, SourcePath: d.sourcePath})
			seen[key] = true
		}
	}
	slices.SortFunc(out, func(a, b Action) int {
		phase := func(x Action) int {
			if x.Kind == Remove {
				return 0
			}
			if x.Kind == Metadata && x.After != nil && x.After.Kind == Directory {
				return 2
			}
			return 1
		}
		if x, y := phase(a), phase(b); x != y {
			return x - y
		}
		ad, bd := strings.Count(a.Path, "/"), strings.Count(b.Path, "/")
		if a.Kind == Remove || phase(a) == 2 {
			if ad != bd {
				return bd - ad
			}
		} else if ad != bd {
			return ad - bd
		}
		if x := strings.Compare(a.Path, b.Path); x != 0 {
			return x
		}
		return strings.Compare(string(a.Side), string(b.Side))
	})
	return out
}

func validateRequest(r Request) error {
	if r.Direction != Push && r.Direction != Pull && r.Direction != Both {
		return errors.New("invalid direction")
	}
	if r.LocalPolicy != Additive && r.LocalPolicy != Full || r.RemotePolicy != Additive && r.RemotePolicy != Full {
		return errors.New("invalid receiving policy")
	}
	if r.Binding.Local.ID == "" || r.Binding.Local.RootID == "" || r.Binding.Remote.ID == "" || r.Binding.Remote.RootID == "" || r.Binding.SelectionID == "" || r.Binding.MetadataID == "" {
		return errors.New("incomplete binding")
	}
	for _, value := range []string{r.Binding.Local.ID, r.Binding.Local.RootID, r.Binding.Remote.ID, r.Binding.Remote.RootID, r.Binding.SelectionID, r.Binding.MetadataID} {
		if len(value) > MaxBindingBytes || !utf8.ValidString(value) {
			return errors.New("binding field exceeds its byte bound or is not UTF-8")
		}
	}
	if r.Binding.Local.ID == r.Binding.Remote.ID && r.Binding.Local.RootID == r.Binding.Remote.RootID {
		return errors.New("identical endpoint roots")
	}
	if r.FirstRun == (r.Baseline != nil) {
		return errors.New("exactly one of FirstRun or confirmed Baseline is required")
	}
	if r.FirstRun && r.ExpectedGeneration != 0 {
		return errors.New("first run requires generation zero")
	}
	if err := validateExclusions(r.Binding.Exclusions); err != nil {
		return err
	}
	if r.Baseline != nil {
		if r.Baseline.Generation == 0 || r.Baseline.Generation == ^uint64(0) || r.Baseline.Generation != r.ExpectedGeneration {
			return errors.New("stale or invalid baseline generation")
		}
		if !reflect.DeepEqual(r.Baseline.Binding, r.Binding) {
			return errors.New("endpoint, root, selection or metadata binding changed")
		}
		if err := validateBaseline(*r.Baseline); err != nil {
			return err
		}
		for p := range r.Baseline.Local {
			if excluded(p, r.Binding.Exclusions) {
				return fmt.Errorf("excluded baseline path %q", p)
			}
		}
	}
	if err := validateSnapshot(r.Local, r.Binding.Exclusions); err != nil {
		return fmt.Errorf("local: %w", err)
	}
	if err := validateSnapshot(r.Remote, r.Binding.Exclusions); err != nil {
		return fmt.Errorf("remote: %w", err)
	}
	if r.Direction != Both && len(r.Resolutions) != 0 {
		return errors.New("resolutions require bidirectional mode")
	}
	if len(r.Resolutions) > MaxResolutions {
		return errors.New("too many resolutions")
	}
	return nil
}

func validateBaseline(b ConfirmedBaseline) error {
	if len(b.Local) > MaxEntries || len(b.Remote) > MaxEntries {
		return errors.New("baseline path count exceeds limit")
	}
	if !validSHA(b.Seal) || b.Seal != sealBaseline(b) {
		return errors.New("missing or corrupt baseline seal")
	}
	for p, s := range b.Local {
		if err := validatePath(p); err != nil {
			return err
		}
		if err := validateState(s); err != nil {
			return fmt.Errorf("local baseline %q: %w", p, err)
		}
		if _, ok := b.Remote[p]; !ok {
			return fmt.Errorf("partial baseline at %q", p)
		}
	}
	for p, s := range b.Remote {
		if err := validatePath(p); err != nil {
			return err
		}
		if err := validateState(s); err != nil {
			return fmt.Errorf("remote baseline %q: %w", p, err)
		}
		if _, ok := b.Local[p]; !ok {
			return fmt.Errorf("partial baseline at %q", p)
		}
	}
	return nil
}

func sealBaseline(b ConfirmedBaseline) string {
	b.Seal = ""
	data, _ := json.Marshal(b)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func validateState(s State) error {
	if s.Present {
		if s.Tombstone {
			return errors.New("present tombstone")
		}
		return validateEntry(s.Entry)
	}
	if s.Entry != (Entry{}) {
		return errors.New("absent state has entry")
	}
	return nil
}
func validateEntry(e Entry) error {
	if e.Kind != File && e.Kind != Directory && e.Kind != Symlink {
		return errors.New("unsupported entry kind")
	}
	if e.Size < 0 {
		return errors.New("negative size")
	}
	if !validSHA(e.MetadataHash) {
		return errors.New("invalid metadata hash")
	}
	if e.Kind == Directory {
		if e.Size != 0 || e.ContentHash != "" {
			return errors.New("directory has content fingerprint")
		}
		return nil
	}
	if !validSHA(e.ContentHash) {
		return errors.New("invalid content hash")
	}
	return nil
}
func validSHA(h string) bool {
	if len(h) != 64 {
		return false
	}
	_, err := hex.DecodeString(h)
	return err == nil && strings.ToLower(h) == h
}

func validateSnapshot(s Snapshot, excludes []string) error {
	if len(s.Entries) > MaxEntries {
		return errors.New("entry count exceeds limit")
	}
	if len(s.NameKeys) > MaxEntries*3 {
		return errors.New("native name count exceeds limit")
	}
	if err := validateExclusionNames(s, excludes); err != nil {
		return err
	}
	canonical := canonicalExclusions(s, excludes)
	for p, e := range s.Entries {
		if err := validatePath(p); err != nil {
			return err
		}
		if excluded(p, excludes) || excluded(s.NameKeys[p], canonical) {
			return fmt.Errorf("selected entry %q is excluded", p)
		}
		if err := validateEntry(e); err != nil {
			return fmt.Errorf("entry %q: %w", p, err)
		}
	}
	goal := make(map[string]desired, len(s.Entries))
	for p, e := range s.Entries {
		goal[p] = desired{entry: e}
	}
	return validateDesired(goal, s.NameKeys)
}
func validateExclusions(xs []string) error {
	if len(xs) > MaxEntries {
		return errors.New("too many exclusions")
	}
	seen := map[string]bool{}
	for _, p := range xs {
		if err := validatePath(p); err != nil {
			return fmt.Errorf("exclusion: %w", err)
		}
		if seen[p] {
			return errors.New("duplicate exclusion")
		}
		seen[p] = true
	}
	if !slices.IsSorted(xs) {
		return errors.New("exclusions must be sorted")
	}
	return nil
}
func excluded(p string, xs []string) bool {
	for probe := p; probe != "."; probe = path.Dir(probe) {
		if _, ok := slices.BinarySearch(xs, probe); ok {
			return true
		}
	}
	return false
}
func validatePath(p string) error {
	if p == "" || len(p) > MaxPathBytes || !utf8.ValidString(p) || strings.ContainsAny(p, "\\\x00\r\n") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return fmt.Errorf("invalid relative path %q", p)
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return fmt.Errorf("control character in path %q", p)
		}
	}
	parts := strings.Split(p, "/")
	if len(parts) > MaxDepth {
		return fmt.Errorf("path depth exceeds limit: %q", p)
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > MaxComponentBytes {
			return fmt.Errorf("invalid path component in %q", p)
		}
	}
	return nil
}
func validateKeys(s Snapshot, paths []string) error {
	for _, p := range paths {
		key, ok := s.NameKeys[p]
		if !ok || key == "" {
			return fmt.Errorf("no native canonical name for %q", p)
		}
		if err := validatePath(key); err != nil {
			return fmt.Errorf("key for %q: %w", p, err)
		}
		if strings.Count(key, "/") != strings.Count(p, "/") {
			return fmt.Errorf("canonical path depth changed for %q", p)
		}
		if parent := path.Dir(p); parent != "." {
			if parentKey, exists := s.NameKeys[parent]; exists && path.Dir(key) != parentKey {
				return fmt.Errorf("inconsistent canonical parent for %q", p)
			}
		}
	}
	return nil
}
func validateDesired(goal map[string]desired, keys map[string]string) error {
	if len(goal) > MaxEntries {
		return errors.New("result entry count exceeds limit")
	}
	paths := make([]string, 0, len(goal))
	for p := range goal {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	if err := validateKeys(Snapshot{NameKeys: keys}, paths); err != nil {
		return err
	}
	seen := make(map[string]string, len(goal))
	for _, p := range paths {
		key := keys[p]
		if first, ok := seen[key]; ok && first != p {
			return fmt.Errorf("case/Unicode collision: %q and %q", first, p)
		}
		seen[key] = p
		parent := path.Dir(p)
		if parent != "." {
			d, ok := goal[parent]
			if !ok || d.entry.Kind != Directory {
				return fmt.Errorf("missing or non-directory parent %q for %q", parent, p)
			}
		}
	}
	return nil
}
func validateKeepBoth(r Resolution, paths map[string]bool, excludes []string) error {
	if err := validatePath(r.LocalName); err != nil {
		return err
	}
	if err := validatePath(r.RemoteName); err != nil {
		return err
	}
	if r.LocalName == r.RemoteName || r.LocalName == r.Path || r.RemoteName == r.Path || path.Dir(r.LocalName) != path.Dir(r.Path) || path.Dir(r.RemoteName) != path.Dir(r.Path) {
		return errors.New("keep-both requires distinct new sibling names")
	}
	if excluded(r.LocalName, excludes) || excluded(r.RemoteName, excludes) {
		return errors.New("keep-both name is excluded")
	}
	if paths[r.LocalName] || paths[r.RemoteName] {
		return errors.New("keep-both name already observed or baselined")
	}
	return nil
}

func pathUnion(local, remote map[string]Entry, lb, rb map[string]State) []string {
	m := make(map[string]bool, len(local)+len(remote)+len(lb)+len(rb))
	for p := range local {
		m[p] = true
	}
	for p := range remote {
		m[p] = true
	}
	for p := range lb {
		m[p] = true
	}
	for p := range rb {
		m[p] = true
	}
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}
func pathUnionDesired(local, remote map[string]desired, lb, rb map[string]State) []string {
	m := map[string]bool{}
	for p := range local {
		m[p] = true
	}
	for p := range remote {
		m[p] = true
	}
	for p := range lb {
		m[p] = true
	}
	for p := range rb {
		m[p] = true
	}
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}
func cloneEntries(in map[string]Entry) map[string]Entry {
	out := make(map[string]Entry, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneStates(in map[string]State) map[string]State {
	out := make(map[string]State, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func cloneBinding(b Binding) Binding { b.Exclusions = slices.Clone(b.Exclusions); return b }
func cloneBaseline(b ConfirmedBaseline) ConfirmedBaseline {
	b.Binding = cloneBinding(b.Binding)
	b.Local = cloneStates(b.Local)
	b.Remote = cloneStates(b.Remote)
	return b
}
func cloneActions(in []Action) []Action {
	out := slices.Clone(in)
	for i := range out {
		if out[i].Before != nil {
			e := *out[i].Before
			out[i].Before = &e
		}
		if out[i].After != nil {
			e := *out[i].After
			out[i].After = &e
		}
	}
	return out
}
func cloneConflicts(in []Conflict) []Conflict {
	out := slices.Clone(in)
	for i := range out {
		if out[i].Local != nil {
			e := *out[i].Local
			out[i].Local = &e
		}
		if out[i].Remote != nil {
			e := *out[i].Remote
			out[i].Remote = &e
		}
	}
	return out
}

func digestPlan(req Request, paths []string, p Plan) string {
	// JSON contains sorted path rows, rather than maps, for a stable wire digest.
	type row struct {
		Path       string
		Local      *Entry
		Remote     *Entry
		LocalKey   string
		RemoteKey  string
		BaseLocal  State
		BaseRemote State
	}
	all := slices.Clone(paths)
	seen := make(map[string]bool, len(paths)+2*len(req.Resolutions))
	for _, name := range all {
		seen[name] = true
	}
	for _, excludedPath := range req.Binding.Exclusions {
		for name := excludedPath; name != "."; name = path.Dir(name) {
			if seen[name] {
				continue
			}
			all = append(all, name)
			seen[name] = true
		}
	}
	for _, r := range req.Resolutions {
		if r.LocalName != "" && !seen[r.LocalName] {
			all = append(all, r.LocalName)
			seen[r.LocalName] = true
		}
		if r.RemoteName != "" && !seen[r.RemoteName] {
			all = append(all, r.RemoteName)
			seen[r.RemoteName] = true
		}
	}
	slices.Sort(all)
	rows := make([]row, 0, len(all))
	for _, name := range all {
		le, lok := req.Local.Entries[name]
		re, rok := req.Remote.Entries[name]
		v := row{Path: name, Local: entryPtr(le, lok), Remote: entryPtr(re, rok), LocalKey: req.Local.NameKeys[name], RemoteKey: req.Remote.NameKeys[name]}
		if req.Baseline != nil {
			v.BaseLocal = req.Baseline.Local[name]
			v.BaseRemote = req.Baseline.Remote[name]
		}
		rows = append(rows, v)
	}
	res := slices.Clone(req.Resolutions)
	slices.SortFunc(res, func(a, b Resolution) int { return strings.Compare(a.Path, b.Path) })
	data, _ := json.Marshal(struct {
		Binding            Binding
		Direction          Direction
		LocalPolicy        Policy
		RemotePolicy       Policy
		FirstRun           bool
		ExpectedGeneration uint64
		Rows               []row
		Resolutions        []Resolution
		Actions            []Action
		Conflicts          []Conflict
		Proposed           *ConfirmedBaseline
	}{req.Binding, req.Direction, req.LocalPolicy, req.RemotePolicy, req.FirstRun, req.ExpectedGeneration, rows, res, p.actions, p.conflicts, p.proposal})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
