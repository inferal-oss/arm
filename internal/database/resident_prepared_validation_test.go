package database

import (
	"context"
	"database/sql"
	"testing"

	"getarm.org/internal/server/lifetimeview"
)

func TestPreparedDatabaseValidationRefusesBeforePoolOrCloseEffects(t *testing.T) {
	ctx := context.Background()
	for _, owner := range []*ResidentOpen{nil, {}, {self: &ResidentOpen{}}} {
		if _, err := owner.ValidatePreparedOriginal(ctx, nil, nil, nil); err != errResidentDatabaseOriginal {
			t.Fatal(err)
		}
	}
	claim := &lifetimeview.ManagerClaim{}
	owner, err := NewResidentOpen(claim) // actual PURE pending, not an issued claim
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = owner.ValidatePreparedOriginal(cancelled, nil, claim, nil); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err = owner.ValidatePreparedOriginal(ctx, &ResidentDatabaseBinding{}, claim, nil); err != errResidentDatabaseOriginal {
		t.Fatal(err)
	}
	if _, err = owner.beginOriginal("unknown-kind"); err != errResidentDatabaseOriginal {
		t.Fatal(err)
	}
	if owner.active != nil || owner.preparedTried || owner.closeTried || owner.openTried || owner.pool != nil || owner.connect != nil || owner.preparedValidation != nil {
		t.Fatal("refusal changed original custody")
	}
}
func TestPreparedSelectedFileProjectionRefusesOverflowAndForeignRows(t *testing.T) {
	main := residentPreparedDatabaseRow{sequence: 0, nameType: "text", nameBytes: sql.NullInt64{Int64: 4, Valid: true}, namePrefix: []byte("main"), fileType: "text", fileBytes: sql.NullInt64{Int64: 10, Valid: true}, filePrefix: []byte("/synthetic")}
	if err := validatePreparedSelectedRowsData([]residentPreparedDatabaseRow{main}, "/synthetic"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"overflow", "alias", "wrong-path", "duplicate", "no-main"} {
		t.Run(kind, func(t *testing.T) {
			bad := main
			rows := []residentPreparedDatabaseRow{bad}
			switch kind {
			case "overflow":
				rows[0].fileBytes.Int64 = 4097
			case "alias":
				rows[0].nameBytes.Int64 = 5
			case "wrong-path":
				rows[0].filePrefix = []byte("/different")
			case "duplicate":
				rows = append(rows, main)
			case "no-main":
				rows[0].namePrefix = []byte("temp")
				rows[0].sequence = 1
			}
			if err := validatePreparedSelectedRowsData(rows, "/synthetic"); err != errResidentDatabaseOriginal {
				t.Fatal("unbounded/foreign DATA accepted", err)
			}
		})
	}
}
