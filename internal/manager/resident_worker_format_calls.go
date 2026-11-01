package manager

import (
	"context"
	"database/sql"
	"errors"
	"getarm.org/internal/server/lifetimeview"
)

// Fixed startup/prepared observations. Rows and exact Scan destinations are
// retained before each public SQL effect, including error/panic/Goexit paths.
// Public API close return is not a native/driver/task Closed witness.
type residentWorkerFreshFormatCall struct {
	owner                           *ResidentWorkerManagerPending
	op                              *residentWorkerManagerOperation
	ctx                             context.Context
	tx                              *sql.Tx
	queries                         [8]*residentWorkerFreshQuery
	nativeAttempted, nativeReturned bool
	native                          lifetimeview.WorkerSnapshot
	nativeError                     error
	normalReturned                  bool
	resultError                     error
}
type residentWorkerFreshScan struct {
	destinations        []any
	attempted, returned bool
	err                 error
}
type residentWorkerFreshQuery struct {
	call                                  *residentWorkerFreshFormatCall
	index                                 int
	queryAttempted, queryReturned         bool
	rows                                  *sql.Rows
	queryError                            error
	nextCalls, nextReturns, scanCalls     int
	scans                                 [13]*residentWorkerFreshScan
	firstError                            error
	rowsErrorAttempted, rowsErrorReturned bool
	rowsError                             error
	closeAttempted, closeReturned         bool
	closeError                            error
}

func (c *residentWorkerFreshFormatCall) query(index int, statement string) (*residentWorkerFreshQuery, error) {
	if c == nil || c.ctx == nil || c.tx == nil || index < 0 || index >= len(c.queries) || c.queries[index] != nil {
		return nil, errResidentWorkerManagerOriginal
	}
	q := &residentWorkerFreshQuery{call: c, index: index}
	c.queries[index] = q
	q.queryAttempted = true
	q.rows, q.queryError = c.tx.QueryContext(c.ctx, statement)
	q.queryReturned = true
	return q, q.queryError
}
func (q *residentWorkerFreshQuery) next() bool {
	if q == nil || q.call == nil || q.index < 0 || q.index >= len(q.call.queries) || q.call.queries[q.index] != q || q.call.ctx == nil || q.rows == nil {
		return false
	}
	if q.nextCalls >= 14 {
		q.firstError = errResidentWorkerManagerOriginal
		return false
	}
	if err := q.call.ctx.Err(); err != nil {
		q.firstError = err
		return false
	}
	q.nextCalls++
	value := q.rows.Next()
	q.nextReturns++
	return value
}
func (q *residentWorkerFreshQuery) scan(destinations ...any) error {
	if q == nil || q.call == nil || q.index < 0 || q.index >= len(q.call.queries) || q.call.queries[q.index] != q || q.rows == nil || q.scanCalls >= len(q.scans) || len(destinations) == 0 || len(destinations) > 12 {
		return errResidentWorkerManagerOriginal
	}
	call := &residentWorkerFreshScan{destinations: destinations}
	q.scans[q.scanCalls] = call
	q.scanCalls++
	call.attempted = true
	call.err = q.rows.Scan(call.destinations...)
	call.returned = true
	return call.err
}
func (q *residentWorkerFreshQuery) finish(prior error) error {
	if q == nil || q.call == nil || q.index < 0 || q.index >= len(q.call.queries) || q.call.queries[q.index] != q || q.rows == nil {
		return errors.Join(prior, errResidentWorkerManagerOriginal)
	}
	q.rowsErrorAttempted = true
	q.rowsError = q.rows.Err()
	q.rowsErrorReturned = true
	q.closeAttempted = true
	q.closeError = q.rows.Close()
	q.closeReturned = true
	return errors.Join(prior, q.firstError, q.rowsError, q.closeError)
}
