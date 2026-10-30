package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"getarm.org/internal/model"
	"github.com/charmbracelet/x/ansi"
)

func monitorInventoryReadingPairVisible(text, id string) bool {
	lines := strings.Split(ansi.Strip(text), "\n")
	prefix := strings.SplitN(id, "-long", 2)[0]
	for row := 0; row+1 < len(lines); row++ {
		if strings.Contains(lines[row], "> [") && strings.Contains(lines[row+1], prefix) {
			return true
		}
	}
	return false
}

// Six ordinary guards only. Original local page-scroll input arranges reading;
// these cases make no native wheel/PTY or live polling/transport claim.
func TestMonitorPickerInventoryPreservesReadingOffset(t *testing.T) {
	for _, disk := range []bool{true, false} {
		for _, transition := range []string{"unchanged", "reorder", "all-member-disappears"} {
			t.Run(fmt.Sprintf("disk=%t/%s", disk, transition), func(t *testing.T) {
				m, bookkeeping := monitorFocusFixture(t, 45, 14, disk, false)
				all := transition == "all-member-disappears"
				if all {
					*m.ioSelection(disk) = ioSelection{All: true}
				}
				focus := monitorFocusID(disk, 8)
				monitorFocusArrange(t, m, focus)
				t.Logf("inventory-reading-initial-frame\n%s", ansi.Strip(m.View().Content))
				monitorFocusVisible(t, m)
				if t.Failed() || m.pickerIndex() != 8 {
					t.Fatal("invalid initial visible-focus arrangement")
				}
				monitorFocusKey(t, m, "pgdown")
				monitorFocusKey(t, m, "pgdown")
				readingOffset := m.viewport.YOffset()
				monitorFocusObserve(t, m, "inventory-reading-before-update", bookkeeping)
				if m.ioPicker.selectedID != focus || readingOffset <= 0 ||
					monitorInventoryReadingPairVisible(m.viewport.View(), focus) || monitorInventoryReadingPairVisible(m.View().Content, focus) {
					t.Fatal("local page reading did not put the focused pair offscreen")
				}
				selection := monitorFocusCopy(m.ioSelection(disk))
				other := monitorFocusCopy(m.ioSelection(!disk))
				var otherIDs []string
				for _, item := range m.ioItems(!disk) {
					otherIDs = append(otherIDs, item.ID)
				}
				items := m.ioItems(disk)
				if len(items) != 24 || items[8].ID != focus {
					t.Fatal("unexpected synthetic inventory")
				}
				nextItems := make([]model.MonitorIO, 0, len(items))
				for _, item := range items {
					if transition == "unchanged" || item.ID != focus {
						nextItems = append(nextItems, item)
					}
				}
				wantFocus := focus
				switch transition {
				case "reorder":
					nextItems = append(nextItems, items[8])
				case "all-member-disappears":
					wantFocus = nextItems[8].ID
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
				m.acceptMonitor(page)
				m.syncViewport()
				monitorFocusObserve(t, m, "inventory-reading-after-update", bookkeeping)
				if readingOffset+m.viewport.Height() > m.viewport.TotalLineCount() {
					t.Fatal("saved reading offset would exceed new content bounds")
				}
				if m.viewport.YOffset() != readingOffset {
					t.Errorf("inventory update moved deliberate reading: got %d want %d", m.viewport.YOffset(), readingOffset)
				}
				options, index := m.ioOptions(), m.pickerIndex()
				if m.ioPicker.selectedID != wantFocus || index < 0 || index >= len(options) || options[index].id != wantFocus {
					t.Errorf("offscreen focus is not canonical: got %q want %q", m.ioPicker.selectedID, wantFocus)
				}
				if monitorInventoryReadingPairVisible(m.viewport.View(), wantFocus) || monitorInventoryReadingPairVisible(m.View().Content, wantFocus) {
					t.Error("inventory update revealed focus during deliberate page reading")
				}
				if got := monitorFocusCopy(m.ioSelection(disk)); !reflect.DeepEqual(got, selection) {
					t.Error("inventory update changed local chart selection policy")
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
					t.Error("inventory update changed the other reported inventory")
				}
			})
		}
	}
}
