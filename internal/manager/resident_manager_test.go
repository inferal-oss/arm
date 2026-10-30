package manager

import (
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
	"getarm.org/internal/server/lifetimeview"
)

func residentNegativeSettings() daemonconfig.Settings {
	// A pure configuration fixture, never an operator service grant or lease.
	return daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"}
}

func TestResidentManagerPendingRetainsActualManagerBeforeRejectedClaim(t *testing.T) {
	o, err := NewResidentManagerPending(&lifetimeview.ManagerView{}, residentNegativeSettings())
	if err != nil {
		t.Fatal(err)
	}
	before := o.InspectOriginal()
	if !before.ManagerRetained || before.OpenAttempted || before.ClaimRetained || before.DatabaseRetained || before.Initialized || before.NativeOutcome != "unknown" {
		t.Fatal(before)
	}
	actual := o.manager
	first := o.OpenOriginal(t.Context())
	if first == nil {
		t.Fatal("zero native view accepted")
	}
	after := o.InspectOriginal()
	if o.manager != actual || !after.ManagerRetained || !after.OpenAttempted || after.DatabaseRetained || after.Initialized || after.OperationPending || after.FirstError != first {
		t.Fatal(after)
	}
	if err = o.OpenOriginal(t.Context()); err != first {
		t.Fatal("failed original constructor was retried or lost its first error", err)
	}
	if err = o.StartOriginal(); err != first {
		t.Fatal("failed Manager was started", err)
	}
	if err = actual.Close(); err == nil {
		t.Fatal("legacy Close accepted resident Manager")
	}
	if actual.db != nil {
		t.Fatal("rejected native claim reached database construction")
	}
}

func TestResidentManagerCopiedHolderHasNoAuthority(t *testing.T) {
	o, err := NewResidentManagerPending(&lifetimeview.ManagerView{}, residentNegativeSettings())
	if err != nil {
		t.Fatal(err)
	}
	copy := &ResidentManagerPending{self: o, view: o.view, manager: o.manager}
	if err = copy.OpenOriginal(t.Context()); err != errResidentManagerOriginal {
		t.Fatal(err)
	}
	if _, err = copy.ManagerForOriginalServer(nil); err != errResidentManagerOriginal {
		t.Fatal(err)
	}
	if o.InspectOriginal().OpenAttempted {
		t.Fatal("copied holder started original construction")
	}
}

// Resident lifetime selection does not activate the separately reserved sync3
// scope or change the ordinary supported sync admission path.
func TestResidentManagerPendingKeepsOrdinarySyncSchema(t *testing.T) {
	o, err := NewResidentManagerPending(&lifetimeview.ManagerView{}, residentNegativeSettings())
	if err != nil {
		t.Fatal(err)
	}
	if o.manager.syncSchemaVersion != "2" {
		t.Fatal("resident construction selected a dedicated unsupported sync schema", o.manager.syncSchemaVersion)
	}
	if o.InspectOriginal().OpenAttempted || o.manager.db != nil {
		t.Fatal("pure constructor selection performed opening work")
	}
}
