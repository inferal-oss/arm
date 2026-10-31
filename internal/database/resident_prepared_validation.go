package database

import (
	"context"
	"database/sql"

	"getarm.org/internal/server/lifetimeview"
)

// DATA returned only by the actual original validator. No constructor, pool,
// native handle, success callback or readiness/closure authority is returned.
type ResidentPreparedDatabaseData struct {
	Native lifetimeview.Snapshot
}

type residentPreparedDatabaseCall struct {
	owner                         *ResidentOpen
	op                            *residentSQLOperation
	binding                       *ResidentDatabaseBinding
	claim                         *lifetimeview.ManagerClaim
	pool                          *sql.DB
	native                        lifetimeview.Snapshot
	queryAttempted, queryReturned bool
	rows                          *sql.Rows
	queryError                    error
	nextCalls, scanCalls          int
	rowsRead                      [3]residentPreparedDatabaseRow
	rowCount                      int
	rowsError                     error
	closeAttempted, closeReturned bool
	closeError                    error
	returned                      bool
	resultError                   error
}
type residentPreparedDatabaseRow struct {
	sequence               int64
	nameType, fileType     string
	nameBytes, fileBytes   sql.NullInt64
	namePrefix, filePrefix []byte
}

// Distinct once-only validation on THIS binding/claim/pool and its one actual
// completed official Connect. No fresh pool/Connect or pre-SQL empty-ledger test.
// The original owner/call retains every partial return before interpretation.
func (o *ResidentOpen) ValidatePreparedOriginal(ctx context.Context, binding *ResidentDatabaseBinding, claim *lifetimeview.ManagerClaim, expectedPool *sql.DB) (data ResidentPreparedDatabaseData, resultErr error) {
	if o == nil || o.self != o || ctx == nil {
		return data, errResidentDatabaseOriginal
	}
	if err := ctx.Err(); err != nil {
		return data, err
	}
	o.mu.Lock()
	valid := binding != nil && o.binding == binding && binding.owner == o && binding.pool == o.pool && o.pool != nil && expectedPool == o.pool && claim != nil && o.claim == claim && o.connector != nil && o.connector.owner == o && o.connector.original == o.standardConnector && o.connector.originalClaim == claim && o.connect != nil && o.connect.owner == o && o.connect.connector == o.connector && o.connect.finished && !o.connect.abnormal && o.connect.resultErr == nil && o.connect.returned != nil && o.connectRefusals == 0
	o.mu.Unlock()
	if !valid {
		return data, errResidentDatabaseOriginal
	}
	op, err := o.beginOriginal("validate_prepared")
	if err != nil {
		return data, err
	}
	normal := false
	defer o.finishOriginal(op, &normal, &resultErr)
	call := &residentPreparedDatabaseCall{owner: o, op: op, binding: binding, claim: claim, pool: o.pool}
	o.mu.Lock()
	o.preparedValidation = call
	o.mu.Unlock()
	o.setOriginalPhase("prepared_original_native_claim_pending")
	call.native, resultErr = claim.ValidateOriginal()
	if resultErr == nil && (!call.native.InheritedFence || call.native.Generation != binding.generation) {
		resultErr = errResidentDatabaseOriginal
	}
	if resultErr == nil {
		resultErr = o.matchOriginalPreparedFiles(ctx, call.native)
	}
	if resultErr == nil {
		resultErr = o.validatePreparedSelectedFile(ctx, call)
	}
	if resultErr == nil {
		o.mu.Lock()
		valid = o.pool == call.pool && o.binding == binding && o.claim == claim && !o.closeTried && !o.connectSealed && o.connectRefusals == 0 && o.connect.finished && !o.connect.abnormal && o.connect.resultErr == nil
		o.mu.Unlock()
		if !valid {
			resultErr = errResidentDatabaseOriginal
		}
	}
	if resultErr == nil {
		resultErr = ctx.Err()
	}
	if resultErr == nil {
		data.Native = call.native
	}
	o.mu.Lock()
	call.returned, call.resultError = true, resultErr
	o.mu.Unlock()
	normal = true
	return data, resultErr
}

// One query, <=3 rows/4 Next/3 Scan, name<=5 and path<=4097 projected
// bytes BEFORE Scan allocation. Overflow refuses, rather than truncating proof.
// Only normal successful row iteration gets an explicit public Rows.Close;
// abnormal/error/unreturned work remains retained and is never cleanup evidence.
func (o *ResidentOpen) validatePreparedSelectedFile(ctx context.Context, call *residentPreparedDatabaseCall) error {
	o.setOriginalPhase("prepared_original_selected_file_query_pending")
	call.queryAttempted = true
	call.rows, call.queryError = call.pool.QueryContext(ctx, `SELECT seq,typeof(name),length(CAST(name AS BLOB)),substr(CAST(name AS BLOB),1,5),typeof(file),length(CAST(file AS BLOB)),substr(CAST(file AS BLOB),1,4097) FROM pragma_database_list LIMIT 3`)
	call.queryReturned = true
	if call.queryError != nil {
		return call.queryError
	}
	if call.rows == nil {
		return errResidentDatabaseOriginal
	}
	for call.nextCalls < 4 {
		call.nextCalls++
		if !call.rows.Next() {
			break
		}
		if call.rowCount == len(call.rowsRead) {
			return errResidentDatabaseOriginal
		}
		row := &call.rowsRead[call.rowCount]
		call.scanCalls++
		if err := call.rows.Scan(&row.sequence, &row.nameType, &row.nameBytes, &row.namePrefix, &row.fileType, &row.fileBytes, &row.filePrefix); err != nil {
			return err
		}
		call.rowCount++
	}
	call.rowsError = call.rows.Err()
	if call.rowsError != nil {
		return call.rowsError
	}
	call.closeAttempted = true
	call.closeError = call.rows.Close()
	call.closeReturned = true // public API observation ONLY, not native quiescence
	if call.closeError != nil {
		return call.closeError
	}
	return validatePreparedSelectedRowsData(call.rowsRead[:call.rowCount], o.filename)
}
func validatePreparedSelectedRowsData(rows []residentPreparedDatabaseRow, filename string) error {
	if len(rows) < 1 || len(rows) > 2 || len(filename) == 0 || len(filename) > 4096 {
		return errResidentDatabaseOriginal
	}
	main := false
	for _, row := range rows {
		if row.nameType != "text" || !row.nameBytes.Valid || row.nameBytes.Int64 != 4 || len(row.namePrefix) != 4 || row.fileType != "text" || !row.fileBytes.Valid || row.fileBytes.Int64 < 0 || row.fileBytes.Int64 > 4096 || int64(len(row.filePrefix)) != row.fileBytes.Int64 {
			return errResidentDatabaseOriginal
		}
		switch string(row.namePrefix) {
		case "main":
			if main || row.sequence != 0 || string(row.filePrefix) != filename {
				return errResidentDatabaseOriginal
			}
			main = true
		case "temp":
			if row.sequence != 1 {
				return errResidentDatabaseOriginal
			}
		default:
			return errResidentDatabaseOriginal
		}
	}
	if !main {
		return errResidentDatabaseOriginal
	}
	return nil
}
