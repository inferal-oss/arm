package manager

import (
	"encoding/json"
	"math"

	"getarm.org/internal/model"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

type residentServiceBinding struct {
	home                  string
	homeUID               uint32
	homeDevice, homeInode uint64
	manager               *Manager
	claim                 *serviceview.ManagerCharge
	held                  model.Resources
	debit, profile        string
	validated             bool
	bound                 bool
}

// Only the actual retained root binds its original nonretiring charge, before
// Manager opening/loops. This accepts no record, DTO, callback or funded bool.
// Constructor fixtures remain externally funded; an unbound resident Manager
// cannot admit new work through the private/public ordinary admission paths.
func (o *ResidentManagerPending) BindOriginalStandingCharge(claim *serviceview.ManagerCharge) (resultErr error) {
	if o == nil || o.self != o || claim == nil {
		return errResidentManagerOriginal
	}
	op, err := o.beginOriginal("service_binding")
	if err != nil {
		return err
	}
	normal := false
	defer o.finishOriginal(op, &normal, &resultErr)
	resultErr = o.bindStandingChargeBody(claim)
	normal = true
	return resultErr
}
func (o *ResidentManagerPending) bindStandingChargeBody(claim *serviceview.ManagerCharge) error {
	m := o.manager
	if m == nil || m.resident != o {
		return errResidentManagerOriginal
	}
	m.mu.Lock()
	if m.residentService != nil {
		m.mu.Unlock()
		return errResidentManagerOriginal
	}
	binding := &residentServiceBinding{manager: m, claim: claim}
	m.residentService = binding
	m.mu.Unlock()
	o.mu.Lock()
	o.standingChargeRetained = true
	o.mu.Unlock()
	observed, err := claim.ObserveOriginal() // bounded Go registry only, never native/DB
	vector := model.Resources{CPU: observed.Resources.CPU, Memory: observed.Resources.Memory, Disk: observed.Resources.Disk}
	if err == nil && (!observed.AdmissionAvailable || !validStandingVector(vector) || observed.DebitID == "" || observed.ProfileSHA256 == "") {
		err = model.Err("resident_service_unavailable", "original service charge cannot authorize new admission")
	}
	m.mu.Lock()
	if validStandingVector(vector) {
		binding.held = vector
		binding.home = observed.Home
		binding.homeUID = observed.HomeID.UID
		binding.homeDevice = observed.HomeID.Device
		binding.homeInode = observed.HomeID.Inode
		binding.debit, binding.profile = observed.DebitID, observed.ProfileSHA256
		binding.validated = true
	}
	binding.bound = err == nil
	m.mu.Unlock()
	o.mu.Lock()
	o.standingBindingCompleted = err == nil
	o.mu.Unlock()
	return err
}

func validStandingVector(vector model.Resources) bool {
	return finiteCPU(vector.CPU) && vector.CPU > 0 && vector.Memory > 0 && vector.Disk > 0
}

// Callers hold Manager.mu or run before any Manager pointer/loop is published.
// A failure never zeroes a prior full promise; it only closes NEW admission.
func (m *Manager) standingService() (held model.Resources, quantityKnown, admission bool) {
	if m.resident == nil {
		return held, true, true
	}
	b := m.residentService
	if b == nil || b.manager != m || !b.validated || !validStandingVector(b.held) {
		return held, false, false
	}
	held, quantityKnown = b.held, true
	if !b.bound {
		return held, true, false
	}
	observed, err := b.claim.ObserveOriginal()
	if err != nil || !observed.AdmissionAvailable || observed.DebitID != b.debit || observed.ProfileSHA256 != b.profile {
		return held, true, false
	}
	if observed.Resources.CPU != held.CPU || observed.Resources.Memory != held.Memory || observed.Resources.Disk != held.Disk {
		return held, true, false
	}
	return held, true, true
}

func (m *Manager) standingAdmissionError() error {
	_, _, available := m.standingService()
	if !available {
		return model.Err("resident_service_unavailable", "original resident standing charge is unresolved; existing obligations remain retained")
	}
	return nil
}

func checkedStandingCPU(jobs, service float64) (float64, bool) {
	if !finiteCPU(jobs) || jobs < 0 || !finiteCPU(service) || service < 0 {
		return math.MaxFloat64, false
	}
	total := jobs + service
	if !finiteCPU(total) || total < jobs || total < service {
		return math.MaxFloat64, false
	}
	return total, true
}
func checkedStandingBytes(jobs, service int64) (int64, bool) {
	if jobs < 0 || service < 0 || service > math.MaxInt64-jobs {
		return math.MaxInt64, false
	}
	return jobs + service, true
}

// Check EACH resident job addition before any service addition. A wrapped job
// subtotal can become positive again; checking only its sign later is unsafe.
// Default-disk promises have their separate checked/backing-aware accumulator.
func checkedResidentJobAdd(subtotal, addition model.Resources) (model.Resources, error) {
	cpu, cpuOK := checkedStandingCPU(subtotal.CPU, addition.CPU)
	memory, memoryOK := checkedStandingBytes(subtotal.Memory, addition.Memory)
	if !cpuOK || !memoryOK {
		return subtotal, model.Err("invalid_accounting", "resident job CPU/memory promise subtotal overflows or is invalid; no new capacity is assumed")
	}
	return model.Resources{CPU: cpu, Memory: memory}, nil
}

func checkedResidentJobTotals(leases []model.Lease) (model.Resources, error) {
	var total model.Resources
	for _, lease := range leases {
		if !consuming(lease.State) {
			continue
		}
		var err error
		total, err = checkedResidentJobAdd(total, lease.Request.Resources)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
func (m *Manager) standingCPUReserved(jobs float64) (float64, bool) {
	if m.resident == nil {
		return jobs, true
	} // exact legacy input semantics
	held, known, _ := m.standingService()
	total, valid := checkedStandingCPU(jobs, held.CPU)
	return total, known && valid
}
func (m *Manager) standingMemoryReserved(jobs int64) (int64, bool) {
	if m.resident == nil {
		return jobs, true
	} // exact legacy input semantics
	held, known, _ := m.standingService()
	total, valid := checkedStandingBytes(jobs, held.Memory)
	return total, known && valid
}
func (m *Manager) standingDiskReserved(jobs int64) (int64, bool) {
	if m.resident == nil {
		return jobs, true
	} // exact legacy input semantics
	held, known, _ := m.standingService()
	total, valid := checkedStandingBytes(jobs, held.Disk)
	return total, known && valid
}

func (m *Manager) standingProjection(kind string) *model.ResidentServiceConsumer {
	if m.resident == nil {
		return nil
	}
	held, known, admission := m.standingService()
	view := &model.ResidentServiceConsumer{State: "unbound", AdmissionAvailable: admission}
	if b := m.residentService; b != nil && b.manager == m {
		view.Debit = b.debit
		view.Profile = b.profile
	}
	if !known {
		return view
	}
	view.State = "retained"
	if !admission {
		view.State = "unresolved"
	}
	var quantity json.Number
	switch kind {
	case "cpu":
		quantity = model.FloatQuantity(held.CPU)
	case "memory":
		quantity = model.CountQuantity(held.Memory)
	case "disk", "storage":
		quantity = model.CountQuantity(held.Disk)
	default:
		return nil
	}
	view.Quantity = &quantity
	return view
}

func (m *Manager) standingSharedDisk(name string) bool {
	return name == "" || name == "default" || m.defaultStorageLocation.Backing != "" && m.storageBacking(name) == m.defaultStorageLocation.Backing
}

// Called after the actual original native Manager claim validates, BEFORE DB
// construction. The same service charge cannot finance another home's Manager.
func (m *Manager) validateOriginalStandingHome(original lifetimeview.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.residentService
	if b == nil {
		return nil
	} // external constructor fixtures: new admission remains blocked
	if !b.bound || b.manager != m || b.home != original.Path || b.homeUID != original.UID || b.homeDevice != original.Device || b.homeInode != original.Inode {
		b.bound = false
		return model.Err("resident_service_unavailable", "standing charge and original Manager native home differ; retain original custody")
	}
	return nil
}
