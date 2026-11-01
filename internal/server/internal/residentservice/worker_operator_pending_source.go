package residentservice

import (
	"encoding/json"

	"getarm.org/internal/residentcost"
)

// Only the trusted compiled server assembly supplies its complete source
// description. This copies bounded DATA before native Plan; it issues no grant,
// evidence success, parent/child charge or native capability. The actual plan
// checks it against current binary and exact committed evidence and displays it.
func (p *WorkerOperatorPending) BindCompiledPendingSourceData(source residentcost.PendingSourceContractData) error {
	if p == nil || p.self != p || p.custody == nil || p.custody.raw == nil {
		return ErrInvalid
	}
	if _, err := source.FingerprintData(); err != nil {
		return ErrInvalid
	}
	raw, err := json.Marshal(source)
	if err != nil || len(raw) > residentcost.MaxLedgerBytes {
		return ErrInvalid
	}
	w := p.custody
	w.raw.mu.Lock()
	defer w.raw.mu.Unlock()
	if p.requiredSource != nil || p.planTried || p.inspectTried || p.applyTried || w.raw.active != nil || w.raw.firstError != nil || w.operator != p || !w.typedOriginalLocked() || !w.legacyEmptyLocked() {
		return ErrInvalid
	}
	p.requiredSourceRaw = raw
	p.requiredSource = &residentcost.PendingSourceContractData{}
	if json.Unmarshal(raw, p.requiredSource) != nil {
		return ErrInvalid
	}
	return nil
}

func (p *WorkerOperatorPending) attachCompletePendingReviewData() error {
	if p.requiredSource == nil || p.plan == nil || p.profile == nil || p.evidence == nil || p.plan.PendingStartupReview != nil {
		return ErrInvalid
	}
	// Preserve the existing16KiB BASE output bound before adding this separate
	// typed field. Profile/evidence/journal/control input caps are unchanged.
	base, err := json.Marshal(p.plan)
	if err != nil || len(base) > MaxProfileBytes {
		return ErrInvalid
	}
	review, err := workerPendingStartupReviewData(*p.profile, *p.evidence, *p.requiredSource)
	if err != nil {
		return err
	}
	required, err := compiledWorkerStructuralRequiredData()
	if err != nil || residentcost.CheckBudgetData(required, review.CheckedTotals.Resources, review.CheckedTotals.Bounds) != nil {
		return ErrInvalid
	}
	p.plan.PendingStartupReview = &review
	encoded, err := json.Marshal(p.plan)
	if err != nil || len(encoded) > MaxWorkerOperatorPlanBytes {
		return ErrInvalid
	}
	return nil
}
