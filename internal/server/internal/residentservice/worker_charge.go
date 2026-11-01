package residentservice

import "context"

// Nonclosing original standing charge. No exported field or constructor can
// create one from a profile, debit record, copied observation or native ID.
type WorkerManagerCharge struct {
	self  *WorkerManagerCharge
	child *WorkerChildPending
	owner *workerCustody
}

type WorkerChargeObservation struct {
	ProfileSHA256            string    `json:"profile_sha256"`
	ContractSHA256           string    `json:"contract_sha256"`
	DebitID                  string    `json:"debit_id"`
	HomeID                   NativeID  `json:"home_id"`
	Resources                Resources `json:"resources"`
	Bounds                   Bounds    `json:"bounds"`
	CurrentOriginalValidated bool      `json:"current_original_validated"`
	NativeOutcome            string    `json:"native_outcome"`
}

func (p *WorkerChildPending) ClaimOriginalWorkerManagerCharge() (charge *WorkerManagerCharge, resultErr error) {
	op, err := p.beginOriginal("worker-child-charge")
	if err != nil {
		return nil, err
	}
	normal := false
	defer p.custody.raw.finish(op, &normal, &resultErr)
	resultErr = p.currentBodyOriginal(p.rootContext, true)
	if resultErr == nil {
		charge = &WorkerManagerCharge{child: p, owner: p.custody}
		charge.self = charge
		p.custody.raw.mu.Lock()
		p.charge = charge
		p.custody.raw.mu.Unlock()
	}
	normal = true
	return charge, resultErr
}

func (c *WorkerManagerCharge) originalData() (*WorkerChildPending, *workerCustody, error) {
	if c == nil || c.self != c || c.child == nil || c.child.self != c.child || c.owner == nil || c.owner.self != c.owner || c.child.custody != c.owner || c.owner.child != c.child || c.owner.raw == nil {
		return nil, nil, ErrInvalid
	}
	w := c.owner
	w.raw.mu.Lock()
	valid := c.child.charge == c && c.child.runtimeBound && c.child.currentShapeLocked()
	w.raw.mu.Unlock()
	if !valid {
		return nil, nil, ErrInvalid
	}
	return c.child, w, nil
}

func (c *WorkerManagerCharge) ObserveOriginal() (observation WorkerChargeObservation, resultErr error) {
	p, w, err := c.originalData()
	if err != nil {
		return observation, err
	}
	op, err := p.beginOriginal("worker-charge-current")
	if err != nil {
		return observation, err
	}
	normal := false
	defer w.raw.finish(op, &normal, &resultErr)
	resultErr = p.currentBodyOriginal(p.rootContext, true)
	if resultErr == nil {
		w.raw.mu.Lock()
		observation = WorkerChargeObservation{ProfileSHA256: w.debit.record.ProfileSHA256, ContractSHA256: w.debit.record.ContractSHA256, DebitID: w.debit.record.ID, HomeID: w.grant.Intent.HomeID, Resources: w.grant.Intent.Profile.Core.Resources, Bounds: w.grant.Intent.Profile.Core.Bounds, CurrentOriginalValidated: true, NativeOutcome: "unknown"}
		w.raw.mu.Unlock()
	}
	normal = true
	return observation, resultErr
}

// The real composed Manager must supply its COMPLETE checked disjoint cost
// ledger, including core/runtime/DB/parser/image/assets/native and retained
// unknowns. The fixed worker floor alone is not that ledger, RSS or funding.
// This comparison returns no Manager/native claim and performs no side effect
// that starts helpers, opens a database, credits capacity or releases a debit.
func (c *WorkerManagerCharge) CheckOriginalRuntimeBounds(required Bounds) error {
	_, w, err := c.originalData()
	if err != nil {
		return err
	}
	w.raw.mu.Lock()
	profile := w.grant.Intent.Profile
	w.raw.mu.Unlock()
	floor, err := profile.Contract.LogicalFloorData()
	if err != nil {
		return err
	}
	if runtimeBoundsSufficientData(required, floor) != nil {
		return ErrInvalid
	}
	seen, err := c.ObserveOriginal()
	if err != nil {
		return err
	}
	if !seen.CurrentOriginalValidated || seen.ProfileSHA256 == "" || seen.ContractSHA256 == "" {
		return ErrInvalid
	}
	return runtimeBoundsSufficientData(seen.Bounds, required)
}

// Current lifetime identity is private DATA checking only. The original root
// context is never replaced by an API request's cancellation lifetime.
func (c *WorkerManagerCharge) requireOriginalContextData(ctx context.Context) error {
	p, _, err := c.originalData()
	if err != nil {
		return err
	}
	if !sameWorkerContextData(ctx, p.rootContext) {
		return ErrInvalid
	}
	return nil
}
