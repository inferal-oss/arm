package tui

import (
	"fmt"
	"reflect"
	"testing"

	"getarm.org/internal/model"
)

// Source-only follow-up: four ordinary cases, reusing the accepted fixture
// unchanged. No Init, Update/stream command, timer or collector is executed.
func TestMonitorPickerInventoryKeepsVisibleFocus(t *testing.T) {
	for _, disk := range []bool{true, false} {
		for _, transition := range []string{"reorder", "all-member-disappears"} {
			t.Run(fmt.Sprintf("disk=%t/%s", disk, transition), func(t *testing.T) {
				m, bookkeeping := monitorFocusFixture(t, 45, 14, disk, false)
				all := transition == "all-member-disappears"
				if all {
					*m.ioSelection(disk) = ioSelection{All: true}
				}
				focus := monitorFocusID(disk, 8)
				monitorFocusArrange(t, m, focus)
				monitorFocusObserve(t, m, "inventory-focused-before-update", bookkeeping)
				if got := monitorFocusVisible(t, m); got != focus {
					t.Error("initial counter focus changed", got)
				}
				if t.Failed() {
					t.Fatal("invalid initial visible-focus arrangement; no inventory diagnosis")
				}
				priorIndex := m.pickerIndex()
				if priorIndex != 8 || m.viewport.YOffset() == 0 {
					t.Fatal("inventory fixture was not visibly focused below the first rows")
				}
				selection := monitorFocusCopy(m.ioSelection(disk))
				other := monitorFocusCopy(m.ioSelection(!disk))
				var otherIDs []string
				for _, item := range m.ioItems(!disk) {
					otherIDs = append(otherIDs, item.ID)
				}

				items := m.ioItems(disk)
				if len(items) != 24 || items[priorIndex].ID != focus {
					t.Fatal("unexpected synthetic inventory arrangement")
				}
				nextItems := make([]model.MonitorIO, 0, len(items))
				for _, item := range items {
					if item.ID != focus {
						nextItems = append(nextItems, item)
					}
				}
				wantFocus := focus
				if transition == "reorder" {
					nextItems = append(nextItems, items[priorIndex])
				} else {
					// Match the accepted local-action fallback: the nearest
					// remaining index, without changing chart membership mode.
					wantFocus = nextItems[priorIndex].ID
				}
				snapshot := *m.page.Latest
				if disk {
					snapshot.DiskIO = nextItems
				} else {
					snapshot.Network = nextItems
				}
				page := m.page
				page.Latest = &snapshot
				page.Cursor++
				// Direct inert snapshot delivery: no stream/read command and
				// no repair/reveal helper is called after this transition.
				m.acceptMonitor(page)
				m.syncViewport()
				monitorFocusObserve(t, m, "inventory-focused-after-update", bookkeeping)

				if m.ioPicker.selectedID != wantFocus {
					t.Errorf("inventory focus got %q want %q", m.ioPicker.selectedID, wantFocus)
				}
				monitorFocusVisible(t, m)
				if got := monitorFocusCopy(m.ioSelection(disk)); !reflect.DeepEqual(got, selection) {
					t.Errorf("inert inventory update changed local selection policy: got %+v want %+v", got, selection)
				}
				wantIDs := map[string]bool{monitorFocusID(disk, 0): true, monitorFocusID(disk, 7): true}
				if all {
					wantIDs = make(map[string]bool, len(nextItems))
					for _, item := range nextItems {
						wantIDs[item.ID] = true
					}
				}
				monitorFocusAssertScope(t, m, disk, all, wantIDs)
				monitorFocusAssertOther(t, m, disk, other)
				var gotOtherIDs []string
				for _, item := range m.ioItems(!disk) {
					gotOtherIDs = append(gotOtherIDs, item.ID)
				}
				if !reflect.DeepEqual(gotOtherIDs, otherIDs) {
					t.Error("inventory update changed the other chart's reported members")
				}
			})
		}
	}
}
