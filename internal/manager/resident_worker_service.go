package manager

import (
	"encoding/json"
	"getarm.org/internal/model"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

type residentWorkerServiceBinding struct {
	manager                  *Manager
	claim                    *serviceview.WorkerManagerCharge
	home                     string
	homeUID                  uint32
	homeDevice, homeInode    uint64
	debit, profile, contract string
	held                     model.Resources
	validated, bound         bool // retained quantity known; actual original native binding complete
}

// Only original Open calls this; binding and immutable quantity precede native
// work. The Manager publication mutex is never held through native Observe.
func (o *ResidentWorkerManagerPending) bindStandingChargeBody(claim *serviceview.WorkerManagerCharge) error {
	if o == nil || o.self != o || claim == nil || claim != o.charge || o.manager == nil {
		return errResidentWorkerManagerOriginal
	}
	o.mu.Lock()
	valid := o.active != nil && o.active.owner == o && o.active.kind == "open"
	o.mu.Unlock()
	if !valid {
		return errResidentWorkerManagerOriginal
	}
	m := o.manager
	m.mu.Lock()
	if m.residentWorker != o || m.resident != nil || m.residentService != nil || m.residentWorkerService != nil {
		m.mu.Unlock()
		return errResidentWorkerManagerOriginal
	}
	b := &residentWorkerServiceBinding{manager: m, claim: claim}
	m.residentWorkerService = b
	m.mu.Unlock()
	o.mu.Lock()
	o.standingChargeRetained = true
	o.mu.Unlock()
	retained, err := claim.ObserveRetainedOriginalData()
	if err != nil {
		return err
	}
	vector := model.Resources{CPU: retained.Resources.CPU, Memory: retained.Resources.Memory, Disk: retained.Resources.Disk}
	if !validStandingVector(vector) || retained.DebitID == "" || retained.ProfileSHA256 == "" || retained.ContractSHA256 == "" {
		return errResidentWorkerManagerOriginal
	}
	m.mu.Lock()
	b.held = vector
	b.home = retained.Home
	b.homeUID = retained.HomeID.UID
	b.homeDevice = retained.HomeID.Device
	b.homeInode = retained.HomeID.Inode
	b.debit = retained.DebitID
	b.profile = retained.ProfileSHA256
	b.contract = retained.ContractSHA256
	b.validated = true
	m.mu.Unlock()
	// This native effect is registered by the actual charge and is OUTSIDE m.mu.
	current, err := claim.ObserveOriginal()
	if err == nil && (!current.CurrentOriginalValidated || current.HomeID != retained.HomeID || current.DebitID != b.debit || current.ProfileSHA256 != b.profile || current.ContractSHA256 != b.contract || current.Resources != retained.Resources) {
		err = errResidentWorkerManagerOriginal
	}
	m.mu.Lock()
	b.bound = err == nil
	m.mu.Unlock()
	o.mu.Lock()
	o.standingBindingCompleted = err == nil
	o.mu.Unlock()
	return err
}

// Manager.mu held, or before publishing the Manager. Native work is forbidden.
// Unknowns never erase the original binding or its immutable full promise.
func (m *Manager) workerStandingService() (held model.Resources, known, admission bool) {
	b := m.residentWorkerService
	if b == nil || b.manager != m || !b.validated || !validStandingVector(b.held) {
		return held, false, false
	}
	held = b.held
	if m.resident != nil || m.residentService != nil {
		// Mixed owners never admit. Preserve/aggregate known old promises as DATA;
		// unknown/overflow remains unknown, with both original records retained.
		old := m.residentService
		if old == nil || old.manager != m || !old.validated || !validStandingVector(old.held) {
			return held, false, false
		}
		cpu, cok := checkedStandingCPU(held.CPU, old.held.CPU)
		memory, mok := checkedStandingBytes(held.Memory, old.held.Memory)
		disk, dok := checkedStandingBytes(held.Disk, old.held.Disk)
		if !cok || !mok || !dok {
			return held, false, false
		}
		return model.Resources{CPU: cpu, Memory: memory, Disk: disk}, true, false
	}
	if m.residentWorker == nil || m.residentWorker.self != m.residentWorker || m.residentWorker.manager != m || m.residentWorker.charge != b.claim {
		return held, true, false
	}
	if !b.bound {
		return held, true, false
	}
	current, err := b.claim.ObserveRetainedOriginalData()
	if err != nil || !current.AdmissionAvailable || current.Home != b.home || current.HomeID.UID != b.homeUID || current.HomeID.Device != b.homeDevice || current.HomeID.Inode != b.homeInode || current.DebitID != b.debit || current.ProfileSHA256 != b.profile || current.ContractSHA256 != b.contract || current.Resources.CPU != held.CPU || current.Resources.Memory != held.Memory || current.Resources.Disk != held.Disk {
		return held, true, false
	}
	return held, true, true
}
func (m *Manager) workerStandingProjection(kind string) *model.ResidentServiceConsumer {
	held, known, admission := m.workerStandingService()
	v := &model.ResidentServiceConsumer{State: "unresolved", AdmissionAvailable: admission}
	if b := m.residentWorkerService; b != nil && b.manager == m {
		v.Debit = b.debit
		v.Profile = b.profile
	}
	if !known {
		return v
	}
	v.State = "retained"
	if !admission {
		v.State = "unresolved"
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
	v.Quantity = &quantity
	return v
}
func (m *Manager) validateOriginalWorkerStandingHome(original lifetimeview.WorkerSnapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.residentWorkerService
	if m.resident != nil || m.residentService != nil || m.residentWorker == nil || b == nil || b.manager != m || !b.bound || b.claim != m.residentWorker.charge {
		return errResidentWorkerManagerOriginal
	}
	if b.home != original.Path || b.homeUID != original.UID || b.homeDevice != original.Device || b.homeInode != original.Inode || b.debit != original.Birth.DebitID || b.profile != original.Birth.ProfileSHA256 || b.contract != original.Birth.ContractSHA256 {
		b.bound = false
		return model.Err("resident_service_unavailable", "worker standing charge and original native Manager home differ; retain original custody")
	}
	return nil
}
