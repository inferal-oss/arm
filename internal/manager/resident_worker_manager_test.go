package manager

import (
	"getarm.org/internal/model"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
	"testing"
)

func TestWorkerManagerPendingRetainsBeforeUnissuedChargeRefusal(t *testing.T) {
	o, err := NewResidentWorkerManagerPending(&lifetimeview.WorkerManagerView{}, residentNegativeSettings(), &serviceview.WorkerManagerCharge{})
	if err != nil {
		t.Fatal(err)
	}
	actual := o.manager
	if actual == nil || actual.resident != nil || actual.residentWorker != o || o.database != nil {
		t.Fatal("constructor entered another owner")
	}
	first := o.OpenOriginal(t.Context())
	if first == nil || o.manager != actual || o.database != nil || o.claim != nil || o.ready || o.started {
		t.Fatal("unissued charge reached native constructor")
	}
	if err = o.OpenOriginal(t.Context()); err != first {
		t.Fatal("failed original was retried")
	}
	if actual.Close() == nil {
		t.Fatal("legacy Close adopted worker Manager")
	}
}
func TestWorkerStandingUnknownMixedAndOrphanRetainPromiseData(t *testing.T) {
	m := &Manager{}
	o := &ResidentWorkerManagerPending{manager: m, charge: &serviceview.WorkerManagerCharge{}}
	o.self = o
	m.residentWorker = o
	original := model.Resources{CPU: 1, Memory: 2, Disk: 3}
	b := &residentWorkerServiceBinding{manager: m, claim: o.charge, held: original, validated: true, bound: true}
	m.residentWorkerService = b
	held, known, admission := m.standingService()
	if held != original || !known || admission || b.held != original {
		t.Fatal("unissued source erased promise or admitted")
	}
	old := &residentServiceBinding{manager: m, held: model.Resources{CPU: 2, Memory: 5, Disk: 7}, validated: true}
	m.resident = &ResidentManagerPending{}
	m.residentService = old
	held, known, admission = m.standingService()
	if held != (model.Resources{CPU: 3, Memory: 7, Disk: 10}) || !known || admission || b.held != original || old.held.CPU != 2 {
		t.Fatal("mixed promises changed or admitted")
	}
	m.resident = nil
	m.residentService = nil
	m.residentWorker = nil
	held, known, admission = m.standingService()
	if held != original || !known || admission || !m.hasResidentOwner() {
		t.Fatal("orphan worker binding created capacity")
	}
	m.residentWorkerService = nil
	m.residentService = old
	held, known, admission = m.standingService()
	if held != old.held || !known || admission || !m.hasResidentOwner() {
		t.Fatal("orphan legacy binding created capacity")
	}
	if m.Close() == nil {
		t.Fatal("orphan binding reached legacy Close")
	}
}
func TestWorkerManagerMetadataGuardCannotStartLegacyLoops(t *testing.T) {
	m := &Manager{residentWorker: &ResidentWorkerManagerPending{}}
	returned := false
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("legacy Start did not refuse")
			}
		}()
		m.Start()
		returned = true
	}()
	if returned || m.db != nil {
		t.Fatal("legacy loops or database started")
	}
}
