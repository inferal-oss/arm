package residentcost

import (
	"math"
	"testing"
)

func TestOwnHeaderReservationDataRejectsMissingDuplicateOversizeAndOverflow(t *testing.T) {
	valid := [][]OwnHeaderData{{{Name: "Empty", Bytes: 0}, {Name: "Original", Bytes: 64}}}
	got, err := OwnHeaderReservationData(valid, 512, 32768)
	if err != nil || got != 32768 {
		t.Fatal(got, err)
	}
	for _, tc := range []struct {
		groups          [][]OwnHeaderData
		copies, ceiling uint64
	}{
		{nil, 512, 32768}, {[][]OwnHeaderData{{}}, 512, 32768},
		{[][]OwnHeaderData{{{Name: "Z", Bytes: 1}, {Name: "A", Bytes: 1}}}, 1, 10},
		{[][]OwnHeaderData{{{Name: "A", Bytes: 1}, {Name: "A", Bytes: 1}}}, 1, 10},
		{[][]OwnHeaderData{{{Name: "A", Bytes: 1<<20 + 1}}}, 1, math.MaxUint64},
		{valid, 0, 32768}, {valid, math.MaxUint64, math.MaxUint64}, {valid, 512, 32767},
	} {
		if _, err := OwnHeaderReservationData(tc.groups, tc.copies, tc.ceiling); err == nil {
			t.Fatal("unbounded header reservation accepted", tc)
		}
	}
}

func TestPendingObserverSourceBudgetIncludesBothSamplesAndSnapshotMemory(t *testing.T) {
	calls, bytes := PendingLinuxObserverReadBoundsData()
	// A former32GiB proposal omitted Snapshot's independent memory traversal.
	if calls != 36120 || bytes != 37874601240 || bytes <= 32<<30 || bytes >= 40<<30 {
		t.Fatal(calls, bytes)
	}
}
