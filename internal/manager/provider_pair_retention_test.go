package manager

import (
	"math"
	"strings"
	"testing"
	"time"

	"getarm.org/internal/providerpreparation"
)

func providerPairPreparing() providerpreparation.Progress {
	return providerpreparation.Progress{Schema: 1, Pair: providerpreparation.Hex32(strings.Repeat("e", 32)), Generation: "1", Revision: "1", Phase: providerpreparation.Preparing, Reason: "none", At: "2026-10-27T12:00:00Z"}
}

func TestProviderPairRetainingProgress(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 1, 0, 0, time.UTC)
	for _, tt := range []struct {
		cause  providerPairStopCause
		phase  providerpreparation.Phase
		reason string
	}{
		{providerPairCancel, providerpreparation.Revoked, "cancelled"},
		{providerPairExpired, providerpreparation.Revoked, "expired"},
		{providerPairClose, providerpreparation.Revoked, "close"},
		{providerPairClockUnknown, providerpreparation.Unknown, "clock"},
		{providerPairBindingUnknown, providerpreparation.Unknown, "binding"},
		{providerPairPersistenceUnknown, providerpreparation.Unknown, "persistence"},
	} {
		t.Run(tt.reason, func(t *testing.T) {
			old := providerPairPreparing()
			next, changed, err := retainProviderPairProgress(old, tt.cause, now)
			if err != nil || !changed || next.Revision != "2" || next.Phase != tt.phase || next.Reason != tt.reason || !providerPairProgressValid(next) {
				t.Fatal(next, changed, err)
			}
			same, changed, err := retainProviderPairProgress(next, providerPairExpired, time.Time{})
			if err != nil || changed || same != next {
				t.Fatal("repeat changed marker or required a new stamp", same, changed, err)
			}
			unknown, changed, err := retainProviderPairProgress(next, providerPairPersistenceUnknown, now.Add(time.Second))
			if tt.phase == providerpreparation.Revoked {
				if err != nil || !changed || unknown.Revision != "3" || unknown.Phase != providerpreparation.Unknown {
					t.Fatal(unknown, changed, err)
				}
			} else if err != nil || changed || unknown != next {
				t.Fatal("unknown was not absorbing", unknown, changed, err)
			}
		})
	}
	for _, tt := range []struct {
		name   string
		damage func(*providerpreparation.Progress)
	}{
		{"schema", func(p *providerpreparation.Progress) { p.Schema = 2 }},
		{"pair", func(p *providerpreparation.Progress) { p.Pair = "bad" }},
		{"generation", func(p *providerpreparation.Progress) { p.Generation = "01" }},
		{"revision", func(p *providerpreparation.Progress) { p.Revision = "2" }},
		{"phase", func(p *providerpreparation.Progress) { p.Phase = "settled" }},
		{"reason", func(p *providerpreparation.Progress) { p.Reason = "cancelled" }},
		{"stamp", func(p *providerpreparation.Progress) { p.At = "2026-10-27T12:00:00+00:00" }},
	} {
		t.Run("damaged/"+tt.name, func(t *testing.T) {
			p := providerPairPreparing()
			tt.damage(&p)
			if _, changed, err := retainProviderPairProgress(p, providerPairCancel, now); err == nil || changed {
				t.Fatal("malformed predecessor advanced", p, changed, err)
			}
		})
	}
	if _, _, err := retainProviderPairProgress(providerPairPreparing(), providerPairCancel, time.Time{}); err == nil {
		t.Fatal("invalid new audit stamp accepted")
	}
	if _, _, err := retainProviderPairProgress(providerPairPreparing(), providerPairStopCause(255), now); err == nil {
		t.Fatal("unknown cause accepted")
	}
}

func providerPairFloorIDs() [2]string {
	return [2]string{strings.Repeat("a", 32), strings.Repeat("b", 32)}
}
func providerPairPromised() []providerPairReadableCharge {
	ids := providerPairFloorIDs()
	return []providerPairReadableCharge{{ids[0], providerPairQuantity{cpuMillis: 1000, memoryBytes: 64 << 20}}, {ids[1], providerPairQuantity{diskBytes: 64 << 20}}}
}
func assertProviderPairFloor(t *testing.T, f providerPairFloor) {
	t.Helper()
	if f.total.cpuMillis < 1000 || f.total.memoryBytes < 64<<20 || f.total.diskBytes < 64<<20 || f.recordBytes != 104<<10 {
		t.Fatal("fixed pair promises were lost", f)
	}
}

func TestProviderPairRetainedFloor(t *testing.T) {
	ids := providerPairFloorIDs()
	promised := providerPairPromised()
	for _, tt := range []struct {
		name    string
		rows    []providerPairReadableCharge
		problem bool
	}{
		{"both", promised, false},
		{"worker missing", promised[1:], true},
		{"temp missing", promised[:1], true},
		{"both missing", nil, true},
		// Lifecycle state cannot lower these readable reservation hints: state is
		// deliberately absent from the floor input, not trusted as reclamation.
		{"terminal hints still consume", promised, false},
		{"larger readable", []providerPairReadableCharge{{ids[0], providerPairQuantity{cpuMillis: 2000, memoryBytes: 128 << 20}}, {ids[1], providerPairQuantity{diskBytes: 128 << 20}}}, true},
		{"duplicate identity maxima", append(append([]providerPairReadableCharge{}, promised...), promised[0]), false},
		{"unrelated", append(append([]providerPairReadableCharge{}, promised...), providerPairReadableCharge{strings.Repeat("f", 32), providerPairQuantity{diskBytes: 1}}), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := retainedProviderPairFloor(ids, tt.rows)
			assertProviderPairFloor(t, f)
			if (f.problem != nil) != tt.problem {
				t.Fatal(f)
			}
			if tt.name == "duplicate identity maxima" && f.total != (providerPairQuantity{cpuMillis: 1000, memoryBytes: 64 << 20, diskBytes: 64 << 20}) {
				t.Fatal("duplicate was added twice", f)
			}
			if tt.name == "larger readable" && f.total != (providerPairQuantity{cpuMillis: 2000, memoryBytes: 128 << 20, diskBytes: 128 << 20}) {
				t.Fatal("larger readable promise omitted", f)
			}
		})
	}
}

func TestProviderPairFloorDamage(t *testing.T) {
	ids := providerPairFloorIDs()
	promised := providerPairPromised()
	for _, tt := range []struct {
		name string
		ids  [2]string
		rows []providerPairReadableCharge
	}{
		{"invalid identity", [2]string{"bad", ids[1]}, promised},
		{"aliased identity", [2]string{ids[0], ids[0]}, promised},
		{"negative", ids, []providerPairReadableCharge{{ids[0], providerPairQuantity{cpuMillis: -1}}, {ids[1], providerPairQuantity{diskBytes: 64 << 20}}}},
		{"count sentinel", ids, make([]providerPairReadableCharge, providerpreparation.MaxMembers+1)},
		{"saturation", ids, []providerPairReadableCharge{{ids[0], providerPairQuantity{cpuMillis: math.MaxInt64, memoryBytes: math.MaxInt64, diskBytes: math.MaxInt64}}, {ids[1], providerPairQuantity{cpuMillis: math.MaxInt64, memoryBytes: math.MaxInt64, diskBytes: math.MaxInt64}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := retainedProviderPairFloor(tt.ids, tt.rows)
			assertProviderPairFloor(t, f)
			if f.problem == nil {
				t.Fatal("damaged inventory advertised exact floor", f)
			}
			if tt.name == "saturation" && f.total != (providerPairQuantity{math.MaxInt64, math.MaxInt64, math.MaxInt64}) {
				t.Fatal("overflow wrapped or omitted", f)
			}
		})
	}
}
