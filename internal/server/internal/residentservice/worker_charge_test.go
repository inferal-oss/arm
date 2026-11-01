package residentservice

import (
	"context"
	"testing"

	"getarm.org/internal/residentrefs"
)

// Install only synthetic records and flags around unissued zero recipients.
// Native scope validation must refuse BEFORE any descriptor or environment IO.
func workerChargeForgeryData(t *testing.T) (*WorkerChildPending, *WorkerManagerCharge) {
	t.Helper()
	p := unissuedWorkerChildData()
	w := p.custody
	r := workerRecordsData(t)
	w.grant = r[0].Grant
	w.path = w.grant.Intent.Home
	w.raw.path = w.path
	w.journal = workerJournalData{records: r[:2]}
	d := &WorkerOriginalDebit{owner: w, record: r[1].Debit}
	d.self = d
	w.debit = d
	id := func(n NativeID) residentrefs.Identity {
		return residentrefs.Identity{Device: n.Device, Inode: n.Inode, UID: n.UID}
	}
	p.identities = residentrefs.ServiceIdentities{Home: id(w.grant.Intent.HomeID), Lock: id(d.record.LockID), Journal: id(d.record.JournalID)}
	p.received, p.runtimeBound = true, true
	p.rootContext = context.Background()
	charge := &WorkerManagerCharge{child: p, owner: w}
	charge.self = charge
	p.charge = charge
	return p, charge
}

func TestWorkerChargeRejectsZeroCopiedAndForeignData(t *testing.T) {
	var zero WorkerManagerCharge
	if _, err := zero.ObserveOriginal(); err == nil {
		t.Fatal("zero charge accepted")
	}
	_, charge := workerChargeForgeryData(t)
	copy := *charge
	if _, err := copy.ObserveOriginal(); err == nil {
		t.Fatal("copied charge accepted")
	}
	other := unissuedWorkerChildData()
	charge.owner = other.custody
	if _, err := charge.ObserveOriginal(); err == nil {
		t.Fatal("foreign owner accepted")
	}
}

func TestWorkerChargeRecordsCannotReplaceNativeScopeData(t *testing.T) {
	p, charge := workerChargeForgeryData(t)
	if _, err := charge.ObserveOriginal(); err == nil {
		t.Fatal("record/flags issued native observation")
	}
	assertWorkerNoNativeData(t, p.custody)
	if len(p.environment) != 0 {
		t.Fatal("unissued native scope reached environment snapshot")
	}
}

func TestWorkerChargeWeakFloorCannotSupplyOriginalAuthorityData(t *testing.T) {
	p, charge := workerChargeForgeryData(t)
	floor, err := p.custody.grant.Intent.Profile.Contract.LogicalFloorData()
	if err != nil {
		t.Fatal(err)
	}
	if err = charge.CheckOriginalRuntimeBounds(floor); err == nil {
		t.Fatal("logical floor manufactured funding")
	}
	assertWorkerNoNativeData(t, p.custody)
}
