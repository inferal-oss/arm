package meshops

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yrashk/arm/internal/local"
	"github.com/yrashk/arm/internal/mesh"
	"github.com/yrashk/arm/internal/model"
)

type settlementMarshalFailure struct{ cause error }

func (v settlementMarshalFailure) MarshalJSON() ([]byte, error) { return nil, v.cause }

func settlementCoordinator(t *testing.T) (*Coordinator, *int) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "state")
	if err := local.Prepare(home); err != nil {
		t.Fatal(err)
	}
	guard, err := mesh.ClaimCoordination(home, "outcome", "connect", strings.Repeat("a", 64), "ssh:fixture")
	if err != nil {
		t.Fatal(err)
	}
	printed := new(int)
	return &Coordinator{Context: context.Background(), Home: home, guard: guard,
		Confirm: func(any, bool, bool) error { return nil },
		Print:   func(any) error { *printed++; return nil }}, printed
}

func requireSettlementFailure(t *testing.T, code int, err error, stage string, printed int) *settlementFailure {
	t.Helper()
	var failure *settlementFailure
	var api *model.Error
	if code != 1 || !errors.As(err, &failure) || failure.stage != stage {
		t.Fatalf("expected incomplete settlement at %s: code=%d error=%v", stage, code, err)
	}
	if !errors.As(err, &api) || api.Code != "mesh_operation_incomplete" || printed != 0 {
		t.Fatal("settlement failure exposed a result or replaced the incomplete API contract", api, printed)
	}
	return failure
}

func TestSettlementFailurePreservesMarshalCauseAndWithholdsOutput(t *testing.T) {
	a, printed := settlementCoordinator(t)
	before, _, err := mesh.ReadCoordination(a.Home, "outcome")
	if err != nil {
		t.Fatal(err)
	}
	cause := model.Err("fixture_native_failure", "private fixture data must not appear in public errors")
	code, err := a.run(func() (int, error) {
		if err := a.Print(settlementMarshalFailure{cause}); err != nil {
			t.Fatal(err)
		}
		return 0, nil
	})
	failure := requireSettlementFailure(t, code, err, "outcome-save", *printed)
	if !errors.Is(failure.SettlementCause(), cause) {
		t.Fatal("native marshal failure was discarded", err)
	}
	var api *model.Error
	if !errors.As(err, &api) {
		t.Fatal(err)
	}
	public, marshalErr := json.Marshal(api)
	if marshalErr != nil || strings.Contains(string(public), "private fixture") || strings.Contains(err.Error(), "private fixture") {
		t.Fatal("public error leaked its underlying error text", string(public), err)
	}
	after, _, readErr := mesh.ReadCoordination(a.Home, "outcome")
	if readErr != nil || !reflect.DeepEqual(before, after) || after.Worker == "" {
		t.Fatal("failed result encoding changed the durable worker obligation", after, readErr)
	}
}

func TestSettlementFailureDoesNotPropagateNativeControlSentinels(t *testing.T) {
	for _, cause := range []error{flag.ErrHelp, context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			a, printed := settlementCoordinator(t)
			code, err := a.run(func() (int, error) {
				return 0, a.Print(settlementMarshalFailure{cause})
			})
			failure := requireSettlementFailure(t, code, err, "outcome-save", *printed)
			if errors.Is(err, cause) || !errors.Is(failure.SettlementCause(), cause) {
				t.Fatal("native control sentinel escaped the explicit diagnostic boundary", err)
			}
		})
	}
}

func TestSettlementFailureIdentifiesLostWorkerBeforeSaving(t *testing.T) {
	a, printed := settlementCoordinator(t)
	code, err := a.run(func() (int, error) {
		if err := a.guard.Change(func(op *mesh.Coordination) error {
			op.Phase, op.Worker = "incomplete", ""
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return 0, a.Print(map[string]bool{"done": true})
	})
	failure := requireSettlementFailure(t, code, err, "worker-read", *printed)
	var native *model.Error
	if !errors.As(failure.cause, &native) || native.Code != "mesh_operation_changed" {
		t.Fatal("lost-worker cause was discarded", failure.cause)
	}
	op, _, readErr := mesh.ReadCoordination(a.Home, "outcome")
	if readErr != nil || op.Phase != "incomplete" || len(op.Result) != 0 {
		t.Fatal("lost worker was overwritten by a successful result", op, readErr)
	}
}

func TestSettlementFailureRetainsCompletedChildWhenParentResolutionFails(t *testing.T) {
	a, printed := settlementCoordinator(t)
	if err := a.guard.Change(func(op *mesh.Coordination) error {
		op.Phase, op.Worker = "incomplete", ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	child, err := mesh.ClaimCoordinationAttempt(a.Home, "outcome", "ssh:fixture")
	if err != nil {
		t.Fatal(err)
	}
	a.guard = child
	nativePath, err := mesh.LifecycleOperationPath(a.Home, "outcome")
	if err != nil {
		t.Fatal(err)
	}
	parentPath := filepath.Join(filepath.Dir(nativePath), "coordinator-"+filepath.Base(nativePath))
	original, err := os.ReadFile(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	code, err := a.run(func() (int, error) {
		// Remove only this test's parent name after the child was admitted. The
		// retained original bytes demonstrate that failure does not replay work.
		if err := os.Rename(parentPath, parentPath+".retained"); err != nil {
			t.Fatal(err)
		}
		return 0, a.Print(map[string]bool{"done": true})
	})
	requireSettlementFailure(t, code, err, "parent-resolution", *printed)
	op, ok, readErr := mesh.ReadCoordination(a.Home, child.Key())
	if readErr != nil || !ok || op.Phase != "completed" || op.Worker != "" || op.ParentKey != "outcome" || len(op.Result) == 0 {
		t.Fatal("durably completed child was erased or relabelled", op, readErr)
	}
	retained, readErr := os.ReadFile(parentPath + ".retained")
	if readErr != nil || string(original) != string(retained) {
		t.Fatal("original parent evidence changed", readErr)
	}
}
