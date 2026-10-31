package manager

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"getarm.org/internal/daemonconfig"
	"getarm.org/internal/model"
	"getarm.org/internal/server/lifetimeview"
)

func TestPreparedManagerRefusesUnopenedForeignAndMissingBindings(t *testing.T) {
	view := &lifetimeview.ManagerView{}
	settings := daemonconfig.Settings{Resources: model.Resources{CPU: 1}, Listen: "127.0.0.1:0"}
	owner, err := NewResidentManagerPending(view, settings)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = owner.ValidatePreparedOriginal(ctx, view, nil, nil); err != context.Canceled {
		t.Fatal(err)
	}
	for _, candidate := range []*ResidentManagerPending{nil, {}, {self: owner}, owner} {
		if _, err = candidate.ValidatePreparedOriginal(context.Background(), view, nil, nil); err != errResidentManagerOriginal {
			t.Fatal(err)
		}
	}
	if owner.active != nil || owner.preparedTried || owner.startTried || owner.openTried || owner.preparedValidation != nil || owner.manager.db != nil {
		t.Fatal("refusal performed work")
	}
	if _, err = owner.OriginalPreparedBorrowerData(&ResidentManagerServerBinding{}, &lifetimeview.ServerClaim{}); err != errResidentManagerOriginal {
		t.Fatal(err)
	}
}
func TestPreparedSchemaProjectionsRequireExactBoundedCoreState(t *testing.T) {
	length := func(n int) sql.NullInt64 { return sql.NullInt64{Int64: int64(n), Valid: true} }
	generation := strings.Repeat("a", 32)
	valid := []residentPreparedSchemaRow{
		{firstType: "text", firstLength: length(16), first: []byte("workspace_format"), secondType: "text", secondLength: length(1), second: []byte("4")},
		{firstType: "text", kindLength: length(5), kindPrefix: []byte("table"), firstLength: length(len("resident_worker_core_state")), first: []byte("resident_worker_core_state"), secondLength: length(len("resident_worker_core_state")), second: []byte("resident_worker_core_state"), thirdLength: length(len(residentManagerCoreSchema)), third: []byte(residentManagerCoreSchema)},
		{firstType: "integer", integer: length(1), secondType: "text", firstLength: length(32), first: []byte(generation)},
	}
	for kind, row := range valid {
		if err := validatePreparedSchemaRowsData(kind, []residentPreparedSchemaRow{row}, generation); err != nil {
			t.Fatal(kind, err)
		}
		if err := validatePreparedSchemaRowsData(kind, []residentPreparedSchemaRow{row, row}, generation); err != errResidentManagerOriginal {
			t.Fatal("duplicate row accepted")
		}
	}
	for _, name := range []string{"blob-marker", "format-five", "ddl-overflow", "not-table", "generation-overflow"} {
		t.Run(name, func(t *testing.T) {
			kind := 0
			row := valid[0]
			switch name {
			case "blob-marker":
				row.firstType = "blob"
			case "format-five":
				row.second = []byte("5")
			case "ddl-overflow":
				kind = 1
				row = valid[1]
				row.thirdLength.Int64++
			case "not-table":
				kind = 1
				row = valid[1]
				row.kindPrefix = []byte("view")
			case "generation-overflow":
				kind = 2
				row = valid[2]
				row.firstLength.Int64 = 4096
			}
			if err := validatePreparedSchemaRowsData(kind, []residentPreparedSchemaRow{row}, generation); err != errResidentManagerOriginal {
				t.Fatal("noncanonical DATA accepted", err)
			}
		})
	}
}
