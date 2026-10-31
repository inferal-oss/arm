package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/model"
	"getarm.org/internal/server"
)

func TestResidentAuthorizationRecoverySurvivesSharedInterruption(t *testing.T) {
	// Formatting DATA ONLY. Synthetic states below are never supplied to an
	// issuer, pending operation, native owner, service grant or charge. Real
	// after-apply/lost-receipt behavior still requires the SAME real issuer.
	states := []struct{ state, code string }{
		{"not_applied", "resident_authorization_not_applied"},
		{"commit_unknown", "resident_authorization_outcome_unknown"},
		{"committed", "resident_authorization_committed"},
		{"existing_record", "resident_authorization_existing_record"},
		{"inspection_unavailable", "resident_authorization_inspection_unavailable"},
	}
	for _, state := range states {
		for _, cancelled := range []bool{false, true} {
			for _, human := range []bool{false, true} {
				for _, wrapped := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/cancelled=%v/human=%v/wrapped=%v", state.state, cancelled, human, wrapped), func(t *testing.T) {
						ctx, cancel := context.WithCancel(t.Context())
						if cancelled {
							cancel()
						} else {
							defer cancel()
						}
						var cause error = io.ErrClosedPipe
						if cancelled {
							cause = context.Canceled
						}
						var out, stderr bytes.Buffer
						home := filepath.Join(t.TempDir(), "selected custom 'home'")
						selectors := residentAuthorizationSelectors{Home: home, Key: "original-key", Expect: strings.Repeat("a", 64), Profile: filepath.Join(home, "profile.json"), Evidence: filepath.Join(home, "evidence.json"), Reason: "original reason"}
						recoveryData := residentAuthorizationRecovery{residentAuthorizationSelectors: selectors, State: state.state, Phase: "synthetic-format-data", InspectArgv: selectors.inspectArgv()}
						var err error = &residentAuthorizationError{api: &model.Error{Code: state.code, Message: "synthetic reporting data only"}, recovery: recoveryData, cause: cause}
						if wrapped {
							err = fmt.Errorf("wrapped result: %w", err)
						}
						a := app{home: home, ctx: ctx, out: &out, err: &stderr, outputTTY: human, errorTTY: human}
						wantCode := 1
						if cancelled {
							wantCode = 130
						}
						if code := a.finishCommand(1, err); code != wantCode {
							t.Fatal(code)
						}
						if !errors.Is(err, cause) || out.Len() != 0 || !strings.Contains(stderr.String(), state.code) || strings.Contains(stderr.String(), "operation interrupted") {
							t.Fatal("typed recovery was erased", out.String(), stderr.String())
						}
						if human {
							if !strings.Contains(stderr.String(), residentInspectDisplay(selectors.inspectArgv())) || !strings.Contains(stderr.String(), "State: "+state.state) {
								t.Fatal(stderr.String())
							}
						} else {
							var result struct {
								Error    *model.Error                  `json:"error"`
								Recovery residentAuthorizationRecovery `json:"resident_authorization"`
							}
							if err := json.Unmarshal(stderr.Bytes(), &result); err != nil || result.Error == nil || result.Error.Code != state.code || result.Recovery.State != state.state || result.Recovery.residentAuthorizationSelectors != selectors || result.Recovery.AutomaticRetry || !reflect.DeepEqual(result.Recovery.InspectArgv, selectors.inspectArgv()) {
								t.Fatal("lost exact recovery selectors", stderr.String(), err)
							}
						}
					})
				}
			}
		}
	}
}

func TestResidentAuthorizationCancelledBeforeMutationReportsNotApplied(t *testing.T) {
	home := filepath.Join(t.TempDir(), "selected absent home")
	selectors := residentAuthorizationSelectors{Home: home, Key: "original-key", Expect: strings.Repeat("a", 64), Profile: filepath.Join(home, "profile"), Evidence: filepath.Join(home, "evidence"), Reason: "negative pre-effect cancellation"}
	pending, err := server.NewResidentOperatorPending(home, selectors.Profile, selectors.Evidence, selectors.Key, selectors.Reason)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = pending.ApplyOriginal(ctx, selectors.Expect); err != context.Canceled {
		t.Fatal(err)
	}
	seen := pending.InspectOriginal()
	if seen.AuthorityMutationAttempted || seen.GrantCommitKnown || seen.Original.OpenedFDs != 0 {
		t.Fatal(seen)
	}
	failure := residentAuthorizationFailure(pending, selectors, "apply", err)
	var typed *residentAuthorizationError
	if !errors.As(failure, &typed) || typed.recovery.State != "not_applied" || typed.recovery.ThisCommandMutationAttempted || typed.api.Code != "resident_authorization_not_applied" {
		t.Fatal(failure)
	}
	var out, stderr bytes.Buffer
	a := app{home: home, ctx: ctx, out: &out, err: &stderr}
	if code := a.finishCommand(1, fmt.Errorf("wrapped: %w", failure)); code != 130 || !strings.Contains(stderr.String(), "resident_authorization_not_applied") || strings.Contains(stderr.String(), "resident_authorization_committed") {
		t.Fatal(code, stderr.String())
	}
}

func TestResidentAuthorizationNilResultLateCancellationKeepsActualContext(t *testing.T) {
	// Actual pure pending owner, no apply/positive issuer/measurement/grant.
	// This isolates shared finish's nil-error path; real committed late
	// cancellation still requires the SAME real issuer fixture later.
	home := filepath.Join(t.TempDir(), "selected custom home")
	selectors := residentAuthorizationSelectors{Home: home, Key: "original-nil-key", Expect: strings.Repeat("a", 64), Profile: filepath.Join(home, "profile"), Evidence: filepath.Join(home, "evidence"), Reason: "negative nil-error finish boundary"}
	pending, err := server.NewResidentOperatorPending(home, selectors.Profile, selectors.Evidence, selectors.Key, selectors.Reason)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var out, stderr bytes.Buffer
	a := app{home: home, ctx: ctx, out: &out, err: &stderr}
	if err := a.retainResidentAuthorization(pending, selectors, "completion"); err != nil {
		t.Fatal(err)
	}
	cancel() // after a hypothetical nil command result, before shared finish
	if code := a.finishCommand(0, nil); code != 130 {
		t.Fatal(code)
	}
	var result struct {
		Error    *model.Error                  `json:"error"`
		Recovery residentAuthorizationRecovery `json:"resident_authorization"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil || result.Error == nil || result.Error.Code != "resident_authorization_not_applied" || result.Recovery.residentAuthorizationSelectors != selectors || result.Recovery.ThisCommandMutationAttempted || result.Recovery.AutomaticRetry || !reflect.DeepEqual(result.Recovery.InspectArgv, selectors.inspectArgv()) {
		t.Fatal("nil result lost actual context", stderr.String(), err)
	}
	seen := pending.InspectOriginal()
	if seen.ApplyAttempted || seen.AuthorityMutationAttempted || seen.GrantCommitKnown || seen.Original.OpenedFDs != 0 || out.Len() != 0 {
		t.Fatal("reporting created authority/effects", seen, out.String())
	}
}

func TestResidentAuthorizationSuccessfulInspectionLateCancellationKeepsContext(t *testing.T) {
	// Real completed readonly command with absent authority; no valid journal
	// or positive authorization is fabricated to exercise successful nil return.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var out, stderr bytes.Buffer
	a := app{home: home, ctx: ctx, out: &out, err: &stderr}
	expect := strings.Repeat("a", 64)
	args := []string{"authorize", "inspect", "--profile", filepath.Join(home, "profile"), "--evidence", filepath.Join(home, "evidence"), "--key", "original-inspect-key", "--expect", expect, "--reason", "readonly finish boundary"}
	code, commandErr := a.residentCommand(args)
	if code != 0 || commandErr != nil || a.residentAuthorization == nil {
		t.Fatal(code, commandErr, stderr.String())
	}
	priorOutput := out.String()
	seen := a.residentAuthorization.pending.InspectOriginal()
	if !seen.InspectionCompleted || !seen.ReferencesClosed || seen.ApplyAttempted || seen.AuthorityMutationAttempted || seen.GrantCommitKnown {
		t.Fatal(seen)
	}
	cancel() // command already returned (0,nil); shared finish must retain context
	if code := a.finishCommand(code, commandErr); code != 130 {
		t.Fatal(code)
	}
	var result struct {
		Error    *model.Error                  `json:"error"`
		Recovery residentAuthorizationRecovery `json:"resident_authorization"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil || result.Error == nil || result.Error.Code != "resident_authorization_inspection_unavailable" || result.Recovery.Home != home || result.Recovery.Key != "original-inspect-key" || result.Recovery.Expect != expect || result.Recovery.AutomaticRetry || result.Recovery.ThisCommandMutationAttempted || out.String() != priorOutput {
		t.Fatal("successful nil return lost exact original context", out.String(), stderr.String(), err)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("readonly reporting created state", entries, err)
	}
}
