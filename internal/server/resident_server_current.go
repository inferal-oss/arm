package server

import (
	"context"

	"getarm.org/internal/manager"
	"getarm.org/internal/server/challengeview"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

type residentCurrentServerData struct {
	Native                          lifetimeview.Snapshot
	SettingsSHA256, WorkspaceFormat string
}
type residentServerCurrentCall struct {
	owner                                     *residentServerPending
	op                                        *residentServerOperation
	ctx                                       context.Context
	prepared                                  *residentServerPreparedCall
	view                                      *lifetimeview.ServerView
	challenge                                 *challengeview.CurrentChallengeView
	charge                                    *serviceview.ManagerCharge
	manager                                   *manager.ResidentManagerPending
	borrower                                  *manager.ResidentManagerServerBinding
	viewAttempted, viewReturned               bool
	viewData                                  challengeview.CurrentChallengeData
	viewError                                 error
	nativeAttempted, nativeReturned           bool
	native                                    lifetimeview.Snapshot
	nativeError                               error
	managerAttempted, managerReturned         bool
	managerData                               manager.ResidentCurrentManagerData
	managerError                              error
	viewRecheckAttempted, viewRecheckReturned bool
	viewRecheck                               challengeview.CurrentChallengeData
	viewRecheckError                          error
	data                                      residentCurrentServerData
	normalReturned                            bool
	resultError                               error
}

func (o *residentServerPending) currentPreparedLocked(view *lifetimeview.ServerView) bool {
	p := o.preparedValidation
	return o.firstError == nil && o.opened && !o.startTried && !o.started && view != nil && view == o.view && o.claim != nil && o.manager != nil && o.managerBound != nil && o.server != nil && o.server.resident == o && o.server.Manager == nil && o.binding != nil && o.binding.owner == o && o.binding.server == o.server && o.binding.claim == o.claim && o.preparedTried && p != nil && p.owner == o && p.op != nil && p.op.owner == o && p.op.kind == "validate_prepared" && p.op.normalFinished && !p.op.abnormal && p.returned && p.resultError == nil && p.view == view && p.manager == o.manager && p.borrower == o.managerBound
}
func (o *residentServerPending) beginCurrentOriginal(ctx context.Context, view *lifetimeview.ServerView, challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge) (*residentServerCurrentCall, error) {
	if o == nil || o.self != o || ctx == nil || challenge == nil || charge == nil {
		return nil, errResidentServerOriginal
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	valid := o.active == nil && o.currentPreparedLocked(view)
	o.mu.Unlock()
	if !valid {
		return nil, errResidentServerOriginal
	}
	op, err := o.begin("validate_current")
	if err != nil {
		return nil, err
	}
	c := &residentServerCurrentCall{owner: o, op: op, ctx: ctx, prepared: o.preparedValidation, view: view, challenge: challenge, charge: charge, manager: o.manager, borrower: o.managerBound}
	o.mu.Lock()
	o.currentValidation = c
	o.mu.Unlock() // retained before foreign/native calls
	return c, nil
}
func (o *residentServerPending) currentCallLocked(c *residentServerCurrentCall) bool {
	return c != nil && c == o.currentValidation && c.owner == o && c.op != nil && c.op.owner == o && c.op.kind == "validate_current" && !c.op.abnormal && o.active == c.op && c.ctx != nil && c.challenge != nil && c.charge != nil && c.prepared == o.preparedValidation && c.manager == o.manager && c.borrower == o.managerBound && o.currentPreparedLocked(c.view)
}
func (o *residentServerPending) currentGate(c *residentServerCurrentCall) error {
	o.mu.Lock()
	valid := o.currentCallLocked(c)
	o.mu.Unlock()
	if !valid {
		return errResidentServerOriginal
	}
	return c.ctx.Err()
}
func (o *residentServerPending) publishCurrentOriginal(c *residentServerCurrentCall, normal *bool, resultErr *error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if c == nil || c != o.currentValidation || c.owner != o || c.op == nil || c.op.owner != o || c.op.kind != "validate_current" {
		panic("original current Server identity lost")
	}
	c.normalReturned = *normal && !c.op.abnormal && c.op.normalFinished
	c.resultError = *resultErr
}
func (o *residentServerPending) validateCurrentOriginal(ctx context.Context, view *lifetimeview.ServerView, challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge) (data residentCurrentServerData, resultErr error) {
	c, err := o.beginCurrentOriginal(ctx, view, challenge, charge)
	if err != nil {
		return data, err
	}
	normal := false
	defer o.publishCurrentOriginal(c, &normal, &resultErr)
	defer o.finish(c.op, &normal, &resultErr)
	resultErr = o.validateCurrentBody(c)
	if resultErr == nil {
		data = c.data
	}
	normal = true
	return data, resultErr
}
func (o *residentServerPending) validateCurrentBody(c *residentServerCurrentCall) error {
	if err := o.currentGate(c); err != nil {
		return err
	}
	c.viewAttempted = true
	c.viewData, c.viewError = c.challenge.ValidateOriginal(c.charge)
	c.viewReturned = true
	if c.viewError != nil {
		return c.viewError
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	c.nativeAttempted = true
	c.native, c.nativeError = o.claim.ValidateOriginalContext(c.ctx)
	c.nativeReturned = true
	if c.nativeError != nil {
		return c.nativeError
	}
	if !c.native.InheritedFence || c.native != o.original || c.native.Generation != c.viewData.Context.Generation || o.server.home != c.native.Path || o.server.generation != c.native.Generation {
		return errResidentServerOriginal
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	// Existing exact original borrower -> SAME completed Manager CURRENT call
	// -> actual DB CURRENT completion. No prepared Borrower DATA or Manager getter.
	c.managerAttempted = true
	c.managerData, c.managerError = c.borrower.CurrentOriginalData(c.challenge, c.charge, o.claim)
	c.managerReturned = true
	if c.managerError != nil {
		return c.managerError
	}
	if c.managerData.Native != c.native || c.managerData.SettingsSHA256 != c.viewData.Context.SettingsSHA256 || c.managerData.DebitID != c.viewData.Context.DebitID || c.managerData.ProfileSHA256 != c.viewData.Context.ProfileSHA256 || c.managerData.WorkspaceFormat != "4" {
		return errResidentServerOriginal
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	c.viewRecheckAttempted = true
	c.viewRecheck, c.viewRecheckError = c.challenge.ValidateOriginal(c.charge)
	c.viewRecheckReturned = true
	if c.viewRecheckError != nil {
		return c.viewRecheckError
	}
	if c.viewRecheck != c.viewData {
		return errResidentServerOriginal
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	c.data = residentCurrentServerData{Native: c.native, SettingsSHA256: c.managerData.SettingsSHA256, WorkspaceFormat: c.managerData.WorkspaceFormat}
	return c.ctx.Err()
}
func (o *residentServerPending) completedCurrentLocked(c *residentServerCurrentCall, view *lifetimeview.ServerView, challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge) bool {
	return o.active == nil && o.currentTried && c != nil && c == o.currentValidation && c.owner == o && c.op != nil && c.op.owner == o && c.op.kind == "validate_current" && c.op.normalFinished && !c.op.abnormal && c.normalReturned && c.resultError == nil && c.prepared == o.preparedValidation && c.view == view && c.challenge == challenge && challenge != nil && c.charge == charge && charge != nil && c.manager == o.manager && c.borrower == o.managerBound && o.currentPreparedLocked(view) && c.viewAttempted && c.viewReturned && c.viewError == nil && c.nativeAttempted && c.nativeReturned && c.nativeError == nil && c.managerAttempted && c.managerReturned && c.managerError == nil && c.viewRecheckAttempted && c.viewRecheckReturned && c.viewRecheckError == nil && c.viewRecheck == c.viewData && c.data.Native == c.native && c.managerData.Native == c.native
}
func (o *residentServerPending) currentOriginalData(view *lifetimeview.ServerView, challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge) (residentCurrentServerData, error) {
	if o == nil || o.self != o {
		return residentCurrentServerData{}, errResidentServerOriginal
	}
	o.mu.Lock()
	c := o.currentValidation
	valid := o.completedCurrentLocked(c, view, challenge, charge)
	o.mu.Unlock()
	if !valid {
		return residentCurrentServerData{}, errResidentServerOriginal
	}
	data, err := c.borrower.CurrentOriginalData(challenge, charge, o.claim)
	if err != nil {
		return residentCurrentServerData{}, err
	}
	body, err := challenge.ValidateOriginal(charge)
	if err != nil {
		return residentCurrentServerData{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.completedCurrentLocked(c, view, challenge, charge) || data != c.managerData || body != c.viewData {
		return residentCurrentServerData{}, errResidentServerOriginal
	}
	return c.data, nil
}
