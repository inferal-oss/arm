package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRecoveryCapacityReservesRowsChangesAndCompleteBytes(t *testing.T) {
	d := newStoreFixture(t)
	root, _ := activateDirectory(t, d)
	_, _, _ = joinedCandidate(t, d, root)
	v, e := d.s.Read(d.ctx)
	if e != nil {
		t.Fatal(e)
	}
	if !v.RecoveryEnabled {
		t.Fatal("initial producer omitted recovery reserve marker")
	}
	if e = validateRecoveryCapacity(v); e != nil {
		t.Fatal(e)
	}
	// The original pending admission reserves four child operation rows plus
	// finalization/cancellation audit and a separate local retirement receipt.
	base := v
	base.Operations = append([]Operation(nil), v.Operations...)
	for i := len(base.Operations); i < MaxOperations-MaxRecoveryAttempts-1; i++ {
		base.Operations = append(base.Operations, Operation{Workspace: root.ID, Kind: "deny", Phase: "completed", Request: json.RawMessage(`{}`)})
	}
	if e = validateRecoveryCapacity(base); e != nil {
		t.Fatal("unused reserved slots rejected", e)
	}
	base.Operations = append(base.Operations, Operation{Workspace: root.ID, Kind: "deny", Phase: "completed", Request: json.RawMessage(`{}`)})
	if e = validateRecoveryCapacity(base); e == nil {
		t.Fatal("unrelated operation spent a reserved recovery row")
	}
	// A ledger can fit its actual rows but not the already promised future
	// evidence; byte reservations must refuse that state too.
	base = v
	base.Operations = append([]Operation(nil), v.Operations...)
	huge := Operation{Workspace: root.ID, Kind: "deny", Phase: "completed", Request: json.RawMessage(`"` + strings.Repeat("x", MaxRecordBytes-4096) + `"`)}
	for completeBytes(base) < int64(MaxLedgerBytes)-2*MaxRecordBytes-(128<<10) {
		base.Operations = append(base.Operations, huge)
	}
	if completeBytes(base) >= MaxLedgerBytes {
		t.Fatal("fixture exceeded actual byte ceiling before reserves")
	}
	if e = validateRecoveryCapacity(base); e == nil {
		t.Fatal("unrelated evidence spent future recovery bytes")
	}
}
