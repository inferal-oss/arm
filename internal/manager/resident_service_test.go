package manager

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

func TestResidentServiceCheckedTotalsDataKeepIntegerPrecisionAndOverflow(t *testing.T) {
	// Arithmetic DATA ONLY, never a measured profile, service binding or grant.
	jobs := int64(9007199254740993)
	if total, known := checkedStandingBytes(jobs, 7); !known || total != 9007199254741000 {
		t.Fatal(total, known)
	}
	if total, known := checkedStandingBytes(math.MaxInt64, 1); known || total != math.MaxInt64 {
		t.Fatal("integer overflow became free capacity", total, known)
	}
	if _, known := checkedStandingBytes(-1, 1); known {
		t.Fatal("negative outstanding promise accepted")
	}
	if _, known := checkedStandingCPU(math.MaxFloat64, math.MaxFloat64); known {
		t.Fatal("CPU overflow became capacity")
	}
	if _, known := checkedStandingCPU(math.NaN(), 1); known {
		t.Fatal("nonfinite job promise accepted")
	}
	if total, known := checkedStandingCPU(1, 2); !known || total != 3 {
		t.Fatal(total, known)
	}
}

func TestResidentServiceJobSubtotalChecksBeforePositiveWrap(t *testing.T) {
	// Synthetic accounting records, never admitted leases or service authority.
	leases := []model.Lease{
		{State: "active", Request: model.Request{Resources: model.Resources{Memory: math.MaxInt64 - 4}}},
		{State: "active", Request: model.Request{Resources: model.Resources{Memory: 8}}},
		{State: "active", Request: model.Request{Resources: model.Resources{Memory: math.MaxInt64}}},
	}
	var wrapped int64
	for _, lease := range leases {
		wrapped += lease.Request.Resources.Memory
	}
	if wrapped <= 0 {
		t.Fatal("data does not exercise a positive final wrap", wrapped)
	}
	if _, err := checkedResidentJobTotals(leases); err == nil {
		t.Fatal("wrapped positive subtotal became capacity")
	}
	if _, err := schedulingSnapshotWithResidentTotals(leases, true); err == nil {
		t.Fatal("resident scheduling accepted positive memory wrap")
	}
}

func TestResidentServiceUnboundOrZeroClaimCannotAdmit(t *testing.T) {
	o, err := NewResidentManagerPending(&lifetimeview.ManagerView{}, daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	m := o.manager
	if m.standingAdmissionError() == nil {
		t.Fatal("unbound resident admitted new work")
	}
	projection := m.standingProjection("cpu")
	if projection == nil || projection.State != "unbound" || projection.AdmissionAvailable || projection.Quantity != nil {
		t.Fatal(projection)
	}
	if err = o.BindOriginalStandingCharge(&serviceview.ManagerCharge{}); err == nil {
		t.Fatal("zero service claim accepted")
	}
	seen := o.InspectOriginal()
	if !seen.StandingChargeRetained || seen.StandingBindingCompleted || seen.FirstError != err || seen.OpenAttempted || seen.DatabaseRetained {
		t.Fatal(seen)
	}
	if second := o.OpenOriginal(t.Context()); second != err {
		t.Fatal("failed original service binding was retried/lost", second)
	}
	legacy := &Manager{}
	if legacy.standingAdmissionError() != nil || legacy.standingProjection("cpu") != nil {
		t.Fatal("legacy acquired a resident charge")
	}
}

func TestResidentServiceConsumerDataExactQuantityAndLegacyShape(t *testing.T) {
	// This optional DTO is serialized DATA, never an actual service capability.
	quantity := model.CountQuantity(9007199254740993)
	view := model.ResourceView{BudgetTotal: model.CountQuantity(0), Reserved: model.CountQuantity(0), Available: model.CountQuantity(0), ResidentService: &model.ResidentServiceConsumer{Quantity: &quantity, State: "unresolved", AdmissionAvailable: false}}
	data, err := json.Marshal(view)
	if err != nil || !strings.Contains(string(data), `"quantity":9007199254740993`) {
		t.Fatal(string(data), err)
	}
	if strings.Contains(string(data), `"lease"`) {
		t.Fatal("standing charge became a fabricated lease", string(data))
	}
	view.ResidentService = nil
	data, err = json.Marshal(view)
	if err != nil || strings.Contains(string(data), "resident_service") {
		t.Fatal("legacy shape changed", string(data), err)
	}
}
