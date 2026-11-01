package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"getarm.org/internal/activation"
	"getarm.org/internal/agentstatus"
	"getarm.org/internal/local"
	"getarm.org/internal/model"
	"getarm.org/internal/rollout"
)

func statusPolicyReader(home string) agentstatus.OnboardingPolicyReader {
	return func(ctx context.Context) ([]rollout.Project, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state, err := (rollout.Store{Home: home}).Read()
		if err != nil {
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		return state.Projects, nil
	}
}
func statusSetupRecovery(home, key string) string {
	return installNextCommand(home, "rollout status-pages inspect --key "+activation.Quote(key))
}
func statusSetupError(home, key string, err error) error {
	var unknown *agentstatus.OnboardingOutcomeUnknown
	if errors.As(err, &unknown) {
		return model.Err("status_onboarding_outcome_unknown", fmt.Sprintf("Project policy is separate. Status setup outcome may be unknown; retain original key %s. Inspect: %s", unknown.Key, statusSetupRecovery(home, unknown.Key)))
	}
	return err
}

func (a *app) rolloutStatusPages(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("use rollout status-pages plan, apply or inspect")
	}
	op := args[0]
	f := a.flags("rollout status-pages " + op)
	key := f.String("key", "", "original status setup operation key; generated for a new plan")
	projects, adapter, clientProject, expect := "", "", "", ""
	disable, yes := false, false
	if op == "plan" || op == "apply" {
		f.StringVar(&projects, "projects", "", "comma-separated exact actual saved project IDs")
		f.StringVar(&adapter, "adapter", "", "explicit adapter context only; no discovery")
		f.StringVar(&clientProject, "client-project", "", "explicit client-project context; never guessed")
		f.BoolVar(&disable, "off", false, "record setup disabled; does not revoke agents or cancel resources")
	}
	if op == "apply" {
		f.StringVar(&expect, "expect", "", "exact review_sha256 from the identical status plan")
		f.BoolVar(&yes, "yes", false, "confirm this separate status preference write")
	}
	f.Bool("json", false, "JSON output")
	if op != "plan" && op != "apply" && op != "inspect" {
		return fmt.Errorf("unsupported status-pages operation")
	}
	if err := a.parse(f, args[1:], false); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("status-pages accepts flags only")
	}
	store := agentstatus.OnboardingStore{Home: a.home}
	reader := statusPolicyReader(a.home)
	if op == "inspect" {
		if *key == "" {
			return fmt.Errorf("inspect requires the original --key")
		}
		result, err := store.Inspect(a.ctx, reader, *key)
		if err != nil {
			return err
		}
		return a.print(result)
	}
	if projects == "" || clientProject != "" && adapter == "" {
		return fmt.Errorf("provide exact --projects and an explicit --adapter with any --client-project")
	}
	if *key == "" {
		if op == "apply" {
			return fmt.Errorf("apply requires the original plan --key")
		}
		*key = local.ID()
	}
	change := agentstatus.OnboardingChange{Version: 1, Key: *key, Projects: strings.Split(projects, ","), Setup: !disable}
	if adapter != "" {
		change.Contexts = []agentstatus.OnboardingContext{{Adapter: adapter, ClientProject: clientProject}}
	}
	if op == "plan" {
		result, err := store.Plan(a.ctx, reader, change)
		if err != nil {
			return err
		}
		return a.print(result)
	}
	if !yes || expect == "" {
		return fmt.Errorf("review plan first; apply requires identical --key/--expect and explicit --yes")
	}
	result, err := store.Apply(a.ctx, reader, change, expect)
	if err != nil {
		return statusSetupError(a.home, *key, err)
	}
	if a.ctx.Err() != nil {
		return model.Err("status_onboarding_interrupted", fmt.Sprintf("Status setup has recorded outcome %s under original key %s; project policy is separate. Inspect: %s", result.State, *key, statusSetupRecovery(a.home, *key)))
	}
	return a.print(result)
}
