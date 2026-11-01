package database

import (
	"context"
	"getarm.org/internal/server/lifetimeview"
	"reflect"
	"testing"
)

func TestWorkerDatabaseOriginalClaimBeforePhysicalEffects(t *testing.T) {
	if _, err := NewWorkerResidentOpen(nil); err == nil {
		t.Fatal("nil worker claim accepted")
	}
	owner, err := NewWorkerResidentOpen(&lifetimeview.WorkerManagerClaim{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.OpenOriginal(nil); err == nil || owner.openTried {
		t.Fatal("nil context crossed operation boundary")
	}
	binding, first := owner.OpenOriginal(context.Background())
	if first == nil || binding != nil {
		t.Fatal("unissued worker claim opened DB")
	}
	seen := owner.InspectOriginal()
	if seen.ConnectorRetained || seen.PoolRetained || seen.FilesOpened != 0 || seen.FirstError != first {
		t.Fatal("native work or unknown custody lost", seen)
	}
	if _, err = owner.OpenOriginal(context.Background()); err != first {
		t.Fatal("failed original retried/replaced error")
	}
}
func TestWorkerDatabaseUnissuedOwnersAndBindingsRefuse(t *testing.T) {
	owner, err := NewWorkerResidentOpen(&lifetimeview.WorkerManagerClaim{})
	if err != nil {
		t.Fatal(err)
	}
	forged := &WorkerResidentOpen{self: owner}
	for _, candidate := range []*WorkerResidentOpen{nil, {}, forged} {
		if _, err := candidate.OpenOriginal(context.Background()); err == nil {
			t.Fatal("unissued owner accepted")
		}
	}
	supplied := &WorkerResidentDatabaseBinding{owner: owner, generation: "synthetic"}
	if _, err = owner.DatabaseForOriginalBinding(supplied); err == nil {
		t.Fatal("DATA supplied pool authority")
	}
	if seen := owner.InspectOriginal(); seen.FilesOpened != 0 || seen.PoolRetained || seen.ConnectorRetained || seen.OriginalOperationPending {
		t.Fatal(seen)
	}
}
func TestWorkerDatabaseSurfaceHasDistinctClaimAndNoCloser(t *testing.T) {
	constructor := reflect.TypeOf(NewWorkerResidentOpen)
	if constructor.In(0) != reflect.TypeOf((*lifetimeview.WorkerManagerClaim)(nil)) || constructor.In(0) == reflect.TypeOf((*lifetimeview.ManagerClaim)(nil)) {
		t.Fatal("claim types merged")
	}
	typ := reflect.TypeOf((*WorkerResidentOpen)(nil))
	for _, name := range []string{"Close", "CloseOriginalSQLAPI", "FD", "File", "Start", "NativeClosed"} {
		if _, ok := typ.MethodByName(name); ok {
			t.Fatal("unreviewed lifetime authority exposed", name)
		}
	}
}
