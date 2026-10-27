package manager

import (
	"context"
	"encoding/json"
	"testing"

	"getarm.org/internal/syncreconcile"
)

func TestInspectSyncKeyBindsOriginalChargedLeaseAndOperation(t *testing.T) {
	m, root, parent := syncManagerFixture(t)
	i, rows, control := sourceSyncIntent(t, m, root)
	lease, err := m.ReserveSyncSource(t.Context(), i, rows, root, parent, control)
	if err != nil {
		t.Fatal(err)
	}
	inspect := func() SyncKeyStatus {
		t.Helper()
		status, err := m.InspectSyncKey(t.Context(), i.Key, syncreconcile.Local)
		if err != nil {
			t.Fatal(err)
		}
		if status.Lease.ID != lease.ID || status.Lease.Request.OriginKey != i.Key || status.Lease.Request.Resources != i.LocalResources || status.OperationState != "reserved" || status.PlanDigest != i.PlanDigest || status.Generation != i.Generation || status.Volume == "" {
			t.Fatalf("original charge and operation not joined: %+v", status)
		}
		return status
	}
	if got := inspect(); got.Lease.State != "active" {
		t.Fatalf("active original source lease absent: %+v", got.Lease)
	}
	if _, err := m.End(lease.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if got := inspect(); got.Lease.State != "needs_attention" {
		t.Fatalf("retained original source lease absent: %+v", got.Lease)
	}
	if _, err := m.InspectSyncKey(t.Context(), i.Key, syncreconcile.Remote); err == nil {
		t.Fatal("unreserved destination side borrowed source evidence")
	}
	if _, err := m.InspectSyncKey(t.Context(), "different-sync-key", syncreconcile.Local); err == nil {
		t.Fatal("different key borrowed original charge")
	}
	// A replacement row at the same revision cannot be reported as evidence
	// for the original source key merely because the binding row remains.
	replacement := lease
	replacement.Request.OriginKey = "different-sync-key"
	raw, err := json.Marshal(replacement)
	if err != nil {
		t.Fatal(err)
	}
	var original string
	if err := m.db.QueryRowContext(context.Background(), `SELECT data FROM leases WHERE id=?`, lease.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.ExecContext(context.Background(), `UPDATE leases SET data=? WHERE id=?`, raw, lease.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InspectSyncKey(t.Context(), i.Key, syncreconcile.Local); err == nil {
		t.Fatal("changed lease origin key reported as original charge")
	}
	if _, err := m.db.ExecContext(context.Background(), `UPDATE leases SET data=? WHERE id=?`, original, lease.ID); err != nil {
		t.Fatal(err)
	}
}
