package rollout

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"getarm.org/internal/gitmeta"
	"getarm.org/internal/local"
)

const MaxOnboardChanges = 256

// OnboardReviewContext seals the UI's scopes and selected/unselected display
// recipient snapshot. It is review binding, never identity or delivery authority.
type OnboardReviewContext struct {
	Version int    `json:"version"`
	Digest  string `json:"sha256"`
}

type OnboardBatch struct {
	Version int                  `json:"version"`
	Context OnboardReviewContext `json:"review_context"`
	Changes []Change             `json:"changes"`
}

type OnboardProjectPlan struct {
	Before    *Project `json:"before,omitempty"`
	After     Project  `json:"after"`
	Unchanged bool     `json:"unchanged,omitempty"`
}

type OnboardBatchPlan struct {
	Version   int                  `json:"version"`
	Review    string               `json:"review_sha256"`
	Revision  int                  `json:"revision"`
	Request   OnboardBatch         `json:"request"`
	Projects  []OnboardProjectPlan `json:"projects"`
	Affected  []Session            `json:"affected_sessions"`
	Unchanged bool                 `json:"unchanged,omitempty"`
	Effect    string               `json:"effect"`
}

// OnboardOutcomeUnknown means the original save was attempted and returned an
// error. It does not prove rollback or authorize a retry or activation delivery.
type OnboardOutcomeUnknown struct {
	Review string
	Err    error
}

func (e *OnboardOutcomeUnknown) Error() string {
	return "rollout onboarding save outcome may have changed; inspect status and history before retrying"
}
func (e *OnboardOutcomeUnknown) Unwrap() error { return e.Err }

type onboardOperation struct {
	budget   *gitmeta.Budget
	resolver *gitmeta.Resolver
}

func (o *onboardOperation) normalize(request OnboardBatch) (OnboardBatch, error) {
	if err := o.budget.Check(); err != nil {
		return OnboardBatch{}, err
	}
	if request.Version != 1 || request.Context.Version != 1 || len(request.Context.Digest) != 64 || len(request.Changes) == 0 || len(request.Changes) > MaxOnboardChanges {
		return OnboardBatch{}, fail("unsupported onboarding batch or review context")
	}
	if _, err := hex.DecodeString(request.Context.Digest); err != nil {
		return OnboardBatch{}, fail("onboarding review context must be a SHA256 digest")
	}
	out := OnboardBatch{Version: 1, Context: OnboardReviewContext{Version: 1, Digest: strings.ToLower(request.Context.Digest)}}
	cache := map[string]Binding{}
	gitInputs := map[string]bool{}
	modes := map[string]string{}
	owners := map[string]Change{}
	for _, change := range request.Changes {
		if err := o.budget.Check(); err != nil {
			return OnboardBatch{}, err
		}
		if change.Action != "onboard" || !projectID(change.Project) || !short(change.Reason, 512) || change.Binding == nil || change.Client != nil || len(change.Sessions) != 0 || change.Binding.Removed || (change.Mode != "disabled" && change.Mode != "observe" && change.Mode != "new-sessions" && change.Mode != "all") {
			return OnboardBatch{}, fail("batch accepts only bounded project onboarding changes")
		}
		if previous, exists := modes[change.Project]; exists && previous != change.Mode {
			return OnboardBatch{}, fail("one project has conflicting onboarding policies")
		}
		modes[change.Project] = change.Mode
		input := *change.Binding
		if (input.Kind != "directory" && input.Kind != "git") || !filepath.IsAbs(input.Root) || len(input.Root) > 4096 || strings.ContainsFunc(input.Root, func(r rune) bool { return r < 32 || r == 127 }) {
			return OnboardBatch{}, fail("unsupported onboarding scope")
		}
		if input.Kind == "directory" && (input.GitDirectory != "" || input.GitIdentity != "") {
			return OnboardBatch{}, fail("directory scope has unrelated Git selectors")
		}
		root, err := canonical(input.Root)
		if err != nil {
			return OnboardBatch{}, err
		}
		if err = o.budget.Check(); err != nil {
			return OnboardBatch{}, err
		}
		key := input.Kind + "\x00" + root
		binding, exists := cache[key]
		if !exists {
			if input.Kind == "git" {
				gitInputs[root] = true
				if len(gitInputs) > gitmeta.MaxGitBindings {
					return OnboardBatch{}, fail("onboarding batch exceeds 32 distinct requested Git scopes")
				}
				if o.resolver == nil {
					o.resolver, err = gitmeta.NewResolver(o.budget)
					if err != nil {
						return OnboardBatch{}, err
					}
				}
				info, err := o.resolver.Resolve(root)
				if err != nil {
					return OnboardBatch{}, err
				}
				binding = Binding{Kind: "git", Root: info.Root.Path, Identity: info.Root.Identity, GitDirectory: info.Git.Path, GitIdentity: info.Git.Identity}
			} else {
				identity, err := directoryIdentity(root)
				if err != nil {
					return OnboardBatch{}, err
				}
				binding = Binding{Kind: "directory", Root: root, Identity: identity}
			}
			cache[key] = binding
		}
		change.Binding = &binding
		change.Sessions = nil
		if previous, exists := owners[binding.Root]; exists {
			if previous.Project != change.Project || !reflect.DeepEqual(previous, change) {
				return OnboardBatch{}, fail("one root has conflicting project, scope or policy changes")
			}
			continue
		}
		owners[binding.Root] = change
		out.Changes = append(out.Changes, change)
	}
	slices.SortFunc(out.Changes, func(a, b Change) int {
		if order := strings.Compare(a.Project, b.Project); order != 0 {
			return order
		}
		return strings.Compare(a.Binding.Root, b.Binding.Root)
	})
	return out, o.budget.Check()
}

func onboardBatchDigest(state State, request OnboardBatch) (string, error) {
	data, err := json.Marshal(struct {
		Operation string
		State     State
		Request   OnboardBatch
	}{"onboard-batch:v1", state, request})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func cloneOnboardProject(project Project) Project {
	project.Bindings = slices.Clone(project.Bindings)
	project.Clients = slices.Clone(project.Clients)
	return project
}

func (o *onboardOperation) transition(state *State, request OnboardBatch, at time.Time, review string) (OnboardBatchPlan, error) {
	plan := OnboardBatchPlan{Version: 1, Review: review, Revision: state.Revision, Request: request, Projects: []OnboardProjectPlan{}, Affected: []Session{}, Unchanged: true, Effect: "All selected project policies commit together. New-sessions cutoffs begin at apply time; retaining that mode preserves its cutoff. Chat selections do not identify, invite, enroll or deliver messages. Activation remains separately reviewed."}
	before := map[string]*Project{}
	order := []string{}
	for _, change := range request.Changes {
		if _, exists := before[change.Project]; exists {
			continue
		}
		order = append(order, change.Project)
		before[change.Project] = nil
		if project := find(state, change.Project); project != nil {
			copy := cloneOnboardProject(*project)
			before[change.Project] = &copy
		}
	}
	affected := map[string]bool{}
	for _, change := range request.Changes {
		if err := o.budget.Check(); err != nil {
			return OnboardBatchPlan{}, err
		}
		step, err := transition(state, change, at)
		if err != nil {
			return OnboardBatchPlan{}, err
		}
		plan.Unchanged = plan.Unchanged && step.Unchanged
		for _, session := range step.Affected {
			affected[session.Identity] = true
		}
	}
	for _, id := range order {
		after := cloneOnboardProject(*find(state, id))
		plan.Projects = append(plan.Projects, OnboardProjectPlan{Before: before[id], After: after, Unchanged: before[id] != nil && reflect.DeepEqual(*before[id], after)})
	}
	for _, session := range state.Sessions {
		if affected[session.Identity] {
			session.Capabilities = slices.Clone(session.Capabilities)
			plan.Affected = append(plan.Affected, session)
		}
	}
	if err := validate(*state); err != nil {
		return OnboardBatchPlan{}, err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil || len(data)+1 > maxBytes {
		return OnboardBatchPlan{}, fail("onboarding ledger capacity reached")
	}
	data, err = json.Marshal(plan)
	if err != nil || len(data) > maxBytes {
		return OnboardBatchPlan{}, fail("onboarding review capacity reached")
	}
	return plan, o.budget.Check()
}

func (s Store) previewOnboardBatch(operation *onboardOperation, request OnboardBatch) (OnboardBatchPlan, error) {
	normalized, err := operation.normalize(request)
	if err != nil {
		return OnboardBatchPlan{}, err
	}
	state, err := s.Read()
	if err != nil {
		return OnboardBatchPlan{}, err
	}
	if err = operation.budget.Check(); err != nil {
		return OnboardBatchPlan{}, err
	}
	review, err := onboardBatchDigest(state, normalized)
	if err != nil {
		return OnboardBatchPlan{}, err
	}
	// Read returns the operation's owned decoded working copy. There is no
	// per-change full-state clone, and no trial state is published during preview.
	return operation.transition(&state, normalized, s.now(), review)
}

func (s Store) PreviewOnboardBatch(ctx context.Context, request OnboardBatch) (OnboardBatchPlan, error) {
	budget := gitmeta.NewBudget(ctx)
	defer budget.Close()
	return s.previewOnboardBatch(&onboardOperation{budget: budget}, request)
}

func (s Store) ApplyOnboardBatch(ctx context.Context, request OnboardBatch, expect string) (OnboardBatchPlan, error) {
	return s.applyOnboardBatch(ctx, request, expect, s.save)
}

func (s Store) applyOnboardBatch(ctx context.Context, request OnboardBatch, expect string, save func(State) error) (OnboardBatchPlan, error) {
	budget := gitmeta.NewBudget(ctx)
	defer budget.Close()
	operation := &onboardOperation{budget: budget}
	return s.applyOnboardBatchOperation(operation, request, expect, save)
}

// applyOnboardBatchOperation accepts one already-owned operation so its budget
// cannot be replaced between preflight and the locked recheck. Tests may charge
// that same private operation before exercising the refusal boundary.
func (s Store) applyOnboardBatchOperation(operation *onboardOperation, request OnboardBatch, expect string, save func(State) error) (OnboardBatchPlan, error) {
	budget := operation.budget
	preview, err := s.previewOnboardBatch(operation, request)
	if err != nil {
		return OnboardBatchPlan{}, err
	}
	if expect == "" || preview.Review != expect {
		return OnboardBatchPlan{}, fail("onboarding review changed; preview again before applying")
	}
	if err = budget.Check(); err != nil {
		return OnboardBatchPlan{}, err
	}
	lock, err := local.Lock(s.Home, "rollout.lock")
	if err != nil {
		return OnboardBatchPlan{}, err
	}
	defer lock.Close()
	if err = budget.Check(); err != nil {
		return OnboardBatchPlan{}, err
	}
	state, err := s.Read()
	if err != nil {
		return OnboardBatchPlan{}, err
	}
	normalized, err := operation.normalize(request)
	if err != nil {
		return OnboardBatchPlan{}, err
	}
	review, err := onboardBatchDigest(state, normalized)
	if err != nil {
		return OnboardBatchPlan{}, err
	}
	if review != expect {
		return OnboardBatchPlan{}, fail("onboarding review changed; no policy was applied")
	}
	plan, err := operation.transition(&state, normalized, s.now(), expect)
	if err != nil {
		return OnboardBatchPlan{}, err
	}
	if plan.Unchanged {
		return plan, nil
	}
	if err = budget.Check(); err != nil {
		return OnboardBatchPlan{}, err
	}
	// Once save is attempted, await its original completion. A late context
	// cancellation cannot turn a successful save into a claim of rollback.
	if err = save(state); err != nil {
		return plan, &OnboardOutcomeUnknown{Review: expect, Err: err}
	}
	return plan, nil
}
