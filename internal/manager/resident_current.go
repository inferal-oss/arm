package manager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"

	"getarm.org/internal/database"
	"getarm.org/internal/model"
	"getarm.org/internal/server/challengeview"
	"getarm.org/internal/server/lifetimeview"
	"getarm.org/internal/server/serviceview"
)

type ResidentCurrentManagerData struct {
	Native                                                  lifetimeview.Snapshot
	SettingsSHA256, WorkspaceFormat, DebitID, ProfileSHA256 string
}
type residentCurrentStandingData struct {
	held                 model.Resources
	debit, profile, home string
	uid                  uint32
	device, inode        uint64
}
type residentCurrentSchemaQuery struct {
	kind                             int
	queryAttempted, queryReturned    bool
	rows                             *sql.Rows
	queryError, scanError, rowsError error
	nextCalls, scanCalls, count      int
	data                             [2]residentPreparedSchemaRow // pure row DATA shape; fresh per-current arrays
	closeAttempted, closeReturned    bool
	closeError                       error
}
type residentManagerCurrentCall struct {
	owner                                         *ResidentManagerPending
	op                                            *residentManagerOperation
	ctx                                           context.Context
	prepared                                      *residentManagerPreparedCall
	view                                          *lifetimeview.ManagerView
	challenge                                     *challengeview.CurrentChallengeView
	charge                                        *serviceview.ManagerCharge
	borrower                                      *ResidentManagerServerBinding
	pool                                          *sql.DB
	databaseOwner                                 *database.ResidentOpen
	databaseBinding                               *database.ResidentDatabaseBinding
	claim                                         *lifetimeview.ManagerClaim
	originalSchemaTx                              *sql.Tx
	viewAttempted, viewReturned                   bool
	viewData                                      challengeview.CurrentChallengeData
	viewError                                     error
	nativeAttempted, nativeReturned               bool
	native                                        lifetimeview.Snapshot
	nativeError                                   error
	standing                                      residentCurrentStandingData
	chargeAttempted, chargeReturned               bool
	chargeData                                    serviceview.ChargeObservation
	chargeError                                   error
	settings                                      []byte
	settingsSHA                                   string
	databaseAttempted, databaseReturned           bool
	database                                      database.ResidentCurrentDatabaseData
	databaseError                                 error
	databaseCheckAttempted, databaseCheckReturned bool
	databaseCheck                                 database.ResidentCurrentDatabaseData
	databaseCheckError                            error
	databaseFinalAttempted, databaseFinalReturned bool
	databaseFinal                                 database.ResidentCurrentDatabaseData
	databaseFinalError                            error
	beginAttempted, beginReturned                 bool
	tx                                            *sql.Tx
	beginError                                    error
	queries                                       [3]*residentCurrentSchemaQuery
	endAttempted, endReturned                     bool
	endError                                      error
	viewRecheckAttempted, viewRecheckReturned     bool
	viewRecheck                                   challengeview.CurrentChallengeData
	viewRecheckError                              error
	data                                          ResidentCurrentManagerData
	normalReturned                                bool
	resultError                                   error
}

func (o *ResidentManagerPending) currentPreparedLocked(view *lifetimeview.ManagerView, charge *serviceview.ManagerCharge, borrower *ResidentManagerServerBinding) bool {
	p := o.preparedValidation
	return o.firstError == nil && o.ready && !o.startTried && !o.started && o.standingBindingCompleted && view != nil && view == o.view && charge != nil && borrower != nil && borrower == o.server && borrower.owner == o && borrower.validated && borrower.claim != nil && o.manager != nil && o.manager.resident == o && o.database != nil && o.binding != nil && o.claim != nil && o.schemaTx != nil && o.schemaCommitTried && o.schemaCommitError == nil && o.preparedTried && p != nil && p.owner == o && p.op != nil && p.op.owner == o && p.op.kind == "validate_prepared" && p.op.normalFinished && !p.op.abnormal && p.returned && p.resultError == nil && p.view == view && p.charge == charge && p.borrower == borrower && p.pool == o.manager.db && p.originalSchemaTx == o.schemaTx
}
func (o *ResidentManagerPending) beginCurrentOriginal(ctx context.Context, view *lifetimeview.ManagerView, challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge, borrower *ResidentManagerServerBinding) (*residentManagerCurrentCall, error) {
	if o == nil || o.self != o || ctx == nil || challenge == nil {
		return nil, errResidentManagerOriginal
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	valid := o.active == nil && o.currentPreparedLocked(view, charge, borrower)
	o.mu.Unlock()
	if !valid {
		return nil, errResidentManagerOriginal
	}
	op, err := o.beginOriginal("validate_current")
	if err != nil {
		return nil, err
	}
	c := &residentManagerCurrentCall{owner: o, op: op, ctx: ctx, prepared: o.preparedValidation, view: view, challenge: challenge, charge: charge, borrower: borrower, pool: o.manager.db, databaseOwner: o.database, databaseBinding: o.binding, claim: o.claim, originalSchemaTx: o.schemaTx}
	o.mu.Lock()
	o.currentValidation = c
	o.mu.Unlock() // before foreign/native/SQL calls
	return c, nil
}
func (o *ResidentManagerPending) currentCallLocked(c *residentManagerCurrentCall) bool {
	return c != nil && c == o.currentValidation && c.owner == o && c.op != nil && c.op.owner == o && c.op.kind == "validate_current" && !c.op.abnormal && o.active == c.op && c.ctx != nil && c.challenge != nil && c.prepared == o.preparedValidation && c.pool == o.manager.db && c.databaseOwner == o.database && c.databaseBinding == o.binding && c.claim == o.claim && c.originalSchemaTx == o.schemaTx && o.currentPreparedLocked(c.view, c.charge, c.borrower)
}
func (o *ResidentManagerPending) currentGate(c *residentManagerCurrentCall) error {
	o.mu.Lock()
	valid := o.currentCallLocked(c)
	o.mu.Unlock()
	if !valid {
		return errResidentManagerOriginal
	}
	return c.ctx.Err()
}
func (o *ResidentManagerPending) publishCurrentOriginal(c *residentManagerCurrentCall, normal *bool, resultErr *error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if c == nil || c != o.currentValidation || c.owner != o || c.op == nil || c.op.owner != o || c.op.kind != "validate_current" {
		panic("original current Manager identity lost")
	}
	c.normalReturned = *normal && !c.op.abnormal && c.op.normalFinished
	c.resultError = *resultErr
}
func (o *ResidentManagerPending) ValidateCurrentOriginal(ctx context.Context, view *lifetimeview.ManagerView, challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge, borrower *ResidentManagerServerBinding) (data ResidentCurrentManagerData, resultErr error) {
	c, err := o.beginCurrentOriginal(ctx, view, challenge, charge, borrower)
	if err != nil {
		return data, err
	}
	normal := false
	defer o.publishCurrentOriginal(c, &normal, &resultErr)
	defer o.finishOriginal(c.op, &normal, &resultErr)
	resultErr = o.validateCurrentBody(c)
	if resultErr == nil {
		data = c.data
	}
	normal = true
	return data, resultErr
}
func (o *ResidentManagerPending) validateCurrentBody(c *residentManagerCurrentCall) error {
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
	c.native, c.nativeError = c.claim.ValidateOriginalContext(c.ctx)
	c.nativeReturned = true
	if c.nativeError != nil {
		return c.nativeError
	}
	if !c.native.InheritedFence || c.native != o.original || c.native.Generation != c.viewData.Context.Generation {
		return errResidentManagerOriginal
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	// Snapshot all standing fields under actual Manager.mu, then release it
	// BEFORE consulting the service charge or any foreign/publication mutex.
	o.manager.mu.Lock()
	standing := o.manager.residentService
	valid := standing != nil && standing.manager == o.manager && standing.claim == c.charge && standing.bound && standing.validated && validStandingVector(standing.held)
	if valid {
		c.standing = residentCurrentStandingData{held: standing.held, debit: standing.debit, profile: standing.profile, home: standing.home, uid: standing.homeUID, device: standing.homeDevice, inode: standing.homeInode}
	}
	o.manager.mu.Unlock()
	if !valid || c.standing.home != c.native.Path || c.standing.uid != c.native.UID || c.standing.device != c.native.Device || c.standing.inode != c.native.Inode {
		return errResidentManagerOriginal
	}
	c.chargeAttempted = true
	c.chargeData, c.chargeError = c.charge.ObserveOriginal()
	c.chargeReturned = true
	if c.chargeError != nil {
		return c.chargeError
	}
	observed := c.chargeData
	if !observed.AdmissionAvailable || observed.Home != c.native.Path || observed.HomeID.UID != c.native.UID || observed.HomeID.Device != c.native.Device || observed.HomeID.Inode != c.native.Inode || observed.DebitID != c.standing.debit || observed.ProfileSHA256 != c.standing.profile || observed.Resources.CPU != c.standing.held.CPU || observed.Resources.Memory != c.standing.held.Memory || observed.Resources.Disk != c.standing.held.Disk || observed.DebitID != c.viewData.Context.DebitID || observed.ProfileSHA256 != c.viewData.Context.ProfileSHA256 {
		return errResidentManagerOriginal
	}
	if err := o.currentGate(c); err != nil {
		return err
	}
	if err := o.settings.Validate(); err != nil {
		return err
	}
	var err error
	c.settings, err = json.Marshal(o.settings)
	if err != nil || len(c.settings) > 16<<10 {
		return errResidentManagerOriginal
	}
	h := sha256.Sum256(c.settings)
	c.settingsSHA = hex.EncodeToString(h[:])
	if c.settingsSHA != c.viewData.Context.SettingsSHA256 || c.settingsSHA != c.prepared.data.SettingsSHA256 {
		return errResidentManagerOriginal
	}
	c.databaseAttempted = true
	c.database, c.databaseError = c.databaseOwner.ValidateCurrentOriginal(c.ctx, c.databaseBinding, c.claim, c.pool, c.challenge, c.charge)
	c.databaseReturned = true
	if c.databaseError != nil {
		return c.databaseError
	}
	if err = o.currentGate(c); err != nil {
		return err
	}
	c.databaseCheckAttempted = true
	c.databaseCheck, c.databaseCheckError = c.databaseOwner.CurrentOriginalData(c.databaseBinding, c.claim, c.pool, c.challenge, c.charge)
	c.databaseCheckReturned = true
	if c.databaseCheckError != nil {
		return c.databaseCheckError
	}
	if c.databaseCheck != c.database || c.database.Native != c.native {
		return errResidentManagerOriginal
	}
	if err = o.currentGate(c); err != nil {
		return err
	}
	c.beginAttempted = true
	c.tx, c.beginError = c.pool.BeginTx(c.ctx, &sql.TxOptions{ReadOnly: true})
	c.beginReturned = true
	if c.beginError != nil {
		return c.beginError
	}
	if c.tx == nil {
		return errResidentManagerOriginal
	}
	for kind := 0; kind < len(c.queries); kind++ {
		if err = o.currentGate(c); err != nil {
			return err
		}
		query := &residentCurrentSchemaQuery{kind: kind}
		c.queries[kind] = query
		if err = o.readCurrentSchema(c, query); err != nil {
			return err
		} // no cleanup on partial failure
	}
	if err = o.currentGate(c); err != nil {
		return err
	}
	c.endAttempted = true
	c.endError = c.tx.Commit()
	c.endReturned = true
	if c.endError != nil {
		return c.endError
	} // public API outcome, never native Closed
	if err = o.currentGate(c); err != nil {
		return err
	}
	c.databaseFinalAttempted = true
	c.databaseFinal, c.databaseFinalError = c.databaseOwner.CurrentOriginalData(c.databaseBinding, c.claim, c.pool, c.challenge, c.charge)
	c.databaseFinalReturned = true
	if c.databaseFinalError != nil {
		return c.databaseFinalError
	}
	if c.databaseFinal != c.database {
		return errResidentManagerOriginal
	}
	if err = o.currentGate(c); err != nil {
		return err
	}
	c.viewRecheckAttempted = true
	c.viewRecheck, c.viewRecheckError = c.challenge.ValidateOriginal(c.charge)
	c.viewRecheckReturned = true
	if c.viewRecheckError != nil {
		return c.viewRecheckError
	}
	if c.viewRecheck != c.viewData {
		return errResidentManagerOriginal
	}
	if err = o.currentGate(c); err != nil {
		return err
	}
	c.data = ResidentCurrentManagerData{Native: c.native, SettingsSHA256: c.settingsSHA, WorkspaceFormat: residentManagerWorkspaceFormat, DebitID: observed.DebitID, ProfileSHA256: observed.ProfileSHA256}
	return c.ctx.Err()
}
func (o *ResidentManagerPending) readCurrentSchema(c *residentManagerCurrentCall, q *residentCurrentSchemaQuery) error {
	o.phase("current_original_bounded_schema_query_pending")
	q.queryAttempted = true
	q.rows, q.queryError = c.tx.QueryContext(c.ctx, preparedSchemaQueries[q.kind])
	q.queryReturned = true
	if q.queryError != nil {
		return q.queryError
	}
	if q.rows == nil {
		return errResidentManagerOriginal
	}
	for q.nextCalls < 3 {
		q.nextCalls++
		if !q.rows.Next() {
			break
		}
		if q.count == len(q.data) {
			return errResidentManagerOriginal
		}
		row := &q.data[q.count]
		q.scanCalls++
		switch q.kind {
		case 0:
			q.scanError = q.rows.Scan(&row.firstType, &row.firstLength, &row.first, &row.secondType, &row.secondLength, &row.second)
		case 1:
			q.scanError = q.rows.Scan(&row.firstType, &row.kindLength, &row.kindPrefix, &row.firstLength, &row.first, &row.secondLength, &row.second, &row.thirdLength, &row.third)
		case 2:
			q.scanError = q.rows.Scan(&row.firstType, &row.integer, &row.secondType, &row.firstLength, &row.first)
		}
		if q.scanError != nil {
			return q.scanError
		}
		q.count++
	}
	q.rowsError = q.rows.Err()
	if q.rowsError != nil {
		return q.rowsError
	}
	q.closeAttempted = true
	q.closeError = q.rows.Close()
	q.closeReturned = true
	if q.closeError != nil {
		return q.closeError
	}
	return validatePreparedSchemaRowsData(q.kind, q.data[:q.count], c.native.Generation) // pure DATA only
}
func (o *ResidentManagerPending) completedCurrentLocked(c *residentManagerCurrentCall, binding *ResidentManagerServerBinding, claim *lifetimeview.ServerClaim, challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge) bool {
	if o.active != nil || !o.currentTried || c == nil || c != o.currentValidation || c.owner != o || c.op == nil || c.op.owner != o || c.op.kind != "validate_current" || !c.op.normalFinished || c.op.abnormal || !c.normalReturned || c.resultError != nil || c.prepared != o.preparedValidation || c.borrower != binding || binding == nil || binding.claim != claim || claim == nil || c.challenge != challenge || challenge == nil || c.charge != charge || !o.currentPreparedLocked(c.view, charge, binding) || c.databaseOwner != o.database || c.databaseBinding != o.binding || c.claim != o.claim || c.pool != o.manager.db || c.originalSchemaTx != o.schemaTx {
		return false
	}
	if !c.viewAttempted || !c.viewReturned || c.viewError != nil || !c.nativeAttempted || !c.nativeReturned || c.nativeError != nil || !c.chargeAttempted || !c.chargeReturned || c.chargeError != nil || !c.databaseAttempted || !c.databaseReturned || c.databaseError != nil || !c.databaseCheckAttempted || !c.databaseCheckReturned || c.databaseCheckError != nil || c.databaseCheck != c.database || !c.beginAttempted || !c.beginReturned || c.beginError != nil || c.tx == nil || !c.endAttempted || !c.endReturned || c.endError != nil || !c.databaseFinalAttempted || !c.databaseFinalReturned || c.databaseFinalError != nil || c.databaseFinal != c.database || !c.viewRecheckAttempted || !c.viewRecheckReturned || c.viewRecheckError != nil || c.viewRecheck != c.viewData {
		return false
	}
	for kind, q := range c.queries {
		if q == nil || q.kind != kind || !q.queryAttempted || !q.queryReturned || q.queryError != nil || q.rows == nil || q.scanError != nil || q.rowsError != nil || q.count != 1 || q.nextCalls > 3 || q.scanCalls != q.count || !q.closeAttempted || !q.closeReturned || q.closeError != nil {
			return false
		}
	}
	return c.data.Native == c.native && c.database.Native == c.native && c.data.SettingsSHA256 == c.settingsSHA
}
func (o *ResidentManagerPending) currentBorrowerData(binding *ResidentManagerServerBinding, claim *lifetimeview.ServerClaim, challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge) (ResidentCurrentManagerData, error) {
	if o == nil || o.self != o {
		return ResidentCurrentManagerData{}, errResidentManagerOriginal
	}
	o.mu.Lock()
	c := o.currentValidation
	valid := o.completedCurrentLocked(c, binding, claim, challenge, charge)
	o.mu.Unlock()
	if !valid {
		return ResidentCurrentManagerData{}, errResidentManagerOriginal
	}
	// No Manager publication lock is held across DB/service owner methods.
	db, err := c.databaseOwner.CurrentOriginalData(c.databaseBinding, c.claim, c.pool, challenge, charge)
	if err != nil {
		return ResidentCurrentManagerData{}, err
	}
	body, err := challenge.ValidateOriginal(charge)
	if err != nil {
		return ResidentCurrentManagerData{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.completedCurrentLocked(c, binding, claim, challenge, charge) || db != c.database || body != c.viewData {
		return ResidentCurrentManagerData{}, errResidentManagerOriginal
	}
	return c.data, nil
}

// CURRENT selector on the EXISTING exact original Server binding, not a Manager
// getter or prepared-result adapter. No pool/native/owner pointer is returned.
func (b *ResidentManagerServerBinding) CurrentOriginalData(challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge, claim *lifetimeview.ServerClaim) (ResidentCurrentManagerData, error) {
	if b == nil || b.owner == nil {
		return ResidentCurrentManagerData{}, errResidentManagerOriginal
	}
	return b.owner.currentBorrowerData(b, claim, challenge, charge)
}
func (o *ResidentManagerPending) OriginalCurrentData(view *lifetimeview.ManagerView, challenge *challengeview.CurrentChallengeView, charge *serviceview.ManagerCharge, binding *ResidentManagerServerBinding) (ResidentCurrentManagerData, error) {
	if o == nil || o.self != o {
		return ResidentCurrentManagerData{}, errResidentManagerOriginal
	}
	o.mu.Lock()
	valid := view != nil && view == o.view && binding != nil && binding == o.server && binding.owner == o
	var claim *lifetimeview.ServerClaim
	if valid {
		claim = binding.claim
	}
	o.mu.Unlock()
	if !valid {
		return ResidentCurrentManagerData{}, errResidentManagerOriginal
	}
	return o.currentBorrowerData(binding, claim, challenge, charge)
}
