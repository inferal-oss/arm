package server

import (
	"context"

	"getarm.org/internal/manager"
	"getarm.org/internal/server/lifetimeview"
)

type residentPreparedServerData struct {
	Native          lifetimeview.Snapshot
	SettingsSHA256  string
	WorkspaceFormat string
}
type residentServerPreparedCall struct {
	owner       *residentServerPending
	op          *residentServerOperation
	view        *lifetimeview.ServerView
	manager     *manager.ResidentManagerPending
	borrower    *manager.ResidentManagerServerBinding
	native      lifetimeview.Snapshot
	managerData manager.ResidentPreparedManagerData
	returned    bool
	resultError error
}

// Actual original, OPENED but UNSTARTED Server and its one real Manager borrower.
// This does not borrow ManagerForOriginalServer or validate ingress readiness.
func (o *residentServerPending) validatePreparedOriginal(ctx context.Context, view *lifetimeview.ServerView, originalManager *manager.ResidentManagerPending) (data residentPreparedServerData, resultErr error) {
	if o == nil || o.self != o || ctx == nil {
		return data, errResidentServerOriginal
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	o.mu.Lock()
	valid := view != nil && o.view == view && originalManager != nil && o.manager == originalManager && o.claim != nil && o.managerBound != nil && o.server != nil && o.server.resident == o && o.server.Manager == nil && o.binding != nil && o.binding.owner == o && o.binding.server == o.server && o.binding.claim == o.claim
	o.mu.Unlock()
	if !valid {
		return data, errResidentServerOriginal
	}
	op, err := o.begin("validate_prepared")
	if err != nil {
		return data, err
	}
	normal := false
	defer o.finish(op, &normal, &resultErr)
	call := &residentServerPreparedCall{owner: o, op: op, view: view, manager: originalManager, borrower: o.managerBound}
	o.mu.Lock()
	o.preparedValidation = call
	o.mu.Unlock()
	o.phase("prepared_original_Server_claim_pending")
	call.native, resultErr = o.claim.ValidateOriginalContext(ctx)
	if resultErr == nil && (!call.native.InheritedFence || call.native != o.original || o.server.home != call.native.Path || o.server.generation != call.native.Generation) {
		resultErr = errResidentServerOriginal
	}
	if resultErr == nil {
		o.phase("prepared_same_original_Manager_Server_borrower")
		call.managerData, resultErr = originalManager.OriginalPreparedBorrowerData(call.borrower, o.claim)
		if resultErr == nil && call.managerData.Native != call.native {
			resultErr = errResidentServerOriginal
		}
	}
	if resultErr == nil {
		resultErr = ctx.Err()
	}
	if resultErr == nil {
		data = residentPreparedServerData{Native: call.native, SettingsSHA256: call.managerData.SettingsSHA256, WorkspaceFormat: call.managerData.WorkspaceFormat}
	}
	o.mu.Lock()
	call.returned, call.resultError = true, resultErr
	o.mu.Unlock()
	normal = true
	return data, resultErr
}
