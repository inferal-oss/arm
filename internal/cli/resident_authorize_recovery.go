package cli

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"getarm.org/internal/model"
	"getarm.org/internal/server"
)

type residentAuthorizationSelectors struct {
	Home     string `json:"home"`
	Key      string `json:"key"`
	Expect   string `json:"expect"`
	Profile  string `json:"profile"`
	Evidence string `json:"evidence"`
	Reason   string `json:"reason"`
}
type residentAuthorizationRecovery struct {
	residentAuthorizationSelectors
	Phase                        string   `json:"phase"`
	State                        string   `json:"state"`
	ThisCommandMutationAttempted bool     `json:"this_command_mutation_attempted"`
	KnownGrantID                 string   `json:"known_grant_id,omitempty"`
	InspectArgv                  []string `json:"inspect_argv"`
	AutomaticRetry               bool     `json:"automatic_retry"` // always false
}

// Reporting lifetime only: this actual owner/immutable selector context stays
// attached to the original app through finishCommand, including nil returns.
// It grants no execution/authorization/native authority and never retries.
type residentAuthorizationContext struct {
	owner     *app
	pending   *server.ResidentOperatorPending
	selectors residentAuthorizationSelectors
	phase     string
}

func (a *app) retainResidentAuthorization(pending *server.ResidentOperatorPending, selectors residentAuthorizationSelectors, phase string) error {
	if a.residentAuthorization != nil || pending == nil {
		return fmt.Errorf("original resident authorization reporting context already retained or unavailable")
	}
	a.residentAuthorization = &residentAuthorizationContext{owner: a, pending: pending, selectors: selectors, phase: phase}
	return nil
}
func (a *app) retainedResidentAuthorizationFailure(cause error) error {
	original := a.residentAuthorization
	if original == nil || original.owner != a || original.pending == nil {
		return nil
	}
	if cause == nil {
		cause = a.ctx.Err()
	}
	return residentAuthorizationFailure(original.pending, original.selectors, original.phase, cause)
}

type residentAuthorizationError struct {
	api      *model.Error
	recovery residentAuthorizationRecovery
	cause    error
}

func (e *residentAuthorizationError) Error() string { return e.api.Error() }
func (e *residentAuthorizationError) Unwrap() error { return e.cause }

// Selectors are DATA; mutation/commit knowledge comes ONLY from this actual
// retained operator's original operation publication, never from caller bools.
func residentAuthorizationFailure(pending *server.ResidentOperatorPending, selectors residentAuthorizationSelectors, phase string, cause error) error {
	seen := pending.InspectOriginal()
	state, code, message := "not_applied", "resident_authorization_not_applied", "No resident authorization mutation was attempted."
	if phase == "inspection" {
		state, code, message = "inspection_unavailable", "resident_authorization_inspection_unavailable", "Read-only inspection could not establish the original authorization outcome."
	} else if seen.GrantCommitKnown {
		state, code, message = "committed", "resident_authorization_committed", "The original grant frame and namespace were synced; the response, current-authority check or reference close failed."
		if phase == "completion" {
			message = "The original grant frame and namespace were synced before command completion was interrupted."
		}
	} else if seen.ExistingGrantRead {
		state, code, message = "existing_record", "resident_authorization_existing_record", "An exact existing authorization record was read; its original fsync result is not observable from this command."
		if phase == "completion" {
			message = "An exact existing authorization record was read before command completion was interrupted; its original fsync result is not observable here."
		}
	} else if seen.AuthorityMutationAttempted {
		state, code, message = "commit_unknown", "resident_authorization_outcome_unknown", "Resident authorization may have changed; the original commit outcome is unknown."
	}
	recovery := residentAuthorizationRecovery{residentAuthorizationSelectors: selectors, Phase: phase, State: state, ThisCommandMutationAttempted: seen.AuthorityMutationAttempted, KnownGrantID: seen.KnownGrantID, InspectArgv: selectors.inspectArgv()}
	message += " Inspect the original home/key with the read-only command. Do not resend apply or use a new key automatically."
	if cause != nil {
		message += " Cause: " + cause.Error()
	}
	return &residentAuthorizationError{api: &model.Error{Code: code, Message: message}, recovery: recovery, cause: cause}
}
func (s residentAuthorizationSelectors) inspectArgv() []string {
	return []string{"arm", "--home", s.Home, "resident", "authorize", "inspect", "--key", s.Key, "--expect", s.Expect, "--profile", s.Profile, "--evidence", s.Evidence, "--reason", s.Reason}
}
func residentInspectDisplay(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}
func (a *app) reportResidentAuthorizationError(err error) bool {
	var recovery *residentAuthorizationError
	if !errors.As(err, &recovery) {
		return false
	}
	if a.machine() || !a.errorTTY {
		_ = json.NewEncoder(a.err).Encode(map[string]any{"error": recovery.api, "resident_authorization": recovery.recovery})
	} else {
		fmt.Fprintln(a.err, a.paint("1;31", "Error: ")+terminalSafe(recovery.api.Message))
		fmt.Fprintln(a.err, "Code: "+terminalSafe(recovery.api.Code))
		fmt.Fprintln(a.err, "State: "+terminalSafe(recovery.recovery.State))
		fmt.Fprintln(a.err, "Inspect: "+terminalSafe(residentInspectDisplay(recovery.recovery.InspectArgv)))
	}
	return true
}

func validResidentAuthorizationExpect(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
