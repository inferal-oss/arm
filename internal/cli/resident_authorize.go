package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"getarm.org/internal/server"
)

func (a *app) residentCommand(args []string) (int, error) {
	if len(args) == 0 || args[0] != "authorize" {
		return 2, fmt.Errorf("resident requires authorize plan, apply or inspect")
	}
	args = args[1:]
	if a.group("resident authorize", args) {
		return 0, nil
	}
	if len(args) == 0 || (args[0] != "plan" && args[0] != "apply" && args[0] != "inspect") {
		return 2, fmt.Errorf("resident authorize requires plan, apply or inspect")
	}
	action, args := args[0], args[1:]
	f := a.flags("resident authorize " + action)
	profile := f.String("profile", "", "canonical JSON measured profile in existing private storage")
	evidence := f.String("evidence", "", "canonical JSON evidence bundle with bounded original artifact bytes")
	key := f.String("key", "", "exact original authorization key; never replace after an unknown outcome")
	reason := f.String("reason", "", "purpose recorded with the exact operator authorization")
	f.Bool("json", false, "JSON output")
	var expect *string
	var yes *bool
	if action != "plan" {
		expect = f.String("expect", "", "SHA256 from the identical reviewed plan")
		if action == "apply" {
			yes = f.Bool("yes", false, "explicitly authorize the exact reviewed standing resource promises")
		}
	}
	if err := a.parse(f, args, false); err != nil {
		return 2, err
	}
	if *profile == "" || *evidence == "" || *key == "" || *reason == "" {
		return 2, fmt.Errorf("profile, evidence, key and reason are required")
	}
	if action == "apply" && (!*yes || *expect == "") {
		return 2, fmt.Errorf("review the identical plan, then apply with --expect PLAN_SHA256 --yes")
	}
	if action == "inspect" && *expect == "" {
		return 2, fmt.Errorf("read-only inspection requires the original --expect PLAN_SHA256")
	}
	if action != "plan" && !validResidentAuthorizationExpect(*expect) {
		return 2, fmt.Errorf("expect must be the original lowercase 64-hex plan SHA256")
	}
	profilePath, err := filepath.Abs(*profile)
	if err != nil {
		return 2, err
	}
	evidencePath, err := filepath.Abs(*evidence)
	if err != nil {
		return 2, err
	}
	pending, err := server.NewResidentOperatorPending(a.home, profilePath, evidencePath, *key, *reason)
	if err != nil {
		return 2, err
	}
	// Retain the actual owner before its readonly native/image/input operations.
	// No backend, SQLite, rollout enrollment, core birth or worker is started.
	if action == "plan" {
		plan, err := pending.PlanOriginal(a.ctx)
		if err != nil {
			return 1, err
		}
		outputErr := a.print(plan)
		return 0, errors.Join(outputErr, pending.CloseCompletedReferences())
	}
	selectors := residentAuthorizationSelectors{Home: a.home, Key: *key, Expect: *expect, Profile: profilePath, Evidence: evidencePath, Reason: *reason}
	phase := "apply"
	if action == "inspect" {
		phase = "inspection"
	}
	if err := a.retainResidentAuthorization(pending, selectors, phase); err != nil {
		return 1, err
	}
	if action == "inspect" {
		observation, err := pending.InspectAuthorizationOriginal(a.ctx, *expect)
		if err != nil {
			return 1, residentAuthorizationFailure(pending, selectors, "inspection", err)
		}
		outputErr := a.print(observation)
		closeErr := pending.CloseCompletedReferences()
		if err = errors.Join(outputErr, closeErr); err != nil {
			return 1, residentAuthorizationFailure(pending, selectors, "inspection", err)
		}
		return 0, nil
	}
	receipt, err := pending.ApplyOriginal(a.ctx, *expect)
	if err != nil {
		return 1, residentAuthorizationFailure(pending, selectors, "apply", err)
	}
	a.residentAuthorization.phase = "response"
	outputErr := a.print(receipt)
	a.residentAuthorization.phase = "reference_close"
	closeErr := pending.CloseCompletedReferences()
	if err = errors.Join(outputErr, closeErr); err != nil {
		phase := "response"
		if closeErr != nil {
			phase = "reference_close"
			if outputErr != nil {
				phase = "response_and_reference_close"
			}
		}
		return 1, residentAuthorizationFailure(pending, selectors, phase, err)
	}
	a.residentAuthorization.phase = "completion"
	return 0, nil
}
