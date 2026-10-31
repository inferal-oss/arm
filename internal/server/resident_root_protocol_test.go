package server

import (
	"context"
	"sync"
	"testing"
	"time"
)

// These controls use only arithmetic and unissued DATA. They do not enter the
// genuine process role, create IO contexts, call a clock, or issue native owners.
func TestResidentProtocolDeadlineDataCapsWithoutClockOrConstructor(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		name    string
		startup time.Time
		finite  bool
		want    time.Time
	}{
		{"child-independent-five-seconds", time.Time{}, false, now.Add(5 * time.Second)},
		{"parent-earlier", now.Add(2 * time.Second), true, now.Add(2 * time.Second)},
		{"parent-equal", now.Add(5 * time.Second), true, now.Add(5 * time.Second)},
		{"parent-later", now.Add(8 * time.Second), true, now.Add(5 * time.Second)},
		{"parent-expired", now.Add(-time.Second), true, now.Add(-time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := residentProtocolDeadlineData(now, tc.startup, tc.finite); !got.Equal(tc.want) {
				t.Fatal("deadline DATA did not retain the smaller bound", got, tc.want)
			}
		})
	}
}

// A context interface may have a comparable dynamic type but an interface
// field containing a slice/map. No context method is invoked in this control.
type residentProtocolNestedContextData struct {
	context.Context
	value any
}

func TestResidentProtocolContextDataRejectsDynamicNonComparableValues(t *testing.T) {
	base := context.Background()
	for _, tc := range []struct {
		name string
		a, b context.Context
		want bool
	}{
		{"same-background", base, base, true},
		{"nil-left", nil, base, false},
		{"nil-right", base, nil, false},
		{"slice-left", residentProtocolNestedContextData{base, []int{1}}, base, false},
		{"slice-right", base, residentProtocolNestedContextData{base, []int{1}}, false},
		{"map-both", residentProtocolNestedContextData{base, map[string]int{"x": 1}}, residentProtocolNestedContextData{base, map[string]int{"x": 1}}, false},
		{"comparable-fields", residentProtocolNestedContextData{base, 1}, residentProtocolNestedContextData{base, 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameResidentProtocolContextData(tc.a, tc.b); got != tc.want {
				t.Fatal("context identity DATA mismatch", got, tc.want)
			}
		})
	}
}

func TestResidentRootProtocolUnissuedAndCopiedRefuseBeforePhaseEffects(t *testing.T) {
	root := &residentChildPending{mu: &sync.Mutex{}}
	root.self = root
	copied := *root
	for _, tc := range []struct {
		name string
		root *residentChildPending
	}{
		{"nil", nil}, {"zero", &residentChildPending{}}, {"unissued", root}, {"copied", &copied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.root.beginRootProtocolOriginal(); err != errResidentChildOriginal {
				t.Fatal("unissued Root entered protocol", err)
			}
			if _, err := tc.root.beginChildProtocolPhase(nil, 1); err != errResidentChildOriginal {
				t.Fatal("unissued Root constructed a phase", err)
			}
			if err := tc.root.exchangeRootProtocolOriginal(); err != errResidentChildOriginal {
				t.Fatal("unissued Root exchanged", err)
			}
		})
	}
	if root.protocol != nil || root.protocolTried || root.active != nil || root.protocolPhases != ([4]*residentProtocolPhase{}) || processResidentCoreEntry.call != nil {
		t.Fatal("refusal changed genuine protocol state")
	}
}

func TestResidentProtocolUnissuedPhaseCannotFinishOrCancel(t *testing.T) {
	for _, phase := range []*residentProtocolPhase{nil, {}, {index: -1}, {index: 4}, {index: 1, ioAttempted: true}, {index: 0, bootstrapPostConstructorClock: true, receiveAttempted: true, receiveReturned: true}} {
		if err := phase.finishIOOriginal(nil); err != errResidentChildOriginal {
			t.Fatal("unissued phase accepted completion", err)
		}
		if phase != nil && (phase.ioReturned || phase.cancelAttempted || phase.cancelReturned) {
			t.Fatal("unissued DATA recorded actual IO/cancel")
		}
	}
}

func TestResidentRootProtocolContextDataRefusesMissingPreparedAndFooter(t *testing.T) {
	for _, tc := range []struct {
		prepared *residentRootPreparedCall
		bound    *residentRootBoundCall
	}{
		{}, {prepared: &residentRootPreparedCall{}}, {bound: &residentRootBoundCall{}}, {prepared: &residentRootPreparedCall{}, bound: &residentRootBoundCall{}},
	} {
		if _, err := originalRootProtocolContextData(tc.prepared, tc.bound); err != errResidentChildOriginal {
			t.Fatal("missing original prepared/footer accepted", err)
		}
	}
}
