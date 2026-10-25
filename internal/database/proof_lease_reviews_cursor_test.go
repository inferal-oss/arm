package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
)

var reviewCursorCloseFault = errors.New("injected lease-review schema cursor close failure")
var reviewCursorQueryFault = errors.New("injected lease-review schema query failure")

type reviewCursorFaultDriver struct{}
type reviewCursorFaultConn struct{ mode string }
type reviewCursorFaultTx struct{}
type reviewCursorFaultRows struct {
	mode    string
	emitted bool
}

func init() { sql.Register("arm-review-schema-cursor-fault", reviewCursorFaultDriver{}) }
func (reviewCursorFaultDriver) Open(mode string) (driver.Conn, error) {
	return &reviewCursorFaultConn{mode: mode}, nil
}
func (c *reviewCursorFaultConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *reviewCursorFaultConn) Close() error              { return nil }
func (c *reviewCursorFaultConn) Begin() (driver.Tx, error) { return reviewCursorFaultTx{}, nil }
func (reviewCursorFaultTx) Commit() error                  { return nil }
func (reviewCursorFaultTx) Rollback() error                { return nil }
func (c *reviewCursorFaultConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	if c.mode == "query-error" {
		return nil, reviewCursorQueryFault
	}
	return &reviewCursorFaultRows{mode: c.mode}, nil
}
func (r *reviewCursorFaultRows) Columns() []string {
	return []string{"type", "name", "tbl_name", "sql"}
}
func (r *reviewCursorFaultRows) Close() error {
	if r.mode == "close-error" {
		return reviewCursorCloseFault
	}
	return nil
}
func (r *reviewCursorFaultRows) Next(values []driver.Value) error {
	if r.emitted {
		return io.EOF
	}
	r.emitted = true
	// The classifier must reject this owned name with a different physical
	// definition before EOF, then still check the actual driver's Close result.
	copy(values, []driver.Value{"table", "lease_review_policies", "lease_review_policies", "CREATE TABLE lease_review_policies(unreviewed TEXT)"})
	return nil
}

func TestSettledProofLeaseReviewCursorErrors(t *testing.T) {
	for _, mode := range []string{"clean-close", "close-error", "query-error"} {
		t.Run(mode, func(t *testing.T) {
			db, err := sql.Open("arm-review-schema-cursor-fault", mode)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			present, err := CheckLeaseReviewSchema(t.Context(), tx)
			if present {
				t.Fatal("malformed optional schema was accepted")
			}
			switch mode {
			case "clean-close":
				if !errors.Is(err, ErrLeaseReviewSchema) {
					t.Fatal("recognized evidence not classified", err)
				}
			case "close-error":
				if !errors.Is(err, reviewCursorCloseFault) || errors.Is(err, ErrLeaseReviewSchema) {
					t.Fatal("cursor failure was downgraded as optional evidence", err)
				}
			case "query-error":
				if !errors.Is(err, reviewCursorQueryFault) || errors.Is(err, ErrLeaseReviewSchema) {
					t.Fatal("query failure was downgraded as optional evidence", err)
				}
			}
		})
	}
}
