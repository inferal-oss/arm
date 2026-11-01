package residentservice

import "context"

// Bounded retained DATA only. This neither validates current native state nor
// creates a charge, Manager/native claim, grant or resource-release authority.
// Quantity remains charged when admission is unavailable or native work unknown.
type WorkerRetainedChargeData struct {
	Home                                   string
	HomeID                                 NativeID
	DebitID, ProfileSHA256, ContractSHA256 string
	Resources                              Resources
	Bounds                                 Bounds
	AdmissionAvailable                     bool
	OriginalOperationPending               bool
	NativeOutcome                          string
}

// Ordinary accounting must not start native current validation under Manager.mu.
// Actual binding first uses ObserveOriginal in its registered original Open call.
func (c *WorkerManagerCharge) ObserveRetainedOriginalData() (WorkerRetainedChargeData, error) {
	p, w, err := c.originalData()
	if err != nil {
		return WorkerRetainedChargeData{}, err
	}
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.charge != c || c.owner != w || !p.currentShapeLocked() || w.grant == nil || w.debit == nil || w.debit.record == nil {
		return WorkerRetainedChargeData{}, ErrInvalid
	}
	profile := w.grant.Intent.Profile.Core
	admitted := w.raw.active == nil && w.raw.firstError == nil && p.rootContext != nil && p.rootContext.Err() == nil
	return WorkerRetainedChargeData{Home: w.path, HomeID: w.grant.Intent.HomeID, DebitID: w.debit.record.ID, ProfileSHA256: w.debit.record.ProfileSHA256, ContractSHA256: w.debit.record.ContractSHA256, Resources: profile.Resources, Bounds: profile.Bounds, AdmissionAvailable: admitted, OriginalOperationPending: w.raw.active != nil, NativeOutcome: "unknown"}, nil
}

// Exact original core lifetime identity only; no context/cancel getter or issuer.
func (c *WorkerManagerCharge) RequireOriginalRootContext(ctx context.Context) error {
	return c.requireOriginalContextData(ctx)
}
