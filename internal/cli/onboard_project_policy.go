package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"getarm.org/internal/agentprojects"
	"getarm.org/internal/agentsessions"
	"getarm.org/internal/gitmeta"
	"getarm.org/internal/install"
	"getarm.org/internal/rollout"
)

type pickerSetting struct {
	Group, Project, Kind, Mode string
}

type pickerChecked struct {
	Group   string
	Session agentsessions.SavedSession
}

type pickerSelection struct {
	Groups   []agentprojects.Group
	Settings []pickerSetting
	Checked  []pickerChecked
}

type pickerGitProbe interface {
	Resolve(string) (gitmeta.Info, error)
}
type pickerProbeFactory func(*gitmeta.Budget) (pickerGitProbe, error)

func pickerDefaultProbe(budget *gitmeta.Budget) (pickerGitProbe, error) {
	return gitmeta.NewResolver(budget)
}

func pickerValidID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func clonePickerSelection(input pickerSelection) pickerSelection {
	out := pickerSelection{Settings: slices.Clone(input.Settings), Checked: slices.Clone(input.Checked)}
	for _, group := range input.Groups {
		out.Groups = append(out.Groups, cloneOnboardGroup(group))
	}
	return out
}

// pickerRequest revalidates every member, including unchecked chats. Membership
// and Store have separate bounded budgets under the SAME entry deadline:
// preview <=64+32 probes, apply <=64+64; each stage <=1MiB observed Git output.
// NewBudget cannot extend ctx's earlier deadline. No root is inferred by prefix.
func pickerRequest(ctx context.Context, selection pickerSelection, reason string, factory pickerProbeFactory) (rollout.OnboardBatch, error) {
	if err := ctx.Err(); err != nil {
		return rollout.OnboardBatch{}, err
	}
	if _, err := newOnboardTree(selection.Groups); err != nil {
		return rollout.OnboardBatch{}, err
	}
	if len(selection.Groups) == 0 || len(selection.Settings) != len(selection.Groups) || len(selection.Checked) > agentsessions.Limit {
		return rollout.OnboardBatch{}, fmt.Errorf("unsupported project selection")
	}
	settings := map[string]pickerSetting{}
	for _, setting := range selection.Settings {
		if settings[setting.Group].Group != "" || !pickerValidID(setting.Project) || (setting.Kind != "git" && setting.Kind != "directory" && setting.Kind != rollout.WorktreesKind) || !slices.Contains([]string{"new-sessions", "all", "observe", "disabled"}, setting.Mode) {
			return rollout.OnboardBatch{}, fmt.Errorf("unsupported project settings")
		}
		settings[setting.Group] = setting
	}
	wanted := map[agentsessions.SavedSessionRef]pickerChecked{}
	for _, group := range selection.Groups {
		for _, session := range group.Sessions {
			wanted[session.Ref] = pickerChecked{Group: group.Key, Session: session}
		}
	}
	checked := map[agentsessions.SavedSessionRef]bool{}
	for _, recipient := range selection.Checked {
		expected, exists := wanted[recipient.Session.Ref]
		if !exists || expected != recipient || checked[recipient.Session.Ref] {
			return rollout.OnboardBatch{}, fmt.Errorf("selected chat context changed")
		}
		checked[recipient.Session.Ref] = true
	}
	budget := gitmeta.NewBudget(ctx)
	defer budget.Close()
	var probe pickerGitProbe
	probes := 0
	if factory == nil {
		factory = pickerDefaultProbe
	}
	actualByMember := map[string]agentprojects.Membership{}
	owners := map[string]rollout.Change{}
	for _, group := range selection.Groups {
		setting, ok := settings[group.Key]
		if !ok || len(group.Memberships) == 0 || group.Kind == "directory" && setting.Kind != "directory" {
			return rollout.OnboardBatch{}, fmt.Errorf("project has no explicit reviewed member mapping; refresh the picker")
		}
		rootInEvidence := false
		var repository gitmeta.Directory
		seenMembers := map[string]bool{}
		for _, expected := range group.Memberships {
			if err := budget.Check(); err != nil {
				return rollout.OnboardBatch{}, err
			}
			actual, cached := actualByMember[expected.Member.Path]
			if !cached {
				member, err := agentprojects.InspectDirectory(ctx, expected.Member.Path)
				if err != nil {
					return rollout.OnboardBatch{}, fmt.Errorf("project directory changed; refresh and review again: %w", err)
				}
				actual.Member = member
				if expected.Checkout != nil {
					if probe == nil {
						probe, err = factory(budget)
						if err != nil {
							return rollout.OnboardBatch{}, err
						}
					}
					if probes >= gitmeta.MaxProbes {
						return rollout.OnboardBatch{}, gitmeta.ErrBudget
					}
					probes++
					info, err := probe.Resolve(member.Path)
					if err != nil {
						return rollout.OnboardBatch{}, fmt.Errorf("checkout membership unavailable; refresh and review again: %w", err)
					}
					actual.Checkout = &info
				}
				actualByMember[member.Path] = actual
			}
			if actual.Member != expected.Member || (actual.Checkout == nil) != (expected.Checkout == nil) || actual.Checkout != nil && *actual.Checkout != *expected.Checkout {
				return rollout.OnboardBatch{}, fmt.Errorf("checkout membership changed; refresh and review again")
			}
			seenMembers[expected.Member.Path] = true
			root := actual.Member.Path
			if actual.Checkout != nil && actual.Checkout.Root.Path == group.Directory {
				rootInEvidence = true
			}
			switch setting.Kind {
			case "git", rollout.WorktreesKind:
				if actual.Checkout == nil {
					return rollout.OnboardBatch{}, fmt.Errorf("Git checkout proof is missing")
				}
				root = actual.Checkout.Root.Path
				if setting.Kind == rollout.WorktreesKind {
					if repository != (gitmeta.Directory{}) && repository != actual.Checkout.Common {
						return rollout.OnboardBatch{}, fmt.Errorf("selected worktrees do not share a local Git repository")
					}
					repository = actual.Checkout.Common
					root = group.Directory
				}
			case "directory":
				if root == group.Directory {
					rootInEvidence = true
				}
			}
			binding := rollout.Binding{Kind: setting.Kind, Root: root}
			change := rollout.Change{Action: "onboard", Project: setting.Project, Mode: setting.Mode, Reason: reason, Binding: &binding}
			if old, exists := owners[root]; exists {
				if old.Project != change.Project || old.Mode != change.Mode || old.Binding.Kind != binding.Kind {
					return rollout.OnboardBatch{}, fmt.Errorf("one checkout has conflicting project, scope or policy settings")
				}
			} else {
				owners[root] = change
			}
		}
		if group.Kind == "git" && !rootInEvidence {
			return rollout.OnboardBatch{}, fmt.Errorf("display representative is not a verified checkout root")
		}
		for _, child := range group.Sessions {
			if !seenMembers[child.Directory] {
				return rollout.OnboardBatch{}, fmt.Errorf("unchecked or checked chat has no exact member mapping")
			}
		}
	}
	if err := budget.Check(); err != nil {
		return rollout.OnboardBatch{}, err
	}
	// Deterministic projection includes unchecked sessions in Groups and exact
	// checked values separately. It grants no native or ARM identity authority.
	projection := clonePickerSelection(selection)
	slices.SortFunc(projection.Groups, func(a, b agentprojects.Group) int { return strings.Compare(a.Key, b.Key) })
	slices.SortFunc(projection.Settings, func(a, b pickerSetting) int { return strings.Compare(a.Group, b.Group) })
	slices.SortFunc(projection.Checked, func(a, b pickerChecked) int {
		return strings.Compare(a.Group+"\x00"+a.Session.Ref.Adapter+"\x00"+a.Session.Ref.Home+"\x00"+a.Session.Ref.ID, b.Group+"\x00"+b.Session.Ref.Adapter+"\x00"+b.Session.Ref.Home+"\x00"+b.Session.Ref.ID)
	})
	data, err := json.Marshal(struct {
		Version   int
		Namespace string
		Selection pickerSelection
	}{1, "rollout-onboard-picker:v1", projection})
	if err != nil {
		return rollout.OnboardBatch{}, err
	}
	digest := sha256.Sum256(data)
	request := rollout.OnboardBatch{Version: 1, Context: rollout.OnboardReviewContext{Version: 1, Digest: hex.EncodeToString(digest[:])}}
	for _, change := range owners {
		request.Changes = append(request.Changes, change)
	}
	slices.SortFunc(request.Changes, func(a, b rollout.Change) int {
		return strings.Compare(a.Project+"\x00"+a.Binding.Root, b.Project+"\x00"+b.Binding.Root)
	})
	return request, ctx.Err()
}

func pickerNeedsInstructions(plan rollout.OnboardBatchPlan) bool {
	for _, project := range plan.Projects {
		if !project.Unchanged && project.After.Mode != "disabled" {
			return true
		}
	}
	return false
}

func preparePickerPolicy(parent context.Context, home, reason string, selection pickerSelection, factory pickerProbeFactory) (rollout.OnboardBatchPlan, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	request, err := pickerRequest(ctx, selection, reason, factory)
	if err != nil {
		return rollout.OnboardBatchPlan{}, err
	}
	return (rollout.Store{Home: home}).PreviewOnboardBatch(ctx, request)
}

func applyPickerPolicy(parent context.Context, home, reason string, selection pickerSelection, reviewed rollout.OnboardBatchPlan, factory pickerProbeFactory) (rollout.OnboardBatchPlan, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	request, err := pickerRequest(ctx, selection, reason, factory)
	if err != nil {
		return rollout.OnboardBatchPlan{}, err
	}
	if request.Context != reviewed.Request.Context {
		return rollout.OnboardBatchPlan{}, fmt.Errorf("selection or checkout evidence changed; a new review is required")
	}
	if pickerNeedsInstructions(reviewed) && !install.InspectInstructions(home).Ready {
		return rollout.OnboardBatchPlan{}, fmt.Errorf("managed instructions changed; install instructions or choose Disabled before a new review")
	}
	if err = ctx.Err(); err != nil {
		return rollout.OnboardBatchPlan{}, err
	}
	return (rollout.Store{Home: home}).ApplyOnboardBatch(ctx, request, reviewed.Review)
}

func pickerUnknown(err error) bool {
	var unknown *rollout.OnboardOutcomeUnknown
	return errors.As(err, &unknown)
}
